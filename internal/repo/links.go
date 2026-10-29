package repo

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// ErrHidden: la operación depende de una página que quien la pide no ve (p. ej. un padre de
// nivel superior); quien llama responde lo mismo que si la ruta estuviera ocupada (§19.35).
var ErrHidden = errors.New("hidden pages")

type TagCount struct {
	Tag   string
	Count int
}

// setLinks reemplaza los enlaces salientes de la página. to_page_id se resuelve por ruta;
// si no hay página, el enlace queda roto (NULL) hasta que alguien la cree.
func setLinks(ctx context.Context, tx *sql.Tx, s Scope, pageID string, slugs []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM page_links WHERE from_page_id = ?`, pageID); err != nil {
		return err
	}
	for _, slug := range slugs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO page_links (workspace_id, from_page_id, to_slug, to_page_id)
			 VALUES (?, ?, ?, (SELECT id FROM pages WHERE workspace_id = ? AND slug = ? AND deleted_at IS NULL))`,
			s.WsID, pageID, slug, s.WsID, slug); err != nil {
			return err
		}
	}
	return nil
}

// resolveLinksTo repara los enlaces que apuntaban a una ruta que ahora existe.
func resolveLinksTo(ctx context.Context, tx *sql.Tx, s Scope, slug, pageID string) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE page_links SET to_page_id = ? WHERE workspace_id = ? AND to_slug = ?`, pageID, s.WsID, slug)
	return err
}

func (r *Repo) SetTags(ctx context.Context, s Scope, pageID string, tags []string) error {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM page_tags WHERE page_id = ? AND workspace_id = ?`, pageID, s.WsID); err != nil {
		return err
	}
	for _, t := range tags {
		if _, err := tx.ExecContext(ctx, `INSERT INTO page_tags (page_id, workspace_id, tag) VALUES (?, ?, ?)`, pageID, s.WsID, t); err != nil {
			return err
		}
	}
	// Las etiquetas pesan en la búsqueda (SPEC §9.1): el índice se actualiza en la misma transacción.
	if _, err := tx.ExecContext(ctx, `UPDATE pages_fts SET tags = ? WHERE rowid = (SELECT search_rowid FROM pages WHERE id = ?)`, strings.Join(tags, " "), pageID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Repo) PageTags(ctx context.Context, s Scope, pageID string) ([]string, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT t.tag FROM page_tags t JOIN pages p ON p.id = t.page_id
		 WHERE t.page_id = ? AND t.workspace_id = ? AND p.deleted_at IS NULL AND p.min_rank <= ? ORDER BY t.tag`, pageID, s.WsID, s.MaxRank)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (string, error) {
		var t string
		return t, row.Scan(&t)
	})
}

// Tags cuenta solo sobre las páginas que quien pregunta puede leer: una etiqueta que solo
// llevan páginas N3 no existe para un lector (SPEC §4.6).
func (r *Repo) Tags(ctx context.Context, s Scope) ([]TagCount, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT MIN(t.tag), COUNT(*) FROM page_tags t JOIN pages p ON p.id = t.page_id
		 WHERE t.workspace_id = ? AND p.deleted_at IS NULL AND p.min_rank <= ?
		 GROUP BY t.tag COLLATE NOCASE ORDER BY COUNT(*) DESC, MIN(t.tag)`, s.WsID, s.MaxRank)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (TagCount, error) {
		var c TagCount
		return c, row.Scan(&c.Tag, &c.Count)
	})
}

func (r *Repo) PagesByTag(ctx context.Context, s Scope, tag string) ([]PageRef, error) {
	return r.pageRefs(ctx,
		`SELECT p.slug, p.title, p.min_rank, p.description, p.icon FROM page_tags t JOIN pages p ON p.id = t.page_id
		 WHERE t.workspace_id = ? AND p.deleted_at IS NULL AND p.min_rank <= ? AND t.tag = ? ORDER BY p.title`, s.WsID, s.MaxRank, tag)
}

// Backlinks es "qué enlaza aquí": solo páginas que quien lee ve. Que una página N3 enlace a
// la actual tampoco se revela (SPEC §4.7).
func (r *Repo) Backlinks(ctx context.Context, s Scope, pageID string) ([]PageRef, error) {
	return r.pageRefs(ctx,
		`SELECT p.slug, p.title, p.min_rank, p.description, p.icon FROM page_links l JOIN pages p ON p.id = l.from_page_id
		 WHERE l.workspace_id = ? AND p.deleted_at IS NULL AND p.min_rank <= ? AND l.to_page_id = ? AND l.from_page_id <> l.to_page_id
		 ORDER BY p.title`, s.WsID, s.MaxRank, pageID)
}

func (r *Repo) pageRefs(ctx context.Context, query string, args ...any) ([]PageRef, error) {
	rows, err := r.db.Read.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanPageRef)
}

// PageByAlias resuelve una ruta vieja a la actual, solo si la página es visible.
func (r *Repo) PageByAlias(ctx context.Context, s Scope, oldSlug string) (string, error) {
	var slug string
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT p.slug FROM page_aliases a JOIN pages p ON p.id = a.page_id
		 WHERE a.workspace_id = ? AND a.old_slug = ? AND p.deleted_at IS NULL AND p.min_rank <= ?`, s.WsID, oldSlug, s.MaxRank).Scan(&slug)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return slug, err
}

type movedPage struct {
	id, slug, title, description, markdown, lang string
	rank, revision                               int
}

// Rewrite reescribe en una página los [[enlaces]] a rutas movidas (moves: vieja → nueva) y
// devuelve lo derivado del contenido nuevo (enlaces e índice de búsqueda).
type Rewrite func(title, description, markdown, lang string, moves map[string]string) (string, []string, IndexDoc)

// MovePages mueve from y las subpáginas que quien mueve ve a to, en una transacción: cambia
// las rutas, deja alias, registra una revisión "moved" por página y reescribe los enlaces a
// las rutas viejas. Las subpáginas que no ve se quedan donde están (§19.36): nadie altera lo
// que no ve, y para quien mueve no hay diferencia que delate algo. La reescritura de enlaces
// sí toca páginas de cualquier nivel, también las de la papelera: es mantenimiento del sistema
// y no muestra nada, y así una página restaurada no vuelve con enlaces a rutas viejas.
// Devuelve el id de la página movida y cuántas se movieron.
func (r *Repo) MovePages(ctx context.Context, s Scope, actorID, from, to string, rewrite Rewrite) (string, int, error) {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx,
		`SELECT id, slug, title, description, content_markdown, lang, min_rank, current_revision FROM pages
		 WHERE workspace_id = ? AND deleted_at IS NULL AND min_rank <= ? AND (slug = ? OR slug LIKE ?)`, s.WsID, s.MaxRank, from, from+"/%")
	if err != nil {
		return "", 0, err
	}
	moved, err := collect(rows, scanMoved)
	if err != nil {
		return "", 0, err
	}
	var rootID string
	for _, p := range moved {
		if p.slug == from {
			rootID = p.id
		}
	}
	if rootID == "" {
		return "", 0, ErrNotFound
	}
	// El destino hereda el nivel de sus padres; uno que quien mueve no ve deja la ruta no disponible.
	anc, err := maxAncestorRank(ctx, tx, s, to)
	if err != nil {
		return "", 0, err
	}
	if anc > s.MaxRank {
		return "", 0, ErrHidden
	}
	moves := make(map[string]string, len(moved))
	targets := []any{s.WsID}
	for _, p := range moved {
		moves[p.slug] = to + strings.TrimPrefix(p.slug, from)
		targets = append(targets, moves[p.slug])
	}
	var taken int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pages WHERE workspace_id = ? AND deleted_at IS NULL AND slug IN (?`+
		strings.Repeat(",?", len(moved)-1)+`)`, targets...).Scan(&taken); err != nil {
		return "", 0, err
	}
	if taken > 0 {
		return "", 0, ErrConflict
	}

	for _, p := range moved {
		newSlug := moves[p.slug]
		if _, err := tx.ExecContext(ctx,
			`UPDATE pages SET slug = ?, min_rank = ?, current_revision = current_revision + 1, updated_by = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
			newSlug, max(p.rank, anc), actorID, p.id); err != nil {
			return "", 0, err
		}
		if err := insertRevision(ctx, tx, s, p.id, p.revision+1, PageWrite{
			Title: p.title, Markdown: p.markdown, Kind: "moved", Summary: p.slug + " → " + newSlug, AuthorID: actorID,
		}); err != nil {
			return "", 0, err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO page_aliases (workspace_id, old_slug, page_id) VALUES (?, ?, ?)`, s.WsID, p.slug, p.id); err != nil {
			return "", 0, err
		}
	}
	if err := rewriteLinkers(ctx, tx, s, actorID, "[["+from+"]] → [["+to+"]]", moves, rewrite); err != nil {
		return "", 0, err
	}
	return rootID, len(moved), tx.Commit()
}

// rewriteLinkers actualiza las páginas que enlazaban a las rutas movidas, con una revisión cada una.
func rewriteLinkers(ctx context.Context, tx *sql.Tx, s Scope, actorID, summary string, moves map[string]string, rewrite Rewrite) error {
	old := make([]any, 0, len(moves)+1)
	old = append(old, s.WsID, s.WsID)
	for slug := range moves {
		old = append(old, slug)
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT p.id, p.slug, p.title, p.description, p.content_markdown, p.lang, p.min_rank, p.current_revision FROM pages p
		 WHERE p.workspace_id = ? AND p.id IN (SELECT from_page_id FROM page_links
		   WHERE workspace_id = ? AND to_slug IN (?`+strings.Repeat(",?", len(moves)-1)+`))`, old...)
	if err != nil {
		return err
	}
	linkers, err := collect(rows, scanMoved)
	if err != nil {
		return err
	}
	for _, p := range linkers {
		markdown, links, index := rewrite(p.title, p.description, p.markdown, p.lang, moves)
		if markdown != p.markdown {
			if err := writeIndex(ctx, tx, p.id, index); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE pages SET content_markdown = ?, current_revision = current_revision + 1, updated_by = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
				markdown, actorID, p.id); err != nil {
				return err
			}
			if err := insertRevision(ctx, tx, s, p.id, p.revision+1, PageWrite{
				Title: p.title, Markdown: markdown, Kind: "updated", Summary: summary, AuthorID: actorID,
			}); err != nil {
				return err
			}
		}
		if err := setLinks(ctx, tx, s, p.id, links); err != nil {
			return err
		}
	}
	return nil
}
