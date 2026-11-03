package audit

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/athomo001/kudawiki/internal/repo"
)

// Sink recibe la copia de cada evento fuera de la wiki (§11.2): la salida del servicio, que
// guardan journald o el driver de logs de Docker, o un servidor syslog.
type Sink interface {
	Send(ctx context.Context, events []Exported) error
}

type forwardStore interface {
	AuditAfter(ctx context.Context, after int64, limit int) ([]repo.AuditRow, error)
	JobState(ctx context.Context, name string) (string, error)
	SetJobState(ctx context.Context, name, valueJSON string) error
}

// Forwarder copia la auditoría a un Sink. Lee la tabla desde el último evento enviado, que queda
// guardado: un reinicio o un syslog caído no pierden eventos, se mandan después. Así la copia
// existe aunque alguien borre filas al final de la cadena, que la cadena sola no detecta (§11.1).
type Forwarder struct {
	Store    forwardStore
	Sink     Sink
	Interval time.Duration
	Log      *slog.Logger
}

// ForwardJob es el nombre del job en job_state.
const ForwardJob = "audit_forward"

type forwardState struct {
	LastID int64  `json:"last_id"`
	Error  string `json:"error,omitempty"`
}

// Run manda lo pendiente cada Interval hasta que ctx termine.
func (f Forwarder) Run(ctx context.Context) {
	failing := false
	for {
		err := f.Once(ctx)
		switch {
		case err != nil && ctx.Err() == nil && !failing:
			// Una vez por caída, no en cada intento: el syslog caído llenaría el log.
			f.Log.Error("audit forward failed; it will retry", "err", err)
			failing = true
		case err == nil && failing:
			f.Log.Info("audit forward recovered")
			failing = false
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(f.Interval):
		}
	}
}

// Once manda todo lo pendiente, en tandas de 500, y guarda hasta dónde llegó.
func (f Forwarder) Once(ctx context.Context) error {
	var st forwardState
	if raw, err := f.Store.JobState(ctx, ForwardJob); err != nil {
		return err
	} else if raw != "" {
		json.Unmarshal([]byte(raw), &st)
	}
	for {
		rows, err := f.Store.AuditAfter(ctx, st.LastID, 500)
		if err != nil || len(rows) == 0 {
			return err
		}
		events := make([]Exported, len(rows))
		for i, row := range rows {
			// Con la IP: la copia es para quien investiga, igual que la vista del superadmin.
			events[i] = Export(repo.AuditEntry{AuditRow: row}, true)
		}
		if err := f.Sink.Send(ctx, events); err != nil {
			st.Error = err.Error()
			b, _ := json.Marshal(st)
			f.Store.SetJobState(context.WithoutCancel(ctx), ForwardJob, string(b))
			return err
		}
		st = forwardState{LastID: rows[len(rows)-1].ID}
		b, _ := json.Marshal(st)
		if err := f.Store.SetJobState(ctx, ForwardJob, string(b)); err != nil {
			return err
		}
	}
}

// StdoutSink escribe un JSON por evento, con "audit_event" como mensaje, en la salida del
// servicio. Con systemd la guarda journald; en Docker, el driver de logs configurado (journald,
// syslog, fluentd…). La salida no es de la wiki: el superadmin de la wiki no la puede tocar.
type StdoutSink struct{ W io.Writer }

func (s StdoutSink) Send(_ context.Context, events []Exported) error {
	enc := json.NewEncoder(s.W)
	enc.SetEscapeHTML(false)
	for _, e := range events {
		if err := enc.Encode(struct {
			Time  string   `json:"time"`
			Msg   string   `json:"msg"`
			Event Exported `json:"audit_event"`
		}{time.Now().UTC().Format(time.RFC3339Nano), "audit_event", e}); err != nil {
			return err
		}
	}
	return nil
}

// SyslogSink manda cada evento a un servidor syslog en formato RFC 5424, con el JSON del evento
// como mensaje. tls:// verifica el certificado del servidor (§21.2.1: las conexiones salientes
// van cifradas); tcp:// y udp:// en claro solo se aceptan hacia la misma máquina (un rsyslog
// local que reenvía). Por TCP y TLS cada mensaje va con su largo delante (RFC 6587).
type SyslogSink struct {
	network, addr string
	tls           *tls.Config
	host          string
	timeout       time.Duration
}

// NewSyslogSink valida la dirección (tls://host:puerto, o tcp:// y udp:// hacia la misma
// máquina). caFile es opcional: la CA del servidor si no es una del sistema.
func NewSyslogSink(address, caFile string) (*SyslogSink, error) {
	u, err := url.Parse(address)
	if err != nil || u.Host == "" || u.Port() == "" {
		return nil, fmt.Errorf("audit.syslog.address must be tls://host:port (or tcp:// or udp:// to this machine), got %q", address)
	}
	s := &SyslogSink{addr: u.Host, timeout: 10 * time.Second}
	s.host, _ = os.Hostname()
	switch u.Scheme {
	case "tls":
		s.network = "tcp"
		s.tls = &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12}
		if caFile != "" {
			pem, err := os.ReadFile(caFile)
			if err != nil {
				return nil, fmt.Errorf("audit.syslog.ca_file: %w", err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(pem) {
				return nil, errors.New("audit.syslog.ca_file has no PEM certificates")
			}
			s.tls.RootCAs = pool
		}
	case "tcp", "udp":
		if !isLoopback(u.Hostname()) {
			return nil, fmt.Errorf("audit.syslog.address: %s:// sends in clear, so only to this machine; use tls:// for %s", u.Scheme, u.Hostname())
		}
		s.network = u.Scheme
	default:
		return nil, fmt.Errorf("audit.syslog.address must start with tls://, tcp:// or udp://, got %q", address)
	}
	return s, nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Facility authpriv (10), severidad notice (5): PRI = 10*8 + 5.
const syslogPRI = "<85>1 "

func (s *SyslogSink) Send(ctx context.Context, events []Exported) error {
	d := net.Dialer{Timeout: s.timeout}
	var conn net.Conn
	var err error
	if s.tls != nil {
		conn, err = (&tls.Dialer{NetDialer: &d, Config: s.tls}).DialContext(ctx, s.network, s.addr)
	} else {
		conn, err = d.DialContext(ctx, s.network, s.addr)
	}
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(s.timeout + time.Duration(len(events))*10*time.Millisecond))
	host := s.host
	if host == "" {
		host = "-"
	}
	for _, e := range events {
		body, err := json.Marshal(e)
		if err != nil {
			return err
		}
		// TIMESTAMP HOSTNAME APP-NAME PROCID MSGID STRUCTURED-DATA MSG
		msg := syslogPRI + e.At + " " + host + " kudawiki - audit - " + string(body)
		var buf bytes.Buffer
		if s.network == "tcp" {
			buf.WriteString(strconv.Itoa(len(msg)) + " ")
		}
		buf.WriteString(msg)
		if _, err := conn.Write(buf.Bytes()); err != nil {
			return err
		}
	}
	return nil
}
