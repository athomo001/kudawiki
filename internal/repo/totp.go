package repo

import (
	"context"
	"database/sql"
	"errors"
)

// TOTP es el segundo factor de una persona tal como está guardado (SPEC §8.1). SecretEnc va
// cifrado con la MASTER_KEY; quien lo abre es auth.
type TOTP struct {
	SecretEnc  []byte
	KeyVersion int
	Enabled    bool
	LastStep   int64
}

// TOTPOf devuelve el TOTP de una persona; SecretEnc vacío si nunca empezó un alta.
func (r *Repo) TOTPOf(ctx context.Context, userID string) (TOTP, error) {
	var t TOTP
	var version sql.NullInt64
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT totp_secret_enc, totp_key_version, totp_enabled, totp_last_step FROM users WHERE id = ?`, userID).
		Scan(&t.SecretEnc, &version, &t.Enabled, &t.LastStep)
	if errors.Is(err, sql.ErrNoRows) {
		return TOTP{}, ErrNotFound
	}
	t.KeyVersion = int(version.Int64)
	return t, err
}

// SetPendingTOTP guarda el secreto de un alta que falta confirmar. Si el TOTP ya está activo no
// lo toca y responde ErrConflict: un alta nueva no reemplaza al que funciona.
func (r *Repo) SetPendingTOTP(ctx context.Context, userID string, secretEnc []byte, keyVersion int) error {
	res, err := r.db.Write.ExecContext(ctx,
		`UPDATE users SET totp_secret_enc = ?, totp_key_version = ?, totp_last_step = 0, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND totp_enabled = 0`, secretEnc, keyVersion, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrConflict
	}
	return nil
}

// EnableTOTP confirma el alta: activa el TOTP, deja usado el paso del código que lo confirmó y
// reemplaza los códigos de recuperación, todo junto.
func (r *Repo) EnableTOTP(ctx context.Context, userID string, step int64, codeHashes []string) error {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`UPDATE users SET totp_enabled = 1, totp_last_step = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND totp_enabled = 0 AND totp_secret_enc IS NOT NULL`, step, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM totp_recovery_codes WHERE user_id = ?`, userID); err != nil {
		return err
	}
	for _, h := range codeHashes {
		if _, err := tx.ExecContext(ctx, `INSERT INTO totp_recovery_codes (user_id, code_hash) VALUES (?, ?)`, userID, h); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UseTOTPStep marca un paso como usado si es posterior al último aceptado. false: el código ya
// se usó (o uno más nuevo), y no vale (RFC 6238 §5.2). Un solo UPDATE: dos pedidos con el mismo
// código no pueden pasar los dos.
func (r *Repo) UseTOTPStep(ctx context.Context, userID string, step int64) (bool, error) {
	res, err := r.db.Write.ExecContext(ctx,
		`UPDATE users SET totp_last_step = ? WHERE id = ? AND totp_enabled = 1 AND totp_last_step < ?`, step, userID, step)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// UseRecoveryCode gasta un código de recuperación; false si no existe o ya se usó.
func (r *Repo) UseRecoveryCode(ctx context.Context, userID, codeHash string) (bool, error) {
	res, err := r.db.Write.ExecContext(ctx,
		`UPDATE totp_recovery_codes SET used_at = CURRENT_TIMESTAMP WHERE user_id = ? AND code_hash = ? AND used_at IS NULL`, userID, codeHash)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// RecoveryCodesLeft cuenta los códigos de recuperación sin usar.
func (r *Repo) RecoveryCodesLeft(ctx context.Context, userID string) (int, error) {
	var n int
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM totp_recovery_codes WHERE user_id = ? AND used_at IS NULL`, userID).Scan(&n)
	return n, err
}

// DisableTOTP borra el TOTP y sus códigos: lo desactiva la persona, lo restablece el superadmin
// o `kudawiki admin reset-2fa` (§13.6).
func (r *Repo) DisableTOTP(ctx context.Context, userID string) error {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`UPDATE users SET totp_secret_enc = NULL, totp_key_version = NULL, totp_enabled = 0, totp_last_step = 0,
		 updated_at = CURRENT_TIMESTAMP WHERE id = ?`, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM totp_recovery_codes WHERE user_id = ?`, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// IsAreaAdmin dice si la persona es admin de alguna área. El espacio personal no cuenta: su
// dueño es su admin (§7), y si contara el 2FA sería obligatorio para todos (§8.1).
func (r *Repo) IsAreaAdmin(ctx context.Context, userID string) (bool, error) {
	var ok bool
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM workspace_members m JOIN workspaces w ON w.id = m.workspace_id
		 WHERE m.user_id = ? AND m.role = 'admin' AND w.kind = 'area')`, userID).Scan(&ok)
	return ok, err
}
