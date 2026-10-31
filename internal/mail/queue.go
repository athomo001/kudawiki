package mail

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/athomo001/kudawiki/internal/crypto"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/oklog/ulid/v2"
)

// Cola persistente (§12): la acción encola y el worker entrega, con reintentos. Un servidor SMTP
// caído no hace fallar la acción ni la demora.
const (
	pollEvery   = 15 * time.Second
	batchSize   = 20
	maxAttempts = 10
	firstRetry  = time.Minute
	maxRetry    = time.Hour
	maxErrorLen = 500
)

var ErrUnknownTemplate = errors.New("unknown email template")

// Message es un correo para la cola: a quién, en qué idioma, qué plantilla y con qué datos.
// Lines son los renglones del resumen diario; Unsubscribe, el enlace de baja de los avisos.
type Message struct {
	To          string            `json:"to"`
	Lang        string            `json:"lang"`
	Template    string            `json:"template"`
	Vars        map[string]string `json:"vars,omitempty"`
	URL         string            `json:"url,omitempty"`
	Lines       []Line            `json:"lines,omitempty"`
	Unsubscribe string            `json:"unsubscribe,omitempty"`
}

// Line es un renglón del resumen: email.line.<Template> con sus datos.
type Line struct {
	Template string            `json:"template"`
	Vars     map[string]string `json:"vars,omitempty"`
}

type queueStore interface {
	EnqueueEmail(ctx context.Context, e repo.OutboxEmail, at time.Time) error
	DueEmails(ctx context.Context, now time.Time, limit int) ([]repo.OutboxEmail, error)
	EmailSent(ctx context.Context, id string, at time.Time) error
	EmailRetry(ctx context.Context, id, lastError string, next time.Time) error
	EmailFailed(ctx context.Context, id, lastError string, at time.Time) error
}

// Enabled dice si hay correo: sin él, quien lo usa ofrece la alternativa manual (§8.5.3).
func (s *Service) Enabled(ctx context.Context) (bool, error) {
	c, err := s.settings(ctx, false)
	return c.Enabled, err
}

// Enqueue deja el correo en la cola, con el cuerpo cifrado con la MASTER_KEY: lleva enlaces con
// token, que no pueden quedar legibles en un respaldo (§6). El id es el dato asociado: un cuerpo
// copiado a otra fila no se abre.
func (s *Service) Enqueue(ctx context.Context, m Message) error {
	if _, ok := templates[m.Template]; !ok {
		return ErrUnknownTemplate
	}
	for _, l := range m.Lines {
		if !lineTemplates[l.Template] {
			return ErrUnknownTemplate
		}
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	id := ulid.Make().String()
	enc, err := crypto.Seal(s.key, raw, []byte(id))
	if err != nil {
		return err
	}
	if err := s.store.EnqueueEmail(ctx, repo.OutboxEmail{ID: id, Template: m.Template, PayloadEnc: enc, KeyVersion: crypto.MasterKeyVersion}, s.now()); err != nil {
		return err
	}
	select { // despierta al worker sin esperar su próxima vuelta
	case s.wake <- struct{}{}:
	default:
	}
	return nil
}

// Run es el worker: una pasada al arrancar, después cada 15 s o apenas se encola algo, hasta que
// ctx termine.
func (s *Service) Run(ctx context.Context, log *slog.Logger) {
	tick := time.NewTicker(pollEvery)
	defer tick.Stop()
	for {
		if err := s.Flush(ctx, log); err != nil && ctx.Err() == nil {
			log.Error("email queue", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-s.wake:
		}
	}
}

// Flush entrega lo que ya tocaba. Un correo que falla se reintenta con espera creciente (1 min,
// 2, 4… hasta 1 h) y al décimo intento la cola se rinde.
func (s *Service) Flush(ctx context.Context, log *slog.Logger) error {
	due, err := s.store.DueEmails(ctx, s.now(), batchSize)
	if err != nil || len(due) == 0 {
		return err
	}
	c, err := s.settings(ctx, true)
	if err != nil {
		return err
	}
	for _, e := range due {
		if ctx.Err() != nil {
			return nil
		}
		err := s.deliverQueued(ctx, c, e)
		now := s.now()
		var retry *SendError
		switch {
		case err == nil:
			err = s.store.EmailSent(ctx, e.ID, now)
		case errors.As(err, &retry) && e.Attempts+1 < maxAttempts:
			log.Warn("email not sent, will retry", "id", e.ID, "template", e.Template, "attempt", e.Attempts+1, "err", retry.Detail)
			err = s.store.EmailRetry(ctx, e.ID, truncate(retry.Detail), now.Add(min(firstRetry<<e.Attempts, maxRetry)))
		default:
			log.Error("email given up", "id", e.ID, "template", e.Template, "attempts", e.Attempts+1, "err", err)
			err = s.store.EmailFailed(ctx, e.ID, truncate(err.Error()), now)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// deliverQueued abre el cuerpo y lo entrega. Sin correo configurado cuenta como un fallo de
// entrega, que se reintenta: quizás el superadmin lo está arreglando. Un cuerpo que no se abre
// (otra llave) no se reintenta.
func (s *Service) deliverQueued(ctx context.Context, c config, e repo.OutboxEmail) error {
	if e.KeyVersion != crypto.MasterKeyVersion {
		return errors.New("payload sealed with another master key version")
	}
	raw, err := crypto.Open(s.key, e.PayloadEnc, []byte(e.ID))
	if err != nil {
		return err
	}
	var m Message
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	if !c.Enabled {
		return &SendError{ErrNotConfigured.Error()}
	}
	msg, err := s.build(ctx, c, m)
	if err != nil {
		return err
	}
	return s.deliver(ctx, c, m.To, msg)
}

func truncate(s string) string {
	if len(s) > maxErrorLen {
		return s[:maxErrorLen]
	}
	return s
}
