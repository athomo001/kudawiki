package repo

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// AuthToken es un enlace de un solo uso; F2 solo usa invitaciones, el restablecimiento llega en F4.
type AuthToken struct {
	TokenHash string
	CreatedBy string
	ExpiresAt time.Time
}

// CreateInvitedUser crea a la persona sin contraseña junto con su invitación, en una
// transacción: no queda un usuario invitado sin enlace para entrar.
func (r *Repo) CreateInvitedUser(ctx context.Context, u User, t AuthToken) error {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO users (id, email, full_name, locale, invited_at) VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		u.ID, u.Email, u.FullName, u.Locale); err != nil {
		return conflict(err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO auth_tokens (token_hash, user_id, kind, created_by, expires_at) VALUES (?, ?, 'invite', ?, ?)`,
		t.TokenHash, u.ID, t.CreatedBy, t.ExpiresAt.UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

// ReplaceInvite deja a la persona con una sola invitación pendiente, la nueva: las anteriores sin
// usar se borran en la misma transacción, así un enlace viejo no sirve (SPEC §8.5.3).
func (r *Repo) ReplaceInvite(ctx context.Context, userID string, t AuthToken) error {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM auth_tokens WHERE user_id = ? AND kind = 'invite' AND used_at IS NULL`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO auth_tokens (token_hash, user_id, kind, created_by, expires_at) VALUES (?, ?, 'invite', ?, ?)`,
		t.TokenHash, userID, t.CreatedBy, t.ExpiresAt.UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

// AcceptInvite consume la invitación y fija la contraseña en una sola transacción: un token
// vencido, usado o de otro tipo da ErrNotFound, y no puede usarse dos veces (SPEC §8.5).
// Devuelve el id de la persona.
func (r *Repo) AcceptInvite(ctx context.Context, tokenHash string, now time.Time, passwordHash string) (string, error) {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var userID string
	var expires time.Time
	err = tx.QueryRowContext(ctx,
		`UPDATE auth_tokens SET used_at = ? WHERE token_hash = ? AND kind = 'invite' AND used_at IS NULL
		 RETURNING user_id, expires_at`, now.UTC(), tokenHash).Scan(&userID, &expires)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !now.Before(expires)) {
		return "", ErrNotFound // el rollback deja el token vencido sin marcar
	}
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, invited_at = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		passwordHash, userID); err != nil {
		return "", err
	}
	return userID, tx.Commit()
}
