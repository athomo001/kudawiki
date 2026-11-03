package repo

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Ciclo de vida de un área (SPEC §3.5). Son del superadmin desde el panel: van por el ID del
// área, como el resto de la administración de áreas, y solo tocan áreas (kind = 'area'): un
// espacio personal no se archiva ni se borra así (§7).

// activeAreaOf filtra los recordatorios (propuestas, lecturas, vigencia) a las áreas activas: en
// una archivada nadie puede hacer lo que se le recuerda, y una desactivada no se ve.
func activeAreaOf(table string) string {
	return ` AND ` + table + `.workspace_id IN (SELECT id FROM workspaces WHERE status = 'active' AND delete_at IS NULL)`
}

// SetWorkspaceStatus cambia el estado: active, archived o deactivated.
func (r *Repo) SetWorkspaceStatus(ctx context.Context, id, status string) error {
	res, err := r.db.Write.ExecContext(ctx, `UPDATE workspaces SET status = ? WHERE id = ? AND kind = 'area'`, status, id)
	return affected(res, err)
}

// RequestWorkspaceDelete deja el área por borrar: desde ya nadie la ve, y se purga en at.
func (r *Repo) RequestWorkspaceDelete(ctx context.Context, id, by string, at time.Time) error {
	res, err := r.db.Write.ExecContext(ctx, `UPDATE workspaces SET delete_at = ?, delete_requested_by = NULLIF(?, '')
		WHERE id = ? AND kind = 'area' AND delete_at IS NULL`, at.UTC(), by, id)
	return affected(res, err)
}

// CancelWorkspaceDelete la devuelve a como estaba antes del pedido.
func (r *Repo) CancelWorkspaceDelete(ctx context.Context, id string) error {
	res, err := r.db.Write.ExecContext(ctx, `UPDATE workspaces SET delete_at = NULL, delete_requested_by = NULL
		WHERE id = ? AND kind = 'area' AND delete_at IS NOT NULL`, id)
	return affected(res, err)
}

// DueWorkspaceDeletes son las áreas cuyo plazo de gracia ya pasó.
func (r *Repo) DueWorkspaceDeletes(ctx context.Context, now time.Time) ([]Workspace, error) {
	rows, err := r.db.Read.QueryContext(ctx, `SELECT `+workspaceColumns+` FROM workspaces w
		WHERE w.kind = 'area' AND w.delete_at IS NOT NULL AND w.delete_at <= ?`, now.UTC())
	if err != nil {
		return nil, err
	}
	return collect(rows, scanWorkspace)
}

// PurgeWorkspace borra el área para siempre: páginas, revisiones, media, sellados (sus llaves
// envueltas se van con ellos: crypto-shredding, §6), propuestas, vínculos y miembros, por
// cascada. Devuelve los archivos para borrarlos del disco. Vuelve a mirar delete_at dentro de la
// transacción: un borrado cancelado mientras tanto no se purga.
func (r *Repo) PurgeWorkspace(ctx context.Context, id string, now time.Time) (pages int, files []MediaFile, err error) {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback()
	var due bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM workspaces WHERE id = ? AND kind = 'area'
		AND delete_at IS NOT NULL AND delete_at <= ?)`, id, now.UTC()).Scan(&due); err != nil {
		return 0, nil, err
	}
	if !due {
		return 0, nil, ErrNotFound
	}
	if files, err = personalPurge(ctx, tx, id, &pages); err != nil {
		return 0, nil, err
	}
	return pages, files, tx.Commit()
}

// RenameWorkspaceSlug cambia la dirección del área y guarda la vieja como alias (B17). Si el
// slug nuevo era un alias de esta misma área, deja de serlo.
func (r *Repo) RenameWorkspaceSlug(ctx context.Context, id, slug string) (old string, err error) {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx, `SELECT slug FROM workspaces WHERE id = ? AND kind = 'area'`, id).Scan(&old); errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	} else if err != nil {
		return "", err
	}
	if old == slug {
		return old, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE workspaces SET slug = ? WHERE id = ?`, slug, id); err != nil {
		return "", conflict(err)
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM workspace_aliases WHERE slug = ? AND workspace_id = ?`, []any{slug, id}},
		{`INSERT INTO workspace_aliases (slug, workspace_id) VALUES (?, ?)
		  ON CONFLICT (slug) DO UPDATE SET workspace_id = excluded.workspace_id, created_at = CURRENT_TIMESTAMP`, []any{old, id}},
	} {
		if _, err := tx.ExecContext(ctx, q.sql, q.args...); err != nil {
			return "", err
		}
	}
	return old, tx.Commit()
}

// MembershipAliases son los slugs viejos de las áreas de una persona: la interfaz los usa para
// llevar una URL vieja a la nueva. Solo de sus áreas: a nadie se le dice a dónde se fue otra.
func (r *Repo) MembershipAliases(ctx context.Context, userID string) (map[string][]string, error) {
	rows, err := r.db.Read.QueryContext(ctx, `SELECT a.workspace_id, a.slug FROM workspace_aliases a
		JOIN workspace_members m ON m.workspace_id = a.workspace_id AND m.user_id = ?
		JOIN workspaces w ON w.id = a.workspace_id
		WHERE NOT EXISTS (SELECT 1 FROM workspaces x WHERE x.slug = a.slug)`+visibleArea+` ORDER BY a.slug`, userID)
	if err != nil {
		return nil, err
	}
	type alias struct{ ws, slug string }
	all, err := collect(rows, func(s scanner) (alias, error) {
		var a alias
		return a, s.Scan(&a.ws, &a.slug)
	})
	out := map[string][]string{}
	for _, a := range all {
		out[a.ws] = append(out[a.ws], a.slug)
	}
	return out, err
}

// ForeignSealedHolders son las personas de fuera del área con acceso a alguno de sus sellados:
// antes de borrarla se les avisa (§3.5), porque lo pierden sin ser parte del área.
func (r *Repo) ForeignSealedHolders(ctx context.Context, wsID string) ([]User, error) {
	rows, err := r.db.Read.QueryContext(ctx, `SELECT `+userColumns+` FROM users WHERE is_active = 1 AND id IN (
		SELECT k.holder_user_id FROM sealed_keys k JOIN sealed_pages s ON s.id = k.page_id
		WHERE s.workspace_id = ?1 AND k.holder_type = 'user'
		  AND NOT EXISTS (SELECT 1 FROM workspace_members m WHERE m.workspace_id = ?1 AND m.user_id = k.holder_user_id))
		ORDER BY full_name`, wsID)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanUser)
}
