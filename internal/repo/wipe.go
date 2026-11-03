package repo

import (
	"context"
	"database/sql"
	"errors"
)

// WipeReport es lo que borró Wipe, para la auditoría: cantidades, nunca nombres.
type WipeReport struct {
	Areas int `json:"areas"`
	Users int `json:"users"`
	Pages int `json:"pages"`
	Files int `json:"files"`
}

// Wipe deja la instalación vacía salvo la cuenta keep, que tiene que ser un superadmin activo
// (§13.12, decisión del dueño del 2026-11-03): todas las áreas y los espacios personales con lo
// que tienen (páginas, revisiones, media, sellados con sus llaves, propuestas…, por cascada), las
// plantillas globales, el resto de las cuentas con sus sesiones y la cola de correo. Quedan la
// configuración del sitio (marca, correo, directorio), la auditoría, que es evidencia (§11.2), y
// el estado de los respaldos. Con secure_delete, como al borrar un sellado (§6): lo borrado no
// queda legible en las páginas libres de la base. Devuelve los archivos para borrarlos del disco.
func (r *Repo) Wipe(ctx context.Context, keep string) (WipeReport, []MediaFile, error) {
	var rep WipeReport
	if _, err := r.db.Write.ExecContext(ctx, `PRAGMA secure_delete = ON`); err != nil {
		return rep, nil, err
	}
	defer r.db.Write.ExecContext(context.WithoutCancel(ctx), `PRAGMA secure_delete = OFF`)
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return rep, nil, err
	}
	defer tx.Rollback()

	var ok bool
	if err := tx.QueryRowContext(ctx, `SELECT is_global_admin = 1 AND is_active = 1 AND offboarded_at IS NULL FROM users WHERE id = ?`,
		keep).Scan(&ok); errors.Is(err, sql.ErrNoRows) || (err == nil && !ok) {
		return rep, nil, ErrNotFound
	} else if err != nil {
		return rep, nil, err
	}

	rows, err := tx.QueryContext(ctx, `SELECT workspace_id, id FROM media_assets
		UNION ALL SELECT s.workspace_id, m.id FROM sealed_media m JOIN sealed_pages s ON s.id = m.page_id`)
	if err != nil {
		return rep, nil, err
	}
	files, err := collect(rows, func(row scanner) (MediaFile, error) {
		var f MediaFile
		return f, row.Scan(&f.WsID, &f.ID)
	})
	if err != nil {
		return rep, nil, err
	}
	rep.Files = len(files)
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM workspaces WHERE kind = 'area'),
		(SELECT COUNT(*) FROM users WHERE id <> ?), (SELECT COUNT(*) FROM pages)`, keep).Scan(&rep.Areas, &rep.Users, &rep.Pages); err != nil {
		return rep, nil, err
	}

	for _, q := range []struct {
		sql  string
		args []any
	}{
		// Lo que queda (la configuración del sitio) deja de apuntar a cuentas que se van.
		{`UPDATE site_settings SET updated_by = ? WHERE updated_by IS NOT NULL AND updated_by <> ?`, []any{keep, keep}},
		{`UPDATE site_assets SET uploaded_by = ? WHERE uploaded_by <> ?`, []any{keep, keep}},
		// El índice de búsqueda no tiene clave foránea: se vacía a mano.
		{`DELETE FROM pages_fts`, nil},
		{`DELETE FROM pages_title_trgm`, nil},
		{`DELETE FROM workspaces`, nil},
		{`DELETE FROM page_templates`, nil},
		{`DELETE FROM email_outbox`, nil},
		{`DELETE FROM access_reviews`, nil},
		{`UPDATE users SET offboarding_by = NULL WHERE offboarding_by IS NOT NULL`, nil},
		{`DELETE FROM auth_tokens WHERE created_by IS NOT NULL AND created_by <> ?`, []any{keep}},
		{`DELETE FROM users WHERE id <> ?`, []any{keep}},
	} {
		if _, err := tx.ExecContext(ctx, q.sql, q.args...); err != nil {
			return rep, nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return rep, nil, err
	}
	// El WAL todavía tiene las páginas viejas enteras: se pasan a la base (ya con secure_delete)
	// y se vacía, como en la rotación de la llave maestra (§6.5).
	_, err = r.db.Write.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return rep, files, err
}
