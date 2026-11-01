package repo

import (
	"context"
	"database/sql"
	"time"
)

// Passkey es una credencial WebAuthn tal como está guardada (SPEC §8.1): la clave pública COSE y
// lo que hay que comprobar en cada uso. Quien la verifica es auth.
type Passkey struct {
	ID             string // credential ID en base64url
	UserID         string
	Name           string
	PublicKey      []byte
	SignCount      uint32
	BackupEligible bool
	BackupState    bool
	RPID           string
	CreatedAt      time.Time
	LastUsedAt     time.Time // cero si nunca se usó
}

func (r *Repo) CreatePasskey(ctx context.Context, p Passkey) error {
	_, err := r.db.Write.ExecContext(ctx,
		`INSERT INTO passkeys (id, user_id, name, public_key, sign_count, backup_eligible, backup_state, rp_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.UserID, p.Name, p.PublicKey, p.SignCount, p.BackupEligible, p.BackupState, p.RPID, p.CreatedAt)
	return conflict(err)
}

// PasskeysOf son las passkeys de una persona, de la más antigua a la más nueva.
func (r *Repo) PasskeysOf(ctx context.Context, userID string) ([]Passkey, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT id, user_id, name, public_key, sign_count, backup_eligible, backup_state, rp_id, created_at, last_used_at
		 FROM passkeys WHERE user_id = ? ORDER BY created_at, id`, userID)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanPasskey)
}

func scanPasskey(row scanner) (Passkey, error) {
	var p Passkey
	var used sql.NullTime
	err := row.Scan(&p.ID, &p.UserID, &p.Name, &p.PublicKey, &p.SignCount, &p.BackupEligible, &p.BackupState, &p.RPID, &p.CreatedAt, &used)
	p.LastUsedAt = used.Time
	return p, err
}

// UsePasskey guarda lo que deja un uso: el contador de firmas, si está respaldada y cuándo.
func (r *Repo) UsePasskey(ctx context.Context, id string, signCount uint32, backupState bool, at time.Time) error {
	_, err := r.db.Write.ExecContext(ctx,
		`UPDATE passkeys SET sign_count = ?, backup_state = ?, last_used_at = ? WHERE id = ?`, signCount, backupState, at, id)
	return err
}

// DeletePasskey borra una passkey solo si es de esa persona: la de otra da ErrNotFound, igual que
// una que no existe.
func (r *Repo) DeletePasskey(ctx context.Context, userID, id string) error {
	res, err := r.db.Write.ExecContext(ctx, `DELETE FROM passkeys WHERE user_id = ? AND id = ?`, userID, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		err = ErrNotFound
	}
	return err
}

// DeletePasskeys borra todas las de una persona: el restablecimiento del 2FA (§13.6, §14.2).
func (r *Repo) DeletePasskeys(ctx context.Context, userID string) (int64, error) {
	res, err := r.db.Write.ExecContext(ctx, `DELETE FROM passkeys WHERE user_id = ?`, userID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
