package health

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"time"
)

// MaxClockSkew es el desfase con el servidor NTP que ya se avisa (SPEC §21.2.5, control 8.17):
// las fechas de la auditoría y los vencimientos dependen del reloj.
const MaxClockSkew = 2 * time.Second

// clockTTL: cada cuánto se vuelve a preguntar; /readyz y el panel se piden seguido y un servidor
// NTP público limita a quien pregunta de más.
const clockTTL = 10 * time.Minute

// Clock compara el reloj del servidor con un servidor NTP (SNTP, RFC 4330: una pregunta y una
// respuesta UDP de 48 bytes). Sin servidor configurado no hace nada.
type Clock struct {
	Server string // host o host:puerto (123 por omisión)
	Now    func() time.Time

	mu     sync.Mutex
	at     time.Time
	offset time.Duration
	err    error
}

// ClockState es lo que muestran /readyz y la pantalla Salud.
type ClockState struct {
	Server   string `json:"server"`
	OffsetMS int64  `json:"offset_ms"`
	Error    string `json:"error,omitempty"`
}

// State devuelve el último desfase medido, midiéndolo si pasó clockTTL. ok=false sin servidor.
func (c *Clock) State(ctx context.Context) (ClockState, bool) {
	if c == nil || c.Server == "" {
		return ClockState{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.at.IsZero() || c.Now().Sub(c.at) > clockTTL {
		c.offset, c.err = sntp(ctx, c.Server, c.Now)
		c.at = c.Now()
	}
	st := ClockState{Server: c.Server, OffsetMS: c.offset.Milliseconds()}
	if c.err != nil {
		st.Error = c.err.Error()
	}
	return st, true
}

// ClockChecks son los problemas del reloj para la pantalla Salud del superadmin.
func ClockChecks(st ClockState, ok bool) []Problem {
	switch {
	case !ok:
		return nil
	case st.Error != "":
		return []Problem{{Check: "clock.unreachable", Severity: Warning, Detail: st.Server}}
	case time.Duration(abs(st.OffsetMS))*time.Millisecond > MaxClockSkew:
		return []Problem{{Check: "clock.skew", Severity: Error, Detail: (time.Duration(st.OffsetMS) * time.Millisecond).String()}}
	}
	return nil
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// ntpEpoch: los segundos de NTP cuentan desde 1900.
const ntpEpoch = 2208988800

func sntp(ctx context.Context, server string, now func() time.Time) (time.Duration, error) {
	if _, _, err := net.SplitHostPort(server); err != nil {
		server = net.JoinHostPort(server, "123")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", server)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	req := make([]byte, 48)
	req[0] = 0x23 // LI 0, versión 4, modo 3 (cliente)
	t1 := now()
	if _, err := conn.Write(req); err != nil {
		return 0, err
	}
	resp := make([]byte, 48)
	n, err := conn.Read(resp)
	t4 := now()
	if err != nil {
		return 0, err
	}
	if n < 48 || resp[0]&0x7 != 4 || resp[1] == 0 { // modo servidor; stratum 0 es un "kiss of death"
		return 0, errors.New("invalid NTP response")
	}
	ts := func(b []byte) time.Time {
		sec, frac := binary.BigEndian.Uint32(b[:4]), binary.BigEndian.Uint32(b[4:8])
		return time.Unix(int64(sec)-ntpEpoch, int64((uint64(frac)*1e9)>>32))
	}
	t2, t3 := ts(resp[32:40]), ts(resp[40:48])
	return (t2.Sub(t1) + t3.Sub(t4)) / 2, nil
}
