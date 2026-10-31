package repo

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// El espacio personal (§7) es un workspace kind = 'personal' con un solo miembro, su dueño, como
// admin. Todo lo demás (árbol, historial, media, búsqueda) es el de cualquier área: la contención
// por Scope ya lo deja fuera del alcance de los demás.

// PersonalSlug es la ruta del espacio personal de una persona. Lleva "~", que un área no puede
// tener (§3.1): no choca con ninguna y se reconoce sin consultar.
func PersonalSlug(userID string) string { return "~" + strings.ToLower(userID) }

// EnsurePersonal devuelve el espacio personal de la persona y lo crea si todavía no existe, con
// una copia de las plantillas globales como cualquier área (§4.9). Dos pedidos a la vez no crean
// dos: el índice único por dueño hace perder al segundo, que lee el del primero.
func (r *Repo) EnsurePersonal(ctx context.Context, userID string) (Workspace, error) {
	w, err := r.personalOf(ctx, userID)
	if !errors.Is(err, ErrNotFound) {
		return w, err
	}
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return Workspace{}, err
	}
	defer tx.Rollback()
	id := ulid.Make().String()
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspaces (id, slug, name, kind, owner_user_id) VALUES (?, ?, '', 'personal', ?)`,
		id, PersonalSlug(userID), userID); err != nil {
		if errors.Is(conflict(err), ErrConflict) {
			return r.personalOf(ctx, userID)
		}
		return Workspace{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_members (workspace_id, user_id, role) VALUES (?, ?, 'admin')`, id, userID); err != nil {
		return Workspace{}, err
	}
	if err := copyGlobalTemplates(ctx, tx, id); err != nil {
		return Workspace{}, err
	}
	if err := tx.Commit(); err != nil {
		return Workspace{}, err
	}
	return r.personalOf(ctx, userID)
}

func (r *Repo) personalOf(ctx context.Context, userID string) (Workspace, error) {
	return scanWorkspace(r.db.Write.QueryRowContext(ctx,
		`SELECT `+workspaceColumns+` FROM workspaces w WHERE w.kind = 'personal' AND w.owner_user_id = ?`, userID))
}

// PersonalBySlug es solo para el modo auditoría del superadmin (§7): un espacio personal ajeno,
// que no es miembro. Un área con ese slug no existe ("~" no es válido en un área).
func (r *Repo) PersonalBySlug(ctx context.Context, slug string) (Workspace, error) {
	return scanWorkspace(r.db.Read.QueryRowContext(ctx,
		`SELECT `+workspaceColumns+` FROM workspaces w WHERE w.kind = 'personal' AND w.slug = ?`, slug))
}

// PageMedia es la media que tiene la página (la que se copia al publicarla en un área, §7).
func (r *Repo) PageMedia(ctx context.Context, s Scope, pageID string) ([]Media, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT m.id, m.original_filename, m.mime_type, m.file_size_bytes, m.sha256, m.has_thumbnail, m.uploaded_by, m.created_at
		 FROM media_assets m JOIN pages p ON p.id = m.page_id
		 WHERE m.workspace_id = ? AND m.page_id = ? AND m.deleted_at IS NULL AND p.deleted_at IS NULL AND p.min_rank <= ?`,
		s.WsID, pageID, s.MaxRank)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (Media, error) {
		var m Media
		err := row.Scan(&m.ID, &m.Filename, &m.MimeType, &m.Size, &m.SHA256, &m.HasThumbnail, &m.UploadedBy, &m.CreatedAt)
		return m, err
	})
}

// PublishMove es una página del espacio personal que pasa a un área (§7, aprobación directa).
// Media son las copias ya guardadas en el disco del área; Replace, las URLs de la media vieja a
// la nueva, que se cambian en el contenido y en todo el historial; Markdown, Links e Index son el
// contenido actual con las URLs ya cambiadas.
type PublishMove struct {
	PageID, Slug, ActorID string
	Media                 []Media
	Replace               map[string]string
	Markdown              string
	Links                 []string
	Index                 IndexDoc
}

// PublishPage mueve la página, con todo su historial, del espacio personal (from) al área (to),
// en una transacción. La ruta destino sigue las reglas de crear una página (§3.2, §19.35): un padre
// que quien publica no ve da ErrHidden y una ruta ocupada, ErrConflict. El nivel se queda con el
// de la página, salvo que quien publica no lo alcance (baja al suyo) o el padre pida más.
// Las subpáginas no viajan: siguen en el espacio personal.
func (r *Repo) PublishPage(ctx context.Context, from, to Scope, m PublishMove) error {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var oldSlug, title string
	var rank, rev int
	err = tx.QueryRowContext(ctx, `SELECT slug, title, min_rank, current_revision FROM pages
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL AND min_rank <= ?`, m.PageID, from.WsID, from.MaxRank).
		Scan(&oldSlug, &title, &rank, &rev)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	anc, err := maxAncestorRank(ctx, tx, to, m.Slug)
	if err != nil {
		return err
	}
	if anc > to.MaxRank {
		return ErrHidden
	}
	var taken bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pages WHERE workspace_id = ? AND slug = ? AND deleted_at IS NULL)`,
		to.WsID, m.Slug).Scan(&taken); err != nil {
		return err
	}
	if taken {
		return ErrConflict
	}

	// La media original queda en el espacio personal, sin página: la purga de huérfanas se la lleva.
	if _, err := tx.ExecContext(ctx, `UPDATE media_assets SET page_id = NULL WHERE workspace_id = ? AND page_id = ?`, from.WsID, m.PageID); err != nil {
		return err
	}
	for _, md := range m.Media {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO media_assets (id, workspace_id, page_id, original_filename, mime_type, file_size_bytes, sha256, has_thumbnail, uploaded_by)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			md.ID, to.WsID, m.PageID, md.Filename, md.MimeType, md.Size, md.SHA256, md.HasThumbnail, md.UploadedBy); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE pages SET workspace_id = ?, slug = ?, min_rank = ?, content_markdown = ?, current_revision = current_revision + 1,
		 updated_by = ?, updated_at = CURRENT_TIMESTAMP, review_notified_at = NULL WHERE id = ?`,
		to.WsID, m.Slug, max(1, anc, min(rank, to.MaxRank)), m.Markdown, m.ActorID, m.PageID); err != nil {
		return err
	}
	// El historial viaja con la página; sus URLs de media se cambian para que las revisiones viejas
	// no apunten al espacio personal, que en el área nadie más puede abrir.
	for old, cur := range m.Replace {
		for _, q := range []string{
			`UPDATE page_revisions SET content_markdown = REPLACE(content_markdown, ?, ?) WHERE page_id = ?`,
			`UPDATE page_drafts SET content_markdown = REPLACE(content_markdown, ?, ?) WHERE page_id = ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, old, cur, m.PageID); err != nil {
				return err
			}
		}
	}
	for _, q := range []string{
		`UPDATE page_revisions SET workspace_id = ? WHERE page_id = ?`,
		`UPDATE page_tags SET workspace_id = ? WHERE page_id = ?`,
		`UPDATE page_views SET workspace_id = ? WHERE page_id = ?`,
		`UPDATE page_drafts SET workspace_id = ? WHERE page_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, to.WsID, m.PageID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE page_drafts SET target_slug = ? WHERE page_id = ?`, m.Slug, m.PageID); err != nil {
		return err
	}
	// Lo que en el espacio personal apuntaba a la página (alias, enlaces, avisos) se queda allá.
	for _, q := range []string{
		`DELETE FROM page_aliases WHERE page_id = ?`,
		`UPDATE page_links SET to_page_id = NULL WHERE to_page_id = ? AND workspace_id = ?`,
		`DELETE FROM notifications WHERE page_id = ? AND workspace_id = ?`,
		`DELETE FROM notification_events WHERE page_id = ? AND workspace_id = ?`,
	} {
		args := []any{m.PageID}
		if strings.Count(q, "?") == 2 {
			args = append(args, from.WsID)
		}
		if _, err := tx.ExecContext(ctx, q, args...); err != nil {
			return err
		}
	}
	if err := insertRevision(ctx, tx, to, m.PageID, rev+1, PageWrite{
		Title: title, Markdown: m.Markdown, Kind: "moved", Summary: "~/" + oldSlug + " → " + m.Slug, AuthorID: m.ActorID,
	}); err != nil {
		return err
	}
	if err := setLinks(ctx, tx, to, m.PageID, m.Links); err != nil {
		return err
	}
	if err := resolveLinksTo(ctx, tx, to, m.Slug, m.PageID); err != nil {
		return err
	}
	if err := writeIndex(ctx, tx, m.PageID, m.Index); err != nil {
		return err
	}
	if err := emit(ctx, tx, to, EventPageSaved, m.ActorID, m.PageID, ""); err != nil {
		return err
	}
	return tx.Commit()
}

// SentProposal es una propuesta de página nueva que salió de un borrador personal (§7), vista
// desde el borrador: en qué área está y en qué quedó.
type SentProposal struct {
	ID, AreaSlug, AreaName, Slug, Status, Comment string
	CreatedAt                                     time.Time
}

// SentProposals lista lo que se mandó desde la página del espacio personal, solo a las áreas donde
// su autor sigue siendo miembro: de las demás ya no le corresponde saber nada.
func (r *Repo) SentProposals(ctx context.Context, authorID, pageID string) ([]SentProposal, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT pr.id, w.slug, w.name, COALESCE(p.slug, pr.target_slug), pr.status, COALESCE(pr.review_comment, ''), pr.created_at
		 FROM proposals pr
		 JOIN workspaces w ON w.id = pr.workspace_id
		 JOIN workspace_members m ON m.workspace_id = pr.workspace_id AND m.user_id = pr.author_id
		 LEFT JOIN pages p ON p.id = pr.page_id AND p.deleted_at IS NULL
		 WHERE pr.source_personal_page_id = ? AND pr.author_id = ?
		 ORDER BY pr.created_at DESC`, pageID, authorID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (SentProposal, error) {
		var p SentProposal
		err := row.Scan(&p.ID, &p.AreaSlug, &p.AreaName, &p.Slug, &p.Status, &p.Comment, &p.CreatedAt)
		return p, err
	})
}
