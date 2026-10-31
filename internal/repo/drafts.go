package repo

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Borradores del autoguardado (SPEC §4.11). Son de una sola persona: toda consulta filtra por
// user_id además del Scope. El de una página que existe se encuentra por la página, y solo si
// quien pregunta todavía la ve; el de una página nueva, por su ruta.

type Draft struct {
	Slug      string // la ruta de la página nueva; en una que existe, la que tenía al guardar
	Title     string
	Markdown  string
	Base      int // revisión sobre la que se escribía; 0 en una página nueva
	NewPage   bool
	UpdatedAt time.Time
}

// draftMatch elige el borrador de slug: el de la página que está en esa ruta (si quien pregunta
// la ve) o el de una página nueva con esa ruta. Recibe user, área, área, slug, nivel y slug.
const draftMatch = `d.user_id = ? AND d.workspace_id = ? AND (
	d.page_id = (SELECT id FROM pages WHERE workspace_id = ? AND slug = ? AND deleted_at IS NULL AND min_rank <= ?)
	OR (d.page_id IS NULL AND d.target_slug = ?))`

// SaveDraft guarda (o reemplaza) el borrador de slug. Si en esa ruta hay una página que quien
// escribe ve, el borrador queda atado a ella; si no, es de una página nueva.
func (r *Repo) SaveDraft(ctx context.Context, s Scope, userID, slug string, d Draft) error {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var pageID sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT id FROM pages WHERE workspace_id = ? AND slug = ? AND deleted_at IS NULL AND min_rank <= ?`,
		s.WsID, slug, s.MaxRank).Scan(&pageID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	conflictTarget := `(user_id, workspace_id, target_slug) WHERE page_id IS NULL`
	if pageID.Valid {
		conflictTarget = `(user_id, page_id) WHERE page_id IS NOT NULL`
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO page_drafts (user_id, workspace_id, page_id, target_slug, base_revision, title, content_markdown)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT `+conflictTarget+` DO UPDATE SET target_slug = excluded.target_slug, base_revision = excluded.base_revision,
		  title = excluded.title, content_markdown = excluded.content_markdown, updated_at = CURRENT_TIMESTAMP`,
		userID, s.WsID, pageID, slug, d.Base, d.Title, d.Markdown); err != nil {
		return err
	}
	return tx.Commit()
}

// DraftBySlug es el borrador de quien pregunta en esa ruta. Si hay dos (el de una página nueva
// que otra persona creó mientras tanto, y el de la página), gana el de la página.
func (r *Repo) DraftBySlug(ctx context.Context, s Scope, userID, slug string) (Draft, error) {
	var d Draft
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT d.target_slug, d.title, d.content_markdown, d.base_revision, d.page_id IS NULL, d.updated_at
		 FROM page_drafts d WHERE `+draftMatch+` ORDER BY d.page_id IS NULL LIMIT 1`,
		userID, s.WsID, s.WsID, slug, s.MaxRank, slug).
		Scan(&d.Slug, &d.Title, &d.Markdown, &d.Base, &d.NewPage, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Draft{}, ErrNotFound
	}
	return d, err
}

// NewPageDrafts son los borradores de páginas nuevas de quien pregunta en el área, sin el
// contenido: el editor los ofrece al abrir una página nueva.
func (r *Repo) NewPageDrafts(ctx context.Context, s Scope, userID string) ([]Draft, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT target_slug, title, updated_at FROM page_drafts
		 WHERE user_id = ? AND workspace_id = ? AND page_id IS NULL ORDER BY updated_at DESC`, userID, s.WsID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (Draft, error) {
		d := Draft{NewPage: true}
		return d, row.Scan(&d.Slug, &d.Title, &d.UpdatedAt)
	})
}

// DeleteDraft borra los borradores de quien pregunta en esa ruta: al publicar, al proponer o al
// descartar. No tener ninguno no es un error.
func (r *Repo) DeleteDraft(ctx context.Context, s Scope, userID, slug string) error {
	_, err := r.db.Write.ExecContext(ctx, `DELETE FROM page_drafts AS d WHERE `+draftMatch,
		userID, s.WsID, s.WsID, slug, s.MaxRank, slug)
	return err
}

// PurgeDrafts borra los borradores sin tocar desde antes de cutoff (§4.11: 30 días). Es del job
// diario, sobre todas las áreas.
func (r *Repo) PurgeDrafts(ctx context.Context, cutoff time.Time) error {
	_, err := r.db.Write.ExecContext(ctx, `DELETE FROM page_drafts WHERE updated_at < ?`, sqlTime(cutoff))
	return err
}
