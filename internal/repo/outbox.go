package repo

import (
	"context"
	"time"
)

// La cola de correos es de la instalación, no de un área (SPEC §12): no lleva Scope. El cuerpo
// llega y sale cifrado; repo no lo lee.

// OutboxEmail es un correo pendiente, con su cuerpo cifrado.
type OutboxEmail struct {
	ID, Template string
	PayloadEnc   []byte
	KeyVersion   int
	Attempts     int
}

func (r *Repo) EnqueueEmail(ctx context.Context, e OutboxEmail, at time.Time) error {
	_, err := r.db.Write.ExecContext(ctx, `INSERT INTO email_outbox (id, template, payload_enc, key_version, next_attempt_at) VALUES (?, ?, ?, ?, ?)`,
		e.ID, e.Template, e.PayloadEnc, e.KeyVersion, at.UTC())
	return err
}

// DueEmails son los pendientes cuyo turno ya llegó, los más antiguos primero.
func (r *Repo) DueEmails(ctx context.Context, now time.Time, limit int) ([]OutboxEmail, error) {
	rows, err := r.db.Read.QueryContext(ctx, `SELECT id, template, payload_enc, key_version, attempts FROM email_outbox
		WHERE payload_enc IS NOT NULL AND next_attempt_at <= ? ORDER BY next_attempt_at, id LIMIT ?`, now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OutboxEmail
	for rows.Next() {
		var e OutboxEmail
		if err := rows.Scan(&e.ID, &e.Template, &e.PayloadEnc, &e.KeyVersion, &e.Attempts); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// EmailSent borra el cuerpo: entregado, el enlace ya no tiene por qué quedar en la base.
func (r *Repo) EmailSent(ctx context.Context, id string, at time.Time) error {
	_, err := r.db.Write.ExecContext(ctx, `UPDATE email_outbox SET payload_enc = NULL, key_version = NULL, attempts = attempts + 1,
		last_error = NULL, sent_at = ? WHERE id = ?`, at.UTC(), id)
	return err
}

// EmailRetry anota el intento fallido y el próximo turno.
func (r *Repo) EmailRetry(ctx context.Context, id, lastError string, next time.Time) error {
	_, err := r.db.Write.ExecContext(ctx, `UPDATE email_outbox SET attempts = attempts + 1, last_error = ?, next_attempt_at = ? WHERE id = ?`,
		lastError, next.UTC(), id)
	return err
}

// EmailFailed se rinde: borra el cuerpo y deja el último error.
func (r *Repo) EmailFailed(ctx context.Context, id, lastError string, at time.Time) error {
	_, err := r.db.Write.ExecContext(ctx, `UPDATE email_outbox SET payload_enc = NULL, key_version = NULL, attempts = attempts + 1,
		last_error = ?, failed_at = ? WHERE id = ?`, lastError, at.UTC(), id)
	return err
}

// PurgeEmailOutbox borra los entregados y los rendidos antes de cutoff; los pendientes nunca.
func (r *Repo) PurgeEmailOutbox(ctx context.Context, cutoff time.Time) error {
	_, err := r.db.Write.ExecContext(ctx, `DELETE FROM email_outbox WHERE payload_enc IS NULL AND coalesce(sent_at, failed_at) < ?`, cutoff.UTC())
	return err
}
