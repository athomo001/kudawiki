package repo

import (
	"context"
	"database/sql"
	"errors"
)

// La marca de la empresa es de la instalación, no de un área (SPEC §15.1): no lleva Scope.

// SiteAsset es un logo o favicon de la empresa en una de sus variantes.
type SiteAsset struct {
	Kind, Variant, MimeType string
	Data                    []byte
	SHA256                  string
	UploadedBy              string
}

// SiteSettings devuelve las claves guardadas que empiezan con prefix, con su valor en JSON.
func (r *Repo) SiteSettings(ctx context.Context, prefix string) (map[string]string, error) {
	rows, err := r.db.Read.QueryContext(ctx, `SELECT key, value_json FROM site_settings
		WHERE substr(key, 1, length(?)) = ? AND value_json IS NOT NULL`, prefix, prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// SetSiteSettings guarda las claves en una transacción; un valor nil borra la fila (vuelve al
// valor por defecto).
func (r *Repo) SetSiteSettings(ctx context.Context, values map[string]*string, by string) error {
	return r.SaveSiteSettings(ctx, values, nil, by)
}

// SiteSecret devuelve un secreto de configuración tal como está guardado: cifrado, con la
// versión de la llave. Lo abre quien tiene la MASTER_KEY; ErrNotFound si no hay.
func (r *Repo) SiteSecret(ctx context.Context, key string) ([]byte, int, error) {
	var enc []byte
	var version int
	err := r.db.Read.QueryRowContext(ctx, `SELECT secret_enc, key_version FROM site_settings WHERE key = ? AND secret_enc IS NOT NULL`,
		key).Scan(&enc, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, ErrNotFound
	}
	return enc, version, err
}

// SealedSetting es un secreto ya cifrado; Enc nil lo borra.
type SealedSetting struct {
	Enc        []byte
	KeyVersion int
}

// SaveSiteSettings guarda valores y secretos juntos: un cambio de servidor SMTP con su
// contraseña no queda a medias.
func (r *Repo) SaveSiteSettings(ctx context.Context, values map[string]*string, secrets map[string]SealedSetting, by string) error {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for k, v := range secrets {
		if v.Enc == nil {
			_, err = tx.ExecContext(ctx, `DELETE FROM site_settings WHERE key = ?`, k)
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO site_settings (key, secret_enc, key_version, updated_by) VALUES (?, ?, ?, ?)
				ON CONFLICT(key) DO UPDATE SET secret_enc = excluded.secret_enc, key_version = excluded.key_version,
					updated_by = excluded.updated_by, updated_at = CURRENT_TIMESTAMP`, k, v.Enc, v.KeyVersion, by)
		}
		if err != nil {
			return err
		}
	}
	for k, v := range values {
		if v == nil {
			_, err = tx.ExecContext(ctx, `DELETE FROM site_settings WHERE key = ?`, k)
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO site_settings (key, value_json, updated_by) VALUES (?, ?, ?)
				ON CONFLICT(key) DO UPDATE SET value_json = excluded.value_json, updated_by = excluded.updated_by, updated_at = CURRENT_TIMESTAMP`,
				k, *v, by)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SiteAssetVersions devuelve el sha256 de cada variante guardada, como "kind/variant".
func (r *Repo) SiteAssetVersions(ctx context.Context) (map[string]string, error) {
	rows, err := r.db.Read.QueryContext(ctx, `SELECT kind || '/' || variant, sha256 FROM site_assets`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (r *Repo) SiteAsset(ctx context.Context, kind, variant string) (SiteAsset, error) {
	a := SiteAsset{Kind: kind, Variant: variant}
	err := r.db.Read.QueryRowContext(ctx, `SELECT mime_type, data, sha256, uploaded_by FROM site_assets WHERE kind = ? AND variant = ?`,
		kind, variant).Scan(&a.MimeType, &a.Data, &a.SHA256, &a.UploadedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return SiteAsset{}, ErrNotFound
	}
	return a, err
}

// ReplaceSiteAsset deja solo las variantes nuevas de ese archivo: un logo nuevo sin íconos no
// convive con los íconos del anterior.
func (r *Repo) ReplaceSiteAsset(ctx context.Context, kind string, variants []SiteAsset) error {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM site_assets WHERE kind = ?`, kind); err != nil {
		return err
	}
	for _, a := range variants {
		if _, err := tx.ExecContext(ctx, `INSERT INTO site_assets (kind, variant, mime_type, data, sha256, uploaded_by) VALUES (?, ?, ?, ?, ?, ?)`,
			kind, a.Variant, a.MimeType, a.Data, a.SHA256, a.UploadedBy); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteSiteAsset borra el archivo con sus variantes y devuelve el sha256 del original, para la
// auditoría; ErrNotFound si no había.
func (r *Repo) DeleteSiteAsset(ctx context.Context, kind string) (string, error) {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var sum string
	err = tx.QueryRowContext(ctx, `SELECT sha256 FROM site_assets WHERE kind = ? AND variant = 'original'`, kind).Scan(&sum)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM site_assets WHERE kind = ?`, kind); err != nil {
		return "", err
	}
	return sum, tx.Commit()
}
