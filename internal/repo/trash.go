package repo

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// TrashItem es una unidad de la papelera: la página que se borró y las subpáginas que se fueron
// con ella (SPEC §4.5).
type TrashItem struct {
	ID        string // id de la página que se borró; identifica la unidad
	Slug      string
	Title     string
	MinRank   int
	Pages     int // páginas de la unidad, incluida ella
	DeletedBy string
	DeletedAt time.Time
}

// MediaFile es un archivo de media que la purga borró de la BD y falta borrar del disco.
type MediaFile struct{ WsID, ID string }

// PurgedPage es una página que la purga borró para siempre, para registrarla en la auditoría.
type PurgedPage struct{ WsID, ID string }

// trashAccess es la regla de §4.5 y §19.38: el admin del área ve y restaura toda la papelera;
// un editor, solo lo que él borró. Va con (MaxRank, actorID).
const trashAccess = `(? >= 3 OR p.deleted_by = ?)`

// DeletePages manda a la papelera la página y las subpáginas que quien borra ve, como una
// unidad, con una revisión "deleted" cada una. Las que no ve se quedan donde están (§19.37,
// mismo criterio que mover en §19.36): nadie altera lo que no ve. Devuelve el id de la unidad
// y cuántas páginas tiene.
func (r *Repo) DeletePages(ctx context.Context, s Scope, actorID, slug string) (string, int, error) {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx,
		`SELECT id, slug, title, description, content_markdown, lang, min_rank, current_revision FROM pages
		 WHERE workspace_id = ? AND deleted_at IS NULL AND min_rank <= ? AND (slug = ? OR slug LIKE ?)`,
		s.WsID, s.MaxRank, slug, slug+"/%")
	if err != nil {
		return "", 0, err
	}
	pages, err := collect(rows, scanMoved)
	if err != nil {
		return "", 0, err
	}
	var rootID string
	ids := make([]any, 0, len(pages)+2)
	for _, p := range pages {
		if p.slug == slug {
			rootID = p.id
		}
		ids = append(ids, p.id)
	}
	if rootID == "" {
		return "", 0, ErrNotFound
	}
	// Una sola sentencia: toda la unidad queda con la misma hora y se purga el mismo día.
	if _, err := tx.ExecContext(ctx,
		`UPDATE pages SET deleted_at = CURRENT_TIMESTAMP, deleted_by = ?, trash_root_id = ?,
		 current_revision = current_revision + 1, updated_by = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id IN (?`+strings.Repeat(",?", len(ids)-1)+`)`, append([]any{actorID, rootID, actorID}, ids...)...); err != nil {
		return "", 0, err
	}
	for _, p := range pages {
		if err := insertRevision(ctx, tx, s, p.id, p.revision+1, PageWrite{
			Title: p.title, Markdown: p.markdown, Kind: "deleted", AuthorID: actorID,
		}); err != nil {
			return "", 0, err
		}
	}
	return rootID, len(pages), tx.Commit()
}

// Trash lista las unidades de la papelera que quien pregunta puede ver y restaurar.
func (r *Repo) Trash(ctx context.Context, s Scope, actorID string) ([]TrashItem, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT p.id, p.slug, p.title, p.min_rank, u.full_name, p.deleted_at,
		   (SELECT COUNT(*) FROM pages c WHERE c.trash_root_id = p.id AND c.min_rank <= ?)
		 FROM pages p JOIN users u ON u.id = p.deleted_by
		 WHERE p.workspace_id = ? AND p.deleted_at IS NOT NULL AND p.trash_root_id = p.id AND p.min_rank <= ? AND `+trashAccess+`
		 ORDER BY p.deleted_at DESC, p.slug`, s.MaxRank, s.WsID, s.MaxRank, s.MaxRank, actorID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (TrashItem, error) {
		var t TrashItem
		return t, row.Scan(&t.ID, &t.Slug, &t.Title, &t.MinRank, &t.DeletedBy, &t.DeletedAt, &t.Pages)
	})
}

// RestorePages devuelve una unidad de la papelera a sus rutas, con una revisión "restored" por
// página. Si otra página ocupó una de esas rutas da ErrConflict; si la unidad quedó bajo un
// padre que quien restaura no ve, ErrHidden: quien llama responde lo mismo en los dos casos
// (§19.35). Cada página toma al menos el nivel de sus padres actuales (§3.2).
func (r *Repo) RestorePages(ctx context.Context, s Scope, actorID, id string) (int, error) {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var rootSlug string
	err = tx.QueryRowContext(ctx,
		`SELECT p.slug FROM pages p WHERE p.id = ? AND p.workspace_id = ? AND p.deleted_at IS NOT NULL
		 AND p.trash_root_id = p.id AND p.min_rank <= ? AND `+trashAccess, id, s.WsID, s.MaxRank, s.MaxRank, actorID).Scan(&rootSlug)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	anc, err := maxAncestorRank(ctx, tx, s, rootSlug)
	if err != nil {
		return 0, err
	}
	if anc > s.MaxRank {
		return 0, ErrHidden
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT id, slug, title, description, content_markdown, lang, min_rank, current_revision FROM pages
		 WHERE workspace_id = ? AND trash_root_id = ? AND deleted_at IS NOT NULL AND min_rank <= ?`, s.WsID, id, s.MaxRank)
	if err != nil {
		return 0, err
	}
	pages, err := collect(rows, scanMoved)
	if err != nil {
		return 0, err
	}
	slugs := []any{s.WsID}
	for _, p := range pages {
		slugs = append(slugs, p.slug)
	}
	var taken int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pages WHERE workspace_id = ? AND deleted_at IS NULL AND slug IN (?`+
		strings.Repeat(",?", len(pages)-1)+`)`, slugs...).Scan(&taken); err != nil {
		return 0, err
	}
	if taken > 0 {
		return 0, ErrConflict
	}
	for _, p := range pages {
		if _, err := tx.ExecContext(ctx,
			`UPDATE pages SET deleted_at = NULL, deleted_by = NULL, trash_root_id = NULL, min_rank = ?,
			 current_revision = current_revision + 1, updated_by = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
			max(p.rank, anc), actorID, p.id); err != nil {
			return 0, err
		}
		if err := insertRevision(ctx, tx, s, p.id, p.revision+1, PageWrite{
			Title: p.title, Markdown: p.markdown, Kind: "restored", AuthorID: actorID,
		}); err != nil {
			return 0, err
		}
		// Los enlaces a la ruta vuelven a apuntar a la página, aunque mientras tanto otra la haya ocupado.
		if err := resolveLinksTo(ctx, tx, s, p.slug, p.id); err != nil {
			return 0, err
		}
	}
	return len(pages), tx.Commit()
}

// sqlTime escribe la hora con el formato de CURRENT_TIMESTAMP, para compararla como texto con
// las columnas que SQLite llenó solo.
func sqlTime(t time.Time) string { return t.UTC().Format(time.DateTime) }

// Lo que sigue lo usa el job diario de purga (SPEC §4.5): son operaciones del sistema sobre
// todas las áreas, sin Scope, como RebuildIndex. No devuelven contenido.

// TrashOrphanMedia manda a la papelera la media que nadie usó desde antes de cutoff (§4.4).
func (r *Repo) TrashOrphanMedia(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := r.db.Write.ExecContext(ctx,
		`UPDATE media_assets SET deleted_at = CURRENT_TIMESTAMP WHERE page_id IS NULL AND deleted_at IS NULL AND created_at <= ?`,
		sqlTime(cutoff))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PurgeTrash borra para siempre lo que está en la papelera desde antes de cutoff: las páginas
// con sus revisiones, etiquetas, enlaces y alias (ON DELETE CASCADE), su fila en la búsqueda,
// su media y la media huérfana vencida. Devuelve los archivos que hay que borrar del disco:
// se borran después de confirmar, para que nunca quede una fila apuntando a un archivo que no está.
func (r *Repo) PurgeTrash(ctx context.Context, cutoff time.Time) ([]PurgedPage, []MediaFile, error) {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	at := sqlTime(cutoff)
	const expiredMedia = `FROM media_assets WHERE deleted_at <= ?1 OR page_id IN (SELECT id FROM pages WHERE deleted_at <= ?1)`
	rows, err := tx.QueryContext(ctx, `SELECT workspace_id, id `+expiredMedia, at)
	if err != nil {
		return nil, nil, err
	}
	files, err := collect(rows, func(row scanner) (MediaFile, error) {
		var f MediaFile
		err := row.Scan(&f.WsID, &f.ID)
		return f, err
	})
	if err != nil {
		return nil, nil, err
	}
	for _, q := range []string{
		`DELETE ` + expiredMedia,
		`DELETE FROM pages_fts WHERE rowid IN (SELECT search_rowid FROM pages WHERE deleted_at <= ?1)`,
		`DELETE FROM pages_title_trgm WHERE rowid IN (SELECT search_rowid FROM pages WHERE deleted_at <= ?1)`,
	} {
		if _, err := tx.ExecContext(ctx, q, at); err != nil {
			return nil, nil, err
		}
	}
	rows, err = tx.QueryContext(ctx, `DELETE FROM pages WHERE deleted_at <= ? RETURNING workspace_id, id`, at)
	if err != nil {
		return nil, nil, err
	}
	pages, err := collect(rows, func(row scanner) (PurgedPage, error) {
		var p PurgedPage
		err := row.Scan(&p.WsID, &p.ID)
		return p, err
	})
	if err != nil {
		return nil, nil, err
	}
	return pages, files, tx.Commit()
}

// PurgeSearchMisses borra las búsquedas sin resultados de antes de cutoff (§9.6: 90 días).
func (r *Repo) PurgeSearchMisses(ctx context.Context, cutoff time.Time) error {
	_, err := r.db.Write.ExecContext(ctx, `DELETE FROM search_misses WHERE day < ?`, cutoff.UTC().Format(time.DateOnly))
	return err
}
