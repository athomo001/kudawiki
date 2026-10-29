package repo

import (
	"context"
	"time"
)

// Consultas de la salud del contenido (SPEC §13.7). Solo leen. Las del área van con su Scope;
// OrphanIndexRows es del sistema (el índice no tiene área) y la usan doctor y el superadmin.

// BrokenLink es un [[enlace]] de una página viva a una ruta que no existe o está en la papelera.
type BrokenLink struct{ FromSlug, ToSlug string }

func (r *Repo) BrokenLinks(ctx context.Context, s Scope) ([]BrokenLink, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT f.slug, l.to_slug FROM page_links l
		 JOIN pages f ON f.id = l.from_page_id
		 LEFT JOIN pages t ON t.id = l.to_page_id
		 WHERE l.workspace_id = ? AND f.deleted_at IS NULL AND f.min_rank <= ?
		   AND (l.to_page_id IS NULL OR t.deleted_at IS NOT NULL)
		 ORDER BY f.slug, l.to_slug`, s.WsID, s.MaxRank)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (BrokenLink, error) {
		var b BrokenLink
		return b, row.Scan(&b.FromSlug, &b.ToSlug)
	})
}

// ShadowedAlias es una ruta vieja que hoy ocupa otra página: el alias ya no se usa nunca.
type ShadowedAlias struct{ OldSlug, PageSlug string }

func (r *Repo) ShadowedAliases(ctx context.Context, s Scope) ([]ShadowedAlias, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT a.old_slug, p.slug FROM page_aliases a
		 JOIN pages live ON live.workspace_id = a.workspace_id AND live.slug = a.old_slug AND live.deleted_at IS NULL
		 JOIN pages p ON p.id = a.page_id
		 WHERE a.workspace_id = ? AND p.min_rank <= ? ORDER BY a.old_slug`, s.WsID, s.MaxRank)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (ShadowedAlias, error) {
		var a ShadowedAlias
		return a, row.Scan(&a.OldSlug, &a.PageSlug)
	})
}

// OrphanPages son las páginas vivas a las que ninguna otra página viva enlaza (§4.7). El
// enlace se busca por ruta, que tiene índice: los enlaces se reescriben al mover.
func (r *Repo) OrphanPages(ctx context.Context, s Scope) ([]string, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT p.slug FROM pages p
		 WHERE p.workspace_id = ? AND p.deleted_at IS NULL AND p.min_rank <= ?
		   AND NOT EXISTS (SELECT 1 FROM page_links l JOIN pages f ON f.id = l.from_page_id AND f.deleted_at IS NULL
		                   WHERE l.workspace_id = p.workspace_id AND l.to_slug = p.slug AND l.from_page_id <> p.id)
		 ORDER BY p.slug`, s.WsID, s.MaxRank)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (string, error) {
		var slug string
		return slug, row.Scan(&slug)
	})
}

// PagesListingChildren son las páginas que muestran el índice de sus hijas ([[children]],
// §4.10): esas hijas no están olvidadas aunque nadie las enlace, porque ese índice no se guarda
// como enlace. Recorre el contenido del área: la salud se pide a mano, no en cada lectura.
func (r *Repo) PagesListingChildren(ctx context.Context, s Scope) ([]string, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT slug FROM pages WHERE workspace_id = ? AND deleted_at IS NULL AND min_rank <= ?
		 AND content_markdown LIKE '%[[children]]%'`, s.WsID, s.MaxRank)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (string, error) {
		var slug string
		return slug, row.Scan(&slug)
	})
}

// MediaRow es lo que la salud revisa de cada archivo subido, también los de la papelera, que
// conservan su archivo hasta la purga.
type MediaRow struct {
	ID, SHA256    string
	Size          int64
	HasThumbnail  bool
	UnattachedOld bool // sin página, fuera de la papelera y más viejo que el corte
}

func (r *Repo) MediaRows(ctx context.Context, s Scope, unattachedBefore time.Time) ([]MediaRow, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT id, sha256, file_size_bytes, has_thumbnail, page_id IS NULL AND deleted_at IS NULL AND created_at < ?
		 FROM media_assets WHERE workspace_id = ? ORDER BY id`, unattachedBefore.UTC(), s.WsID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (MediaRow, error) {
		var m MediaRow
		return m, row.Scan(&m.ID, &m.SHA256, &m.Size, &m.HasThumbnail, &m.UnattachedOld)
	})
}

// PagesWithoutIndex son páginas vivas que la búsqueda no encuentra: sin fila en el índice.
func (r *Repo) PagesWithoutIndex(ctx context.Context, s Scope) ([]string, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT p.slug FROM pages p
		 WHERE p.workspace_id = ? AND p.deleted_at IS NULL AND p.min_rank <= ?
		   AND (p.search_rowid IS NULL OR NOT EXISTS (SELECT 1 FROM pages_fts f WHERE f.rowid = p.search_rowid))
		 ORDER BY p.slug`, s.WsID, s.MaxRank)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (string, error) {
		var slug string
		return slug, row.Scan(&slug)
	})
}

// OrphanIndexRows cuenta filas del índice sin página: el texto de una página purgada que sigue
// en el índice. La búsqueda no las devuelve (une con pages), pero el texto no debería estar:
// es un error de seguridad (§13.7). Es una consulta del sistema, sin Scope.
func (r *Repo) OrphanIndexRows(ctx context.Context) (int, error) {
	var n int
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT (SELECT count(*) FROM pages_fts f WHERE NOT EXISTS (SELECT 1 FROM pages p WHERE p.search_rowid = f.rowid))
		      + (SELECT count(*) FROM pages_title_trgm t WHERE NOT EXISTS (SELECT 1 FROM pages p WHERE p.search_rowid = t.rowid))`).Scan(&n)
	return n, err
}
