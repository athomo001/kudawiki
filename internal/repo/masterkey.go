package repo

import (
	"context"
	"fmt"
)

// Versiones de la MASTER_KEY y los secretos que cifra (SPEC §6.5). La llave nunca llega aquí:
// solo su KeyCheck y los valores cifrados, que la rotación re-cifra afuera.

// MasterKeyVersion es una llave que tuvo la instalación, por su KeyCheck.
type MasterKeyVersion struct {
	Version int
	Check   []byte
	Retired bool
}

func (r *Repo) MasterKeyVersions(ctx context.Context) ([]MasterKeyVersion, error) {
	rows, err := r.db.Read.QueryContext(ctx, `SELECT version, key_check, retired_at IS NOT NULL FROM master_key_versions ORDER BY version`)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (MasterKeyVersion, error) {
		var v MasterKeyVersion
		return v, row.Scan(&v.Version, &v.Check, &v.Retired)
	})
}

func (r *Repo) AddMasterKeyVersion(ctx context.Context, version int, check []byte) error {
	_, err := r.db.Write.ExecContext(ctx, `INSERT INTO master_key_versions (version, key_check) VALUES (?, ?)`, version, check)
	return conflict(err)
}

func (r *Repo) RetireMasterKeyVersion(ctx context.Context, version int) error {
	_, err := r.db.Write.ExecContext(ctx,
		`UPDATE master_key_versions SET retired_at = CURRENT_TIMESTAMP WHERE version = ? AND retired_at IS NULL`, version)
	return err
}

// Secret es un valor cifrado con la MASTER_KEY. Kind dice de dónde es: sealed (la DEK de un
// sellado envuelta con la llave, ID es el sellado), totp (el secreto de una persona), setting
// (un ajuste secreto, ID es su nombre) u outbox (un correo en cola).
type Secret struct {
	Kind, ID string
	Enc      []byte
}

// secretsFrom son todas las columnas cifradas con la MASTER_KEY; ?1 es la versión.
// #nosec G101 -- nombres de columnas cifradas, no credenciales
const secretsFrom = `SELECT 'sealed', page_id, wrapped_dek FROM sealed_keys WHERE holder_type = 'master' AND key_version %[1]s ?1
	UNION ALL SELECT 'totp', id, totp_secret_enc FROM users WHERE totp_secret_enc IS NOT NULL AND totp_key_version %[1]s ?1
	UNION ALL SELECT 'setting', key, secret_enc FROM site_settings WHERE secret_enc IS NOT NULL AND key_version %[1]s ?1
	UNION ALL SELECT 'outbox', id, payload_enc FROM email_outbox WHERE payload_enc IS NOT NULL AND key_version %[1]s ?1`

// SecretsAt son hasta limit secretos que siguen cifrados con esa versión.
func (r *Repo) SecretsAt(ctx context.Context, version, limit int) ([]Secret, error) {
	rows, err := r.db.Read.QueryContext(ctx, fmt.Sprintf(secretsFrom, "=")+` LIMIT ?2`, version, limit)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (Secret, error) {
		var s Secret
		return s, row.Scan(&s.Kind, &s.ID, &s.Enc)
	})
}

// SecretsNotAt cuenta los secretos cifrados con otra versión: con la rotación terminada, cero.
func (r *Repo) SecretsNotAt(ctx context.Context, version int) (int, error) {
	var n int
	err := r.db.Read.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+fmt.Sprintf(secretsFrom, "<>")+`)`, version).Scan(&n)
	return n, err
}

// ResealSecrets guarda los secretos re-cifrados con la versión to, en una transacción y con
// secure_delete: la rotación existe porque la llave vieja pudo filtrarse, y lo cifrado con ella
// no debe quedar en las páginas libres de la base. Cada fila tiene que seguir en from.
func (r *Repo) ResealSecrets(ctx context.Context, from, to int, secrets []Secret) error {
	if _, err := r.db.Write.ExecContext(ctx, `PRAGMA secure_delete = ON`); err != nil {
		return err
	}
	// Se apaga después del Rollback (los defer corren al revés): la conexión de escritura es una.
	defer r.db.Write.ExecContext(context.WithoutCancel(ctx), `PRAGMA secure_delete = OFF`)
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, s := range secrets {
		q, ok := resealSQL[s.Kind]
		if !ok {
			return fmt.Errorf("unknown secret kind %q", s.Kind)
		}
		if err := affected(tx.ExecContext(ctx, q, s.Enc, to, s.ID, from)); err != nil {
			return fmt.Errorf("reseal %s %s: %w", s.Kind, s.ID, err)
		}
	}
	return tx.Commit()
}

var resealSQL = map[string]string{
	"sealed":  `UPDATE sealed_keys SET wrapped_dek = ?, key_version = ? WHERE page_id = ? AND holder_type = 'master' AND key_version = ?`,
	"totp":    `UPDATE users SET totp_secret_enc = ?, totp_key_version = ? WHERE id = ? AND totp_key_version = ?`,
	"setting": `UPDATE site_settings SET secret_enc = ?, key_version = ? WHERE key = ? AND key_version = ?`,
	"outbox":  `UPDATE email_outbox SET payload_enc = ?, key_version = ? WHERE id = ? AND key_version = ?`,
}

// TruncateWAL pasa el WAL a la base y lo deja vacío: lo que se borró con secure_delete tampoco
// queda en el WAL.
func (r *Repo) TruncateWAL(ctx context.Context) error {
	_, err := r.db.Write.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}

// Optimize deja que SQLite actualice las estadísticas que usa para planear las consultas (§13.4).
func (r *Repo) Optimize(ctx context.Context) error {
	_, err := r.db.Write.ExecContext(ctx, `PRAGMA optimize`)
	return err
}
