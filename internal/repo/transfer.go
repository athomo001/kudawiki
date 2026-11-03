package repo

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Traslado de páginas entre áreas (SPEC §4.13): solo el superadmin, que no es miembro de las
// áreas; por eso las dos van con Scope de admin y las rutas lo exigen aparte.

// Subtree es la página de esa ruta y todas sus subpáginas vigentes, con su contenido, de padres a
// hijas. Las de la papelera no viajan: se quedan para la purga del área de origen.
func (r *Repo) Subtree(ctx context.Context, s Scope, root string) ([]Page, error) {
	rows, err := r.db.Read.QueryContext(ctx, `SELECT id, slug, min_rank, title, content_markdown, lang, current_revision, description
		FROM pages WHERE workspace_id = ? AND deleted_at IS NULL AND min_rank <= ? AND (slug = ? OR slug LIKE ? ESCAPE '\')
		ORDER BY slug`, s.WsID, s.MaxRank, root, likePrefix(root))
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (Page, error) {
		var p Page
		return p, row.Scan(&p.ID, &p.Slug, &p.MinRank, &p.Title, &p.Markdown, &p.Lang, &p.Revision, &p.Description)
	})
}

// likePrefix es "ruta/%" con los comodines de LIKE escapados: una ruta con "_" no arrastra otras.
func likePrefix(slug string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(slug) + "/%"
}

// SealedRef es un sellado del subárbol que se traslada: su ruta y su autor, a quien se avisa.
type SealedInTree struct{ ID, Slug, AuthorID string }

// SealedSubtree son los sellados que viven bajo esa ruta: viajan con ella, sin descifrarse.
func (r *Repo) SealedSubtree(ctx context.Context, s Scope, root string) ([]SealedInTree, error) {
	rows, err := r.db.Read.QueryContext(ctx, `SELECT id, slug, author_id FROM sealed_pages
		WHERE workspace_id = ? AND (slug = ? OR slug LIKE ? ESCAPE '\') ORDER BY slug`, s.WsID, root, likePrefix(root))
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (SealedInTree, error) {
		var x SealedInTree
		return x, row.Scan(&x.ID, &x.Slug, &x.AuthorID)
	})
}

// TransferImpact es lo que muestra la vista previa (§4.13) además de las rutas y los niveles.
type TransferImpact struct {
	Taken       []string     // rutas del destino ya ocupadas (por una página, viva o en la papelera, o un sellado)
	BrokenLinks []BrokenLink // enlaces de páginas del origen que quedan rotos
	Proposals   int          // propuestas pendientes que se cierran como retiradas
	Shares      int          // vínculos de compartir que terminan
	Reads       int          // solicitudes de lectura abiertas que se cierran
}

// TransferCheck arma el impacto de llevar esas páginas (ids) y sellados a esas rutas del destino.
func (r *Repo) TransferCheck(ctx context.Context, from, to Scope, ids, newSlugs []string) (TransferImpact, error) {
	var out TransferImpact
	for _, slug := range newSlugs {
		var taken bool
		if err := r.db.Read.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pages WHERE workspace_id = ?1 AND slug = ?2)
			OR EXISTS (SELECT 1 FROM sealed_pages WHERE workspace_id = ?1 AND slug = ?2)`, to.WsID, slug).Scan(&taken); err != nil {
			return out, err
		}
		if taken {
			out.Taken = append(out.Taken, slug)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	in, args := inList(ids)
	rows, err := r.db.Read.QueryContext(ctx, `SELECT f.slug, l.to_slug FROM page_links l JOIN pages f ON f.id = l.from_page_id
		WHERE l.workspace_id = ? AND f.deleted_at IS NULL AND l.to_page_id IN `+in+` AND l.from_page_id NOT IN `+in+`
		ORDER BY f.slug, l.to_slug`, append(append([]any{from.WsID}, args...), args...)...)
	if err != nil {
		return out, err
	}
	if out.BrokenLinks, err = collect(rows, func(row scanner) (BrokenLink, error) {
		var b BrokenLink
		return b, row.Scan(&b.FromSlug, &b.ToSlug)
	}); err != nil {
		return out, err
	}
	for _, c := range []struct {
		sql string
		n   *int
	}{
		{`SELECT COUNT(*) FROM proposals WHERE status = 'pending' AND page_id IN ` + in, &out.Proposals},
		{`SELECT COUNT(*) FROM page_shares WHERE status IN ('pending','active') AND page_id IN ` + in, &out.Shares},
		{`SELECT COUNT(*) FROM read_requests WHERE closed_at IS NULL AND page_id IN ` + in, &out.Reads},
	} {
		if err := r.db.Read.QueryRowContext(ctx, c.sql, args...).Scan(c.n); err != nil {
			return out, err
		}
	}
	return out, nil
}

func inList(ids []string) (string, []any) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return `(?` + strings.Repeat(",?", len(ids)-1) + `)`, args
}

// TransferredPage es una página como llega al destino: su ruta y nivel nuevos, y su contenido con
// los enlaces a media ya apuntando al área nueva, con lo que de él sale (enlaces e índice).
type TransferredPage struct {
	ID, Slug, NewSlug string
	Revision          int
	NewLevel          int
	Markdown          string
	Links             []string
	Index             IndexDoc
}

type TransferMove struct {
	From, To Scope
	ActorID  string
	Pages    []TransferredPage
	Sealed   []SealedInTree // con Slug ya en la ruta nueva
	// Replace: los prefijos de las URL de media del origen (su slug y sus alias) y el del destino.
	Replace map[string]string
	At      time.Time
}

// WithdrawnProposal es una propuesta pendiente que el traslado cerró: a su autor se le avisa.
type WithdrawnProposal struct{ ID, AuthorID, Title string }

type TransferResult struct {
	Media     []string // ids a mover en el disco, también los .enc de los sellados
	Withdrawn []WithdrawnProposal
	Ended     []EndedShare
}

// ErrChanged: entre la vista previa y el traslado alguien cambió una de las páginas.
var ErrChanged = errors.New("transfer_changed")

// TransferPages lleva las páginas y sellados de un área a otra, en una sola transacción. Viajan
// el historial, la media, las etiquetas, los borradores, las visitas, las solicitudes de lectura
// (las abiertas se cierran: su audiencia era el origen) y los cuestionarios. Las propuestas
// pendientes se retiran, los vínculos de compartir terminan y en el origen no queda alias ni
// enlace que apunte a ellas (§4.13: no revelar a dónde se fueron).
func (r *Repo) TransferPages(ctx context.Context, m TransferMove) (TransferResult, error) {
	var out TransferResult
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	ids := make([]string, len(m.Pages))
	for i, p := range m.Pages {
		ids[i] = p.ID
		var ok bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pages WHERE id = ? AND workspace_id = ? AND slug = ?
			AND current_revision = ? AND deleted_at IS NULL)`, p.ID, m.From.WsID, p.Slug, p.Revision).Scan(&ok); err != nil {
			return out, err
		}
		if !ok {
			return out, ErrChanged
		}
	}
	in, args := inList(ids)
	withIDs := func(first ...any) []any { return append(first, args...) }

	// Lo que vuelve: la media a mover y las propuestas retiradas, antes de tocarlas.
	rows, err := tx.QueryContext(ctx, `SELECT id FROM media_assets WHERE workspace_id = ? AND page_id IN `+in, withIDs(m.From.WsID)...)
	if err != nil {
		return out, err
	}
	if out.Media, err = collect(rows, func(row scanner) (string, error) {
		var id string
		return id, row.Scan(&id)
	}); err != nil {
		return out, err
	}
	if len(m.Sealed) > 0 {
		sin, sargs := inList(sealedIDs(m.Sealed))
		rows, err := tx.QueryContext(ctx, `SELECT id FROM sealed_media WHERE page_id IN `+sin, sargs...)
		if err != nil {
			return out, err
		}
		enc, err := collect(rows, func(row scanner) (string, error) {
			var id string
			return id, row.Scan(&id)
		})
		if err != nil {
			return out, err
		}
		out.Media = append(out.Media, enc...)
	}
	if rows, err = tx.QueryContext(ctx, `UPDATE proposals SET status = 'withdrawn', updated_at = CURRENT_TIMESTAMP
		WHERE status = 'pending' AND page_id IN `+in+` RETURNING id, author_id, title`, args...); err != nil {
		return out, err
	}
	if out.Withdrawn, err = collect(rows, func(row scanner) (WithdrawnProposal, error) {
		var w WithdrawnProposal
		return w, row.Scan(&w.ID, &w.AuthorID, &w.Title)
	}); err != nil {
		return out, err
	}
	if rows, err = tx.QueryContext(ctx, `UPDATE page_shares SET status = 'ended', end_reason = 'transferred', ended_by = NULLIF(?, ''),
		ended_at = CURRENT_TIMESTAMP WHERE status IN ('pending','active') AND page_id IN `+in+`
		RETURNING id, page_id, target_workspace_id, end_reason`, withIDs(m.ActorID)...); err != nil {
		return out, err
	}
	if out.Ended, err = collect(rows, func(row scanner) (EndedShare, error) {
		var e EndedShare
		return e, row.Scan(&e.ID, &e.PageID, &e.TargetID, &e.Reason)
	}); err != nil {
		return out, err
	}

	// Lo que en el origen apuntaba a ellas se queda allá, sin decir a dónde se fueron.
	for _, q := range []string{
		`DELETE FROM page_aliases WHERE page_id IN ` + in,
		`DELETE FROM page_links WHERE from_page_id IN ` + in,
		`DELETE FROM notifications WHERE page_id IN ` + in,
		`DELETE FROM notification_events WHERE page_id IN ` + in,
	} {
		if _, err := tx.ExecContext(ctx, q, args...); err != nil {
			return out, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE page_links SET to_page_id = NULL WHERE workspace_id = ? AND to_page_id IN `+in, withIDs(m.From.WsID)...); err != nil {
		return out, err
	}
	// Quien sigue la página y no es del destino deja de seguirla: no vería los avisos.
	if _, err := tx.ExecContext(ctx, `DELETE FROM page_watchers WHERE page_id IN `+in+`
		AND user_id NOT IN (SELECT user_id FROM workspace_members WHERE workspace_id = ?)`, append(args, m.To.WsID)...); err != nil {
		return out, err
	}

	for _, p := range m.Pages {
		if _, err := tx.ExecContext(ctx, `UPDATE pages SET workspace_id = ?, slug = ?, min_rank = ?, content_markdown = ?, review_notified_at = NULL
			WHERE id = ?`, m.To.WsID, p.NewSlug, p.NewLevel, p.Markdown, p.ID); err != nil {
			return out, conflict(err)
		}
	}
	for old, cur := range m.Replace {
		for _, q := range []string{
			`UPDATE page_revisions SET content_markdown = REPLACE(content_markdown, ?, ?) WHERE page_id IN `,
			`UPDATE page_drafts SET content_markdown = REPLACE(content_markdown, ?, ?) WHERE page_id IN `,
			`UPDATE proposals SET content_markdown = REPLACE(content_markdown, ?, ?) WHERE page_id IN `,
		} {
			if _, err := tx.ExecContext(ctx, q+in, withIDs(old, cur)...); err != nil {
				return out, err
			}
		}
	}
	for _, q := range []string{
		`UPDATE page_revisions SET workspace_id = ? WHERE page_id IN `,
		`UPDATE page_tags SET workspace_id = ? WHERE page_id IN `,
		`UPDATE page_views SET workspace_id = ? WHERE page_id IN `,
		`UPDATE page_drafts SET workspace_id = ? WHERE page_id IN `,
		`UPDATE proposals SET workspace_id = ? WHERE page_id IN `,
		`UPDATE quizzes SET workspace_id = ? WHERE page_id IN `,
		`UPDATE read_requests SET workspace_id = ?, closed_at = COALESCE(closed_at, CURRENT_TIMESTAMP) WHERE page_id IN `,
		`UPDATE media_assets SET workspace_id = ? WHERE page_id IN `,
	} {
		if _, err := tx.ExecContext(ctx, q+in, withIDs(m.To.WsID)...); err != nil {
			return out, err
		}
	}
	for _, p := range m.Pages {
		if _, err := tx.ExecContext(ctx, `UPDATE page_drafts SET target_slug = ? WHERE page_id = ?`, p.NewSlug, p.ID); err != nil {
			return out, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE proposals SET target_slug = ? WHERE page_id = ?`, p.NewSlug, p.ID); err != nil {
			return out, err
		}
	}
	for _, sd := range m.Sealed {
		var taken bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pages WHERE workspace_id = ?1 AND slug = ?2 AND deleted_at IS NULL)
			OR EXISTS (SELECT 1 FROM sealed_pages WHERE workspace_id = ?1 AND slug = ?2)`, m.To.WsID, sd.Slug).Scan(&taken); err != nil {
			return out, err
		}
		if taken {
			return out, ErrConflict
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sealed_pages SET workspace_id = ?, slug = ? WHERE id = ? AND workspace_id = ?`,
			m.To.WsID, sd.Slug, sd.ID, m.From.WsID); err != nil {
			return out, err
		}
	}
	// Los enlaces se arman cuando ya están todas en el destino: así los de una a otra del mismo
	// subárbol quedan resueltos.
	for _, p := range m.Pages {
		if err := setLinks(ctx, tx, m.To, p.ID, p.Links); err != nil {
			return out, err
		}
		if err := resolveLinksTo(ctx, tx, m.To, p.NewSlug, p.ID); err != nil {
			return out, err
		}
		if err := writeIndex(ctx, tx, p.ID, p.Index); err != nil {
			return out, err
		}
	}
	return out, tx.Commit()
}

func sealedIDs(s []SealedInTree) []string {
	out := make([]string, len(s))
	for i, x := range s {
		out[i] = x.ID
	}
	return out
}

// WorkspaceAliases son los slugs viejos de un área (B17): sus enlaces a media también se
// reescriben al trasladar.
func (r *Repo) WorkspaceAliases(ctx context.Context, wsID string) ([]string, error) {
	rows, err := r.db.Read.QueryContext(ctx, `SELECT slug FROM workspace_aliases WHERE workspace_id = ?`, wsID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (string, error) {
		var s string
		return s, row.Scan(&s)
	})
}

// AncestorRank es el nivel más alto entre los padres de esa ruta en el área (0 si no tiene): la
// vista previa del traslado sube a él las páginas que lleguen debajo (§3.2).
func (r *Repo) AncestorRank(ctx context.Context, s Scope, slug string) (int, error) {
	return maxAncestorRank(ctx, r.db.Read, s, slug)
}
