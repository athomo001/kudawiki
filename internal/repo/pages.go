package repo

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/oklog/ulid/v2"
)

// Toda consulta de este archivo filtra por s.WsID y s.MaxRank: una página de otra área o de
// un nivel superior no existe para quien pregunta (SPEC §3.2, §3.3). Una página en la papelera
// tampoco existe fuera de la papelera (§4.5).

type Page struct {
	ID        string
	Slug      string
	MinRank   int
	Title     string
	Markdown  string
	Lang      string
	Revision  int
	UpdatedBy string // nombre de quien hizo la última revisión
	UpdatedAt time.Time
	// Metadatos (§4.1): como las etiquetas, cambiarlos no crea revisión.
	Description string
	Icon        string
}

type PageRef struct {
	Slug        string
	Title       string
	MinRank     int
	Description string
	Icon        string
}

type Revision struct {
	Number    int
	Kind      string
	Title     string
	Markdown  string
	Author    string
	Approver  string // quien aprobó la propuesta de la que salió; vacío si se publicó directo (§5.2)
	Summary   string
	CreatedAt time.Time
}

// PageWrite es lo que se guarda en una revisión nueva.
type PageWrite struct {
	Slug     string
	Title    string
	Markdown string
	Kind     string
	Summary  string
	AuthorID string
	MediaIDs []string // media que el contenido usa; se asocia a la página en la misma transacción
	Links    []string // rutas de los [[enlaces]] del contenido (SPEC §4.7)
	Lang     string
	Index    IndexDoc // lo que se indexa para la búsqueda, en la misma transacción (SPEC §9.1)
	// Merge va cuando la revisión sale de una propuesta aprobada: la propuesta queda aprobada en
	// la misma transacción, o no se publica nada (§5.2).
	Merge *ProposalMerge
}

// ProposalMerge es la aprobación que publica una propuesta. AuthorID de la PageWrite es el autor
// de la propuesta; ReviewerID, quien la aprueba.
type ProposalMerge struct {
	ID, ReviewerID, Comment string
}

func (r *Repo) PageBySlug(ctx context.Context, s Scope, slug string) (Page, error) {
	var p Page
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT p.id, p.slug, p.min_rank, p.title, p.content_markdown, p.lang, p.current_revision, u.full_name, p.updated_at, p.description, p.icon
		 FROM pages p JOIN users u ON u.id = p.updated_by
		 WHERE p.workspace_id = ? AND p.slug = ? AND p.deleted_at IS NULL AND p.min_rank <= ?`, s.WsID, slug, s.MaxRank).
		Scan(&p.ID, &p.Slug, &p.MinRank, &p.Title, &p.Markdown, &p.Lang, &p.Revision, &p.UpdatedBy, &p.UpdatedAt, &p.Description, &p.Icon)
	if errors.Is(err, sql.ErrNoRows) {
		return Page{}, ErrNotFound
	}
	return p, err
}

// Tree devuelve solo lo que el nivel deja ver: las páginas superiores no aparecen ni como
// carpeta (SPEC §17, fugas por metadatos).
func (r *Repo) Tree(ctx context.Context, s Scope) ([]PageRef, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT slug, title, min_rank, description, icon FROM pages WHERE workspace_id = ? AND deleted_at IS NULL AND min_rank <= ? ORDER BY slug`, s.WsID, s.MaxRank)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanPageRef)
}

func scanPageRef(row scanner) (PageRef, error) {
	var p PageRef
	return p, row.Scan(&p.Slug, &p.Title, &p.MinRank, &p.Description, &p.Icon)
}

// SetPageInfo cambia la descripción y el ícono (§4.1) y, en la misma transacción, la
// descripción en el índice de búsqueda, que pesa más que el contenido (§9.1).
func (r *Repo) SetPageInfo(ctx context.Context, s Scope, pageID, description, descriptionStem, icon string) error {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE pages SET description = ?, icon = ? WHERE id = ? AND workspace_id = ?`,
		description, icon, pageID, s.WsID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE pages_fts SET description = ?, description_stem = ? WHERE rowid = (SELECT search_rowid FROM pages WHERE id = ?)`,
		description, descriptionStem, pageID); err != nil {
		return err
	}
	return tx.Commit()
}

// CreatePage crea la página con su revisión 1. Un slug ya usado da ErrConflict, también si
// la página existente es de un nivel que quien crea no ve: quien llama responde lo mismo en
// los dos casos (§19.35). Devuelve el id de la página nueva.
func (r *Repo) CreatePage(ctx context.Context, s Scope, w PageWrite) (string, error) {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	// La página nace con el nivel de su padre más alto (§3.2). Si ese padre es de un nivel que
	// quien crea no ve, la ruta no está disponible, igual que si estuviera ocupada (§19.35).
	anc, err := maxAncestorRank(ctx, tx, s, w.Slug)
	if err != nil {
		return "", err
	}
	if anc > s.MaxRank {
		return "", ErrHidden
	}
	id := ulid.Make().String()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO pages (id, workspace_id, slug, min_rank, title, content_markdown, lang, created_by, updated_by)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, s.WsID, w.Slug, max(1, anc), w.Title, w.Markdown, w.Lang, w.AuthorID, w.AuthorID); err != nil {
		return "", conflict(err)
	}
	if err := insertRevision(ctx, tx, s, id, 1, w); err != nil {
		return "", err
	}
	if err := mergeProposal(ctx, tx, s, id, 1, w.Merge); err != nil {
		return "", err
	}
	if err := emit(ctx, tx, s, EventPageSaved, w.AuthorID, id, mergeID(w.Merge)); err != nil {
		return "", err
	}
	if err := attachMedia(ctx, tx, s, id, w.MediaIDs); err != nil {
		return "", err
	}
	if err := setLinks(ctx, tx, s, id, w.Links); err != nil {
		return "", err
	}
	if err := resolveLinksTo(ctx, tx, s, w.Slug, id); err != nil {
		return "", err
	}
	if err := writeIndex(ctx, tx, id, w.Index); err != nil {
		return "", err
	}
	return id, tx.Commit()
}

// UpdatePage aplica el bloqueo optimista de SPEC §4.1: si la revisión actual ya no es base,
// otra persona guardó antes y se devuelve ErrConflict sin tocar nada.
func (r *Repo) UpdatePage(ctx context.Context, s Scope, pageID string, base int, w PageWrite) error {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`UPDATE pages SET title = ?, content_markdown = ?, lang = ?, current_revision = current_revision + 1,
		 updated_by = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL AND min_rank <= ? AND current_revision = ?`,
		w.Title, w.Markdown, w.Lang, w.AuthorID, pageID, s.WsID, s.MaxRank, base)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrConflict
	}
	if err := insertRevision(ctx, tx, s, pageID, base+1, w); err != nil {
		return err
	}
	if err := mergeProposal(ctx, tx, s, pageID, base+1, w.Merge); err != nil {
		return err
	}
	if err := emit(ctx, tx, s, EventPageSaved, w.AuthorID, pageID, mergeID(w.Merge)); err != nil {
		return err
	}
	if err := attachMedia(ctx, tx, s, pageID, w.MediaIDs); err != nil {
		return err
	}
	if err := setLinks(ctx, tx, s, pageID, w.Links); err != nil {
		return err
	}
	if err := writeIndex(ctx, tx, pageID, w.Index); err != nil {
		return err
	}
	return tx.Commit()
}

func insertRevision(ctx context.Context, tx *sql.Tx, s Scope, pageID string, n int, w PageWrite) error {
	var approver, proposal sql.NullString
	if w.Merge != nil {
		approver, proposal = sql.NullString{String: w.Merge.ReviewerID, Valid: true}, sql.NullString{String: w.Merge.ID, Valid: true}
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO page_revisions (id, page_id, workspace_id, revision_number, kind, title, content_markdown, created_by, change_summary, approved_by, proposal_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?)`,
		ulid.Make().String(), pageID, s.WsID, n, w.Kind, w.Title, w.Markdown, w.AuthorID, w.Summary, approver, proposal)
	return err
}

const revisionColumns = `r.revision_number, r.kind, r.title, r.content_markdown, u.full_name, COALESCE(a.full_name, ''), COALESCE(r.change_summary, ''), r.created_at`

// revisionScope une con pages para que el nivel de la página, y la papelera, también filtren su historial.
const revisionScope = `FROM page_revisions r
	JOIN pages p ON p.id = r.page_id
	JOIN users u ON u.id = r.created_by
	LEFT JOIN users a ON a.id = r.approved_by
	WHERE r.page_id = ? AND r.workspace_id = ? AND p.deleted_at IS NULL AND p.min_rank <= ?`

func scanRevision(row scanner) (Revision, error) {
	var v Revision
	err := row.Scan(&v.Number, &v.Kind, &v.Title, &v.Markdown, &v.Author, &v.Approver, &v.Summary, &v.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Revision{}, ErrNotFound
	}
	return v, err
}

func (r *Repo) Revisions(ctx context.Context, s Scope, pageID string) ([]Revision, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT `+revisionColumns+` `+revisionScope+` ORDER BY r.revision_number DESC`, pageID, s.WsID, s.MaxRank)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanRevision)
}

func (r *Repo) RevisionByNumber(ctx context.Context, s Scope, pageID string, n int) (Revision, error) {
	return scanRevision(r.db.Read.QueryRowContext(ctx,
		`SELECT `+revisionColumns+` `+revisionScope+` AND r.revision_number = ?`, pageID, s.WsID, s.MaxRank, n))
}
