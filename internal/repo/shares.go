package repo

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Artículos compartidos entre áreas (SPEC §4.19, ADR-0010). Hay dos lados y ninguno amplía un
// Scope:
//   - el origen (s.WsID es el área de la página): pedir, aprobar, rechazar y dejar de compartir,
//     con las páginas que su nivel deja ver, como cualquier otra consulta del área;
//   - el destino (s.WsID es el área que recibe): solo llega a una página por un vínculo activo de
//     page_shares hacia s.WsID, y solo si la página sigue siendo N1, vigente y de un área (no de un
//     espacio personal). Esa condición está una sola vez, en sharedWhere.

// ErrNotShareable: la página no es N1 (§4.19: N2, N3 y el espacio personal no se comparten).
var ErrNotShareable = errors.New("not_shareable")

// ShareTarget es un área a la que se puede pedir compartir. Solo el nombre: es la única
// relajación de O1 (§19.40 a), y la ven solo editores y admins.
type ShareTarget struct{ ID, Name string }

// Share es un pedido o un vínculo visto desde el área de origen.
type Share struct {
	ID, PageID, PageSlug, PageTitle string
	TargetID, TargetName            string
	Status                          string // pending | active
	RequestedByID, RequestedBy      string
	Reviewer, Comment               string
	CreatedAt, ReviewedAt           time.Time
}

// SharedArticle es un artículo visto desde el área de destino. SourceWsID y SourceSlug son para
// reescribir los enlaces a la media; no salen en la API.
type SharedArticle struct {
	ID, PageID, Title, Description, Icon string
	SourceName                           string
	SourceWsID, SourceSlug               string
	Markdown, Lang                       string // vacíos en el listado
	UpdatedBy, Owner                     string
	UpdatedAt                            time.Time
}

// EndedShare es un vínculo que terminó, para la auditoría y los avisos de las dos áreas.
type EndedShare struct {
	ID, PageID, PageTitle, TargetID, Reason string
}

// sharedJoins y sharedWhere son la vía del destino: vínculo activo hacia el área de s, página N1 vigente que
// sigue en el área de origen, y el origen es un área. Lleva s.WsID como primer argumento de
// sharedWhere; sharedJoins va antes, para que cada consulta sume sus uniones en el medio.
const (
	sharedJoins = ` FROM page_shares sh
	JOIN pages p ON p.id = sh.page_id AND p.workspace_id = sh.workspace_id
	JOIN workspaces sw ON sw.id = sh.workspace_id`
	// Un área de origen desactivada o por borrar no se ve, tampoco por sus vínculos (§3.5); al
	// reactivarla vuelven a verse. Archivada sí: es solo lectura.
	sharedWhere = ` WHERE sh.target_workspace_id = ? AND sh.status = 'active' AND p.deleted_at IS NULL AND p.min_rank = 1 AND sw.kind = 'area'
	AND sw.status != 'deactivated' AND sw.delete_at IS NULL`
)

// ShareTargets son las áreas a las que se puede pedir compartir desde la de s: todas las demás
// áreas activas, sin los espacios personales (§3.5: una archivada no recibe nada nuevo).
func (r *Repo) ShareTargets(ctx context.Context, s Scope) ([]ShareTarget, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT id, name FROM workspaces WHERE kind = 'area' AND id <> ? AND status = 'active' AND delete_at IS NULL ORDER BY name`, s.WsID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (ShareTarget, error) {
		var t ShareTarget
		return t, row.Scan(&t.ID, &t.Name)
	})
}

// RequestShares pide compartir la página con cada destino; approved los deja activos de una vez
// (pide un admin del origen, que es quien aprueba). Un destino que ya tiene un pedido abierto o
// un vínculo activo se salta. Devuelve los ids creados, en el orden de targets.
func (r *Repo) RequestShares(ctx context.Context, s Scope, pageID, actorID string, targets, ids []string, approved bool) ([]string, error) {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var rank int
	err = tx.QueryRowContext(ctx, `SELECT p.min_rank FROM pages p JOIN workspaces w ON w.id = p.workspace_id
		WHERE p.id = ? AND p.workspace_id = ? AND p.deleted_at IS NULL AND p.min_rank <= ? AND w.kind = 'area'`,
		pageID, s.WsID, s.MaxRank).Scan(&rank)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if rank != 1 {
		return nil, ErrNotShareable
	}
	status, reviewer := "pending", any(nil)
	if approved {
		status, reviewer = "active", actorID
	}
	var created []string
	for i, target := range targets {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO page_shares (id, workspace_id, page_id, target_workspace_id, status, requested_by, reviewed_by, reviewed_at)
			 SELECT ?, ?, ?, w.id, ?, ?, ?, CASE WHEN ? IS NULL THEN NULL ELSE CURRENT_TIMESTAMP END
			 FROM workspaces w WHERE w.id = ? AND w.kind = 'area' AND w.id <> ?
			 ON CONFLICT DO NOTHING`,
			ids[i], s.WsID, pageID, status, actorID, reviewer, reviewer, target, s.WsID)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			created = append(created, ids[i])
		}
	}
	return created, tx.Commit()
}

const shareColumns = `SELECT sh.id, p.id, p.slug, p.title, sh.target_workspace_id, tw.name, sh.status, sh.requested_by, rq.full_name,
	COALESCE(rv.full_name, ''), COALESCE(sh.review_comment, ''), sh.created_at, sh.reviewed_at
	FROM page_shares sh
	JOIN pages p ON p.id = sh.page_id AND p.workspace_id = sh.workspace_id
	JOIN workspaces tw ON tw.id = sh.target_workspace_id
	JOIN users rq ON rq.id = sh.requested_by
	LEFT JOIN users rv ON rv.id = sh.reviewed_by
	WHERE sh.workspace_id = ? AND p.deleted_at IS NULL AND p.min_rank <= ?`

func scanShare(row scanner) (Share, error) {
	var sh Share
	var reviewed sql.NullTime
	err := row.Scan(&sh.ID, &sh.PageID, &sh.PageSlug, &sh.PageTitle, &sh.TargetID, &sh.TargetName, &sh.Status, &sh.RequestedByID, &sh.RequestedBy,
		&sh.Reviewer, &sh.Comment, &sh.CreatedAt, &reviewed)
	if errors.Is(err, sql.ErrNoRows) {
		return Share{}, ErrNotFound
	}
	sh.ReviewedAt = reviewed.Time
	return sh, err
}

// Shares son los pedidos pendientes y los vínculos activos del área de origen, de las páginas que
// s ve. pageID vacío: todas las páginas.
func (r *Repo) Shares(ctx context.Context, s Scope, pageID string) ([]Share, error) {
	q, args := shareColumns+` AND sh.status IN ('pending','active')`, []any{s.WsID, s.MaxRank}
	if pageID != "" {
		q += ` AND p.id = ?`
		args = append(args, pageID)
	}
	rows, err := r.db.Read.QueryContext(ctx, q+` ORDER BY sh.status DESC, sh.created_at DESC, sh.id`, args...)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanShare)
}

func (r *Repo) ShareByID(ctx context.Context, s Scope, id string) (Share, error) {
	return scanShare(r.db.Read.QueryRowContext(ctx, shareColumns+` AND sh.id = ?`, s.WsID, s.MaxRank, id))
}

// ReviewShare aprueba o rechaza un pedido pendiente del área de s. Aprobar exige que la página
// siga siendo N1 y vigente. Si ya no estaba pendiente (otro admin lo resolvió), ErrConflict.
func (r *Repo) ReviewShare(ctx context.Context, s Scope, id, reviewerID string, approve bool, comment string) error {
	status := "rejected"
	if approve {
		status = "active"
	}
	res, err := r.db.Write.ExecContext(ctx,
		`UPDATE page_shares SET status = ?, reviewed_by = ?, reviewed_at = CURRENT_TIMESTAMP, review_comment = NULLIF(?, '')
		 WHERE id = ? AND workspace_id = ? AND status = 'pending'
		   AND (? = 'rejected' OR EXISTS (SELECT 1 FROM pages p WHERE p.id = page_shares.page_id AND p.deleted_at IS NULL AND p.min_rank = 1))`,
		status, reviewerID, comment, id, s.WsID, status)
	if errors.Is(affected(res, err), ErrNotFound) {
		return ErrConflict
	}
	return err
}

// EndShare termina un pedido o un vínculo. Desde el origen (unshared) vale para pendientes y
// activos del área de s; desde el destino (removed), solo para activos hacia el área de s.
func (r *Repo) EndShare(ctx context.Context, s Scope, id, actorID, reason string) error {
	side, from := `workspace_id = ?`, `('pending','active')`
	if reason == "removed" {
		side, from = `target_workspace_id = ?`, `('active')`
	}
	res, err := r.db.Write.ExecContext(ctx,
		`UPDATE page_shares SET status = 'ended', end_reason = ?, ended_by = ?, ended_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND `+side+` AND status IN `+from, reason, actorID, id, s.WsID)
	return affected(res, err)
}

// EndInvalidShares termina los vínculos y pedidos del área de s cuya página dejó de poder
// compartirse: subió de nivel o se borró (§4.19). La lectura del destino ya no los mostraba
// (sharedJoins y sharedWhere); esto deja el estado al día y dice qué terminó, para avisar y auditar.
func (r *Repo) EndInvalidShares(ctx context.Context, s Scope, actorID string) ([]EndedShare, error) {
	rows, err := r.db.Write.QueryContext(ctx,
		`UPDATE page_shares SET status = 'ended', ended_by = NULLIF(?, ''), ended_at = CURRENT_TIMESTAMP,
		   end_reason = (SELECT CASE WHEN p.deleted_at IS NOT NULL THEN 'deleted' ELSE 'level' END FROM pages p WHERE p.id = page_shares.page_id)
		 WHERE workspace_id = ? AND status IN ('pending','active')
		   AND page_id IN (SELECT id FROM pages WHERE workspace_id = ? AND (deleted_at IS NOT NULL OR min_rank > 1))
		 RETURNING id, page_id, target_workspace_id, end_reason`, actorID, s.WsID, s.WsID)
	if err != nil {
		return nil, err
	}
	ended, err := collect(rows, func(row scanner) (EndedShare, error) {
		var e EndedShare
		return e, row.Scan(&e.ID, &e.PageID, &e.TargetID, &e.Reason)
	})
	if err != nil || len(ended) == 0 {
		return ended, err
	}
	for i := range ended {
		if err := r.db.Read.QueryRowContext(ctx, `SELECT title FROM pages WHERE id = ?`, ended[i].PageID).Scan(&ended[i].PageTitle); err != nil {
			return nil, err
		}
	}
	return ended, nil
}

// PageShared dice si la página de esa ruta tiene algún vínculo activo: mientras lo tenga, todo
// cambio de un editor va por propuesta (§4.19).
func (r *Repo) PageShared(ctx context.Context, s Scope, slug string) (bool, error) {
	var shared bool
	err := r.db.Read.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM page_shares sh JOIN pages p ON p.id = sh.page_id
		WHERE p.workspace_id = ? AND p.slug = ? AND p.deleted_at IS NULL AND sh.status = 'active')`, s.WsID, slug).Scan(&shared)
	return shared, err
}

// ShareAudience es cuántas personas de cada área de destino ven la página por un vínculo activo:
// lo que suma *¿Quién puede ver esto?* (§3.4).
type ShareAudience struct {
	Area    string
	Members int
}

func (r *Repo) ShareAudience(ctx context.Context, s Scope, pageID string) ([]ShareAudience, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT w.name, (SELECT COUNT(*) FROM workspace_members m JOIN users u ON u.id = m.user_id WHERE m.workspace_id = w.id AND u.is_active = 1)
		 FROM page_shares sh JOIN workspaces w ON w.id = sh.target_workspace_id
		 JOIN pages p ON p.id = sh.page_id AND p.workspace_id = sh.workspace_id
		 WHERE sh.workspace_id = ? AND sh.page_id = ? AND sh.status = 'active' AND p.min_rank <= ? ORDER BY w.name`, s.WsID, pageID, s.MaxRank)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (ShareAudience, error) {
		var a ShareAudience
		return a, row.Scan(&a.Area, &a.Members)
	})
}

func scanShared(row scanner) (SharedArticle, error) {
	var a SharedArticle
	err := row.Scan(&a.ID, &a.PageID, &a.Title, &a.Description, &a.Icon, &a.SourceName, &a.SourceWsID, &a.SourceSlug, &a.Markdown, &a.Lang,
		&a.UpdatedBy, &a.Owner, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SharedArticle{}, ErrNotFound
	}
	return a, err
}

// sharedQuery arma la consulta del destino: la vía de siempre con el autor de la última revisión y el
// dueño de la página.
func sharedQuery(content bool) string {
	cols := `'', ''`
	if content {
		cols = `p.content_markdown, p.lang`
	}
	return `SELECT sh.id, p.id, p.title, p.description, p.icon, sw.name, sw.id, sw.slug, ` + cols + `, u.full_name, COALESCE(o.full_name, ''), p.updated_at` +
		sharedJoins + ` JOIN users u ON u.id = p.updated_by LEFT JOIN users o ON o.id = p.owner_id` + sharedWhere
}

// SharedWith es lo que otras áreas le comparten al área de s: su *Compartido*, por área de origen.
func (r *Repo) SharedWith(ctx context.Context, s Scope) ([]SharedArticle, error) {
	rows, err := r.db.Read.QueryContext(ctx, sharedQuery(false)+` ORDER BY sw.name, p.title, sh.id`, s.WsID)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanShared)
}

// SharedArticleByID es un artículo del *Compartido* del área de s, con su contenido.
func (r *Repo) SharedArticleByID(ctx context.Context, s Scope, id string) (SharedArticle, error) {
	return scanShared(r.db.Read.QueryRowContext(ctx, sharedQuery(true)+` AND sh.id = ?`, s.WsID, id))
}

// SharedMedia es la media de un artículo compartido con el área de s: del área de origen y usada
// en la versión vigente del artículo. Cualquier otra media del origen no existe por esta vía.
func (r *Repo) SharedMedia(ctx context.Context, s Scope, shareID, mediaID string) (Media, string, error) {
	var m Media
	var wsID string
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT m.id, m.original_filename, m.mime_type, m.file_size_bytes, m.sha256, m.has_thumbnail, m.uploaded_by, m.created_at, sh.workspace_id`+
			sharedJoins+` JOIN media_assets m ON m.workspace_id = sh.workspace_id AND m.id = ?`+sharedWhere+
			` AND sh.id = ? AND m.deleted_at IS NULL AND instr(p.content_markdown, m.id) > 0`,
		mediaID, s.WsID, shareID).
		Scan(&m.ID, &m.Filename, &m.MimeType, &m.Size, &m.SHA256, &m.HasThumbnail, &m.UploadedBy, &m.CreatedAt, &wsID)
	if errors.Is(err, sql.ErrNoRows) {
		return Media{}, "", ErrNotFound
	}
	return m, wsID, err
}

// SharedSearchHit es un artículo compartido con el área de s que coincide con la búsqueda.
type SharedSearchHit struct {
	ID, Title, SourceName, Snippet string
}

// SearchShared busca en lo que otras áreas le comparten al área de s (§4.19): entra a su búsqueda
// con la marca de origen. Usa el mismo índice, siempre por sharedWhere.
func (r *Repo) SearchShared(ctx context.Context, s Scope, match string, limit int) ([]SharedSearchHit, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT sh.id, p.title, sw.name, snippet(pages_fts, 4, '`+HitStart+`', '`+HitEnd+`', '…', 14)`+
			sharedJoins+` JOIN pages_fts f ON f.rowid = p.search_rowid`+sharedWhere+` AND pages_fts MATCH ?`+
			` ORDER BY bm25(pages_fts, 10, 6, 4, 3, 1, 0.5, 10, 4, 3, 1) LIMIT ?`,
		s.WsID, match, limit)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (SharedSearchHit, error) {
		var h SharedSearchHit
		return h, row.Scan(&h.ID, &h.Title, &h.SourceName, &h.Snippet)
	})
}
