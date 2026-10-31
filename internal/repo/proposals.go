package repo

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Una propuesta hereda el nivel de su página (§3.2): la de una página que quien pregunta no ve,
// o que está en la papelera, no existe para él. La de una página nueva tiene el nivel que le
// tocó al proponerla.

type Proposal struct {
	ID       string
	PageID   string // vacío: página nueva todavía no aprobada
	Slug     string // la ruta actual de la página, o la propuesta si es nueva
	Base     int    // revisión sobre la que se hizo; 0 en una página nueva
	Current  int    // revisión actual de la página; 0 si es nueva
	MinRank  int
	Title    string
	Markdown string // vacío en los listados
	Lang     string
	Summary  string
	Status   string // pending | merged | rejected | withdrawn
	AuthorID string
	Author   string
	// ReviewerID y Reviewer: quien la resolvió (vacíos si sigue pendiente).
	ReviewerID string
	Reviewer   string
	Comment    string
	Merged     int // revisión publicada al aprobarse
	// CreatedAt es cuándo se envió (o reenvió); ReviewedAt, cuándo se resolvió (cero si no).
	CreatedAt  time.Time
	ReviewedAt time.Time
}

// ProposalFilter elige qué propuestas listar. AuthorID vacío: las de todos.
type ProposalFilter struct {
	Status   []string
	AuthorID string
	Limit    int
}

// ApprovalOverride es una excepción por subárbol (§5.2.1).
type ApprovalOverride struct {
	Prefix string
	Mode   string
}

const proposalFrom = ` FROM proposals pr
	LEFT JOIN pages p ON p.id = pr.page_id
	JOIN users u ON u.id = pr.author_id
	LEFT JOIN users rv ON rv.id = pr.reviewer_id
	WHERE pr.workspace_id = ?
	  AND ((pr.page_id IS NULL AND pr.min_rank <= ?) OR (p.deleted_at IS NULL AND p.min_rank <= ?))`

func proposalColumns(content bool) string {
	md := `''`
	if content {
		md = `pr.content_markdown`
	}
	return `SELECT pr.id, COALESCE(pr.page_id, ''), COALESCE(p.slug, pr.target_slug), COALESCE(pr.base_revision, 0), COALESCE(p.current_revision, 0),
		COALESCE(p.min_rank, pr.min_rank), pr.title, ` + md + `, pr.lang, COALESCE(pr.change_summary, ''), pr.status, pr.author_id, u.full_name,
		COALESCE(pr.reviewer_id, ''), COALESCE(rv.full_name, ''), COALESCE(pr.review_comment, ''), COALESCE(pr.merged_revision, 0), pr.created_at, pr.reviewed_at`
}

func scanProposal(row scanner) (Proposal, error) {
	var p Proposal
	var reviewed sql.NullTime
	err := row.Scan(&p.ID, &p.PageID, &p.Slug, &p.Base, &p.Current, &p.MinRank, &p.Title, &p.Markdown, &p.Lang, &p.Summary, &p.Status,
		&p.AuthorID, &p.Author, &p.ReviewerID, &p.Reviewer, &p.Comment, &p.Merged, &p.CreatedAt, &reviewed)
	if errors.Is(err, sql.ErrNoRows) {
		return Proposal{}, ErrNotFound
	}
	p.ReviewedAt = reviewed.Time
	return p, err
}

// CreateProposal guarda una propuesta pendiente. Para una página nueva calcula su nivel (el del
// padre más alto, §3.2): si ese padre no lo ve quien propone, o la ruta ya está ocupada, la ruta
// no está disponible (ErrHidden / ErrConflict, §19.35).
func (r *Repo) CreateProposal(ctx context.Context, s Scope, p Proposal) error {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var pageID, base any
	rank := 1
	if p.PageID == "" {
		anc, err := maxAncestorRank(ctx, tx, s, p.Slug)
		if err != nil {
			return err
		}
		if anc > s.MaxRank {
			return ErrHidden
		}
		var taken bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pages WHERE workspace_id = ? AND slug = ? AND deleted_at IS NULL)`,
			s.WsID, p.Slug).Scan(&taken); err != nil {
			return err
		}
		if taken {
			return ErrConflict
		}
		rank = max(1, anc)
	} else {
		pageID, base = p.PageID, p.Base
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO proposals (id, workspace_id, page_id, target_slug, base_revision, min_rank, title, content_markdown, lang, change_summary, status, author_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), 'pending', ?)`,
		p.ID, s.WsID, pageID, p.Slug, base, rank, p.Title, p.Markdown, p.Lang, p.Summary, p.AuthorID); err != nil {
		return err
	}
	if err := emit(ctx, tx, s, EventProposalSubmitted, p.AuthorID, p.PageID, p.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Repo) ProposalByID(ctx context.Context, s Scope, id string) (Proposal, error) {
	return scanProposal(r.db.Read.QueryRowContext(ctx, proposalColumns(true)+proposalFrom+` AND pr.id = ?`, s.WsID, s.MaxRank, s.MaxRank, id))
}

// Proposals lista sin el contenido: las pendientes, de la más nueva a la más vieja; las resueltas,
// por la fecha en que se resolvieron.
func (r *Repo) Proposals(ctx context.Context, s Scope, f ProposalFilter) ([]Proposal, error) {
	q := proposalColumns(false) + proposalFrom
	args := []any{s.WsID, s.MaxRank, s.MaxRank}
	if len(f.Status) > 0 {
		q += ` AND pr.status IN (?` + strings.Repeat(",?", len(f.Status)-1) + `)`
		for _, st := range f.Status {
			args = append(args, st)
		}
	}
	if f.AuthorID != "" {
		q += ` AND pr.author_id = ?`
		args = append(args, f.AuthorID)
	}
	q += ` ORDER BY COALESCE(pr.reviewed_at, pr.created_at) DESC, pr.id DESC LIMIT ?`
	args = append(args, max(1, f.Limit))
	rows, err := r.db.Read.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanProposal)
}

// UpdateProposal guarda lo que el autor cambió en una propuesta pendiente o rechazada (§5.2).
// Base 0 la deja donde estaba (una página nueva no tiene revisión).
func (r *Repo) UpdateProposal(ctx context.Context, s Scope, p Proposal) error {
	return affected(r.db.Write.ExecContext(ctx,
		`UPDATE proposals SET title = ?, content_markdown = ?, lang = ?, change_summary = NULLIF(?, ''),
		   base_revision = CASE WHEN page_id IS NULL THEN NULL ELSE ? END, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND workspace_id = ? AND author_id = ? AND status IN ('pending','rejected')`,
		p.Title, p.Markdown, p.Lang, p.Summary, p.Base, p.ID, s.WsID, p.AuthorID))
}

// SetProposalStatus pasa una propuesta de uno de los estados from a to. Si ya no estaba en
// ninguno (otra persona la resolvió entre la lectura y la escritura), ErrConflict. Volver a
// pendiente es reenviarla: cuenta desde ahí, borra la revisión anterior y vuelve a armar el
// recordatorio (§5.3). Reenviar y rechazar avisan; retirar, no.
func (r *Repo) SetProposalStatus(ctx context.Context, s Scope, id string, from []string, to, reviewerID, comment string) error {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	args := []any{to, to, to, reviewerID, reviewerID, comment, id, s.WsID}
	for _, st := range from {
		args = append(args, st)
	}
	var authorID, pageID string
	err = tx.QueryRowContext(ctx,
		`UPDATE proposals SET status = ?,
		   created_at = CASE WHEN ? = 'pending' THEN CURRENT_TIMESTAMP ELSE created_at END,
		   reminded_at = CASE WHEN ? = 'pending' THEN NULL ELSE reminded_at END,
		   reviewer_id = NULLIF(?, ''), reviewed_at = CASE WHEN ? = '' THEN NULL ELSE CURRENT_TIMESTAMP END,
		   review_comment = NULLIF(?, ''), updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND workspace_id = ? AND status IN (?`+strings.Repeat(",?", len(from)-1)+`)
		 RETURNING author_id, COALESCE(page_id, '')`, args...).Scan(&authorID, &pageID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	} else if err != nil {
		return err
	}
	switch to {
	case "pending":
		err = emit(ctx, tx, s, EventProposalSubmitted, authorID, pageID, id)
	case "rejected":
		err = emit(ctx, tx, s, EventProposalResolved, reviewerID, pageID, id)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// ErrProposalClosed: la propuesta ya no estaba pendiente al publicarla (otro admin la resolvió
// en el mismo momento). Es distinto de ErrConflict, que en una página es "otra revisión ganó".
var ErrProposalClosed = errors.New("proposal closed")

// mergeProposal deja la propuesta aprobada en la transacción que publica su revisión. Si ya no
// estaba pendiente, nada se publica.
func mergeProposal(ctx context.Context, tx *sql.Tx, s Scope, pageID string, rev int, m *ProposalMerge) error {
	if m == nil {
		return nil
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE proposals SET status = 'merged', page_id = ?, merged_revision = ?, reviewer_id = ?, review_comment = NULLIF(?, ''),
		   reviewed_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND workspace_id = ? AND status = 'pending'`, pageID, rev, m.ReviewerID, m.Comment, m.ID, s.WsID)
	if err := affected(res, err); errors.Is(err, ErrNotFound) {
		return ErrProposalClosed
	} else if err != nil {
		return err
	}
	return emit(ctx, tx, s, EventProposalResolved, m.ReviewerID, pageID, m.ID)
}

func mergeID(m *ProposalMerge) string {
	if m == nil {
		return ""
	}
	return m.ID
}

// affected convierte "no tocó ninguna fila" en ErrNotFound.
func affected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repo) ApprovalOverrides(ctx context.Context, s Scope) ([]ApprovalOverride, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT slug_prefix, approval_mode FROM approval_overrides WHERE workspace_id = ? ORDER BY slug_prefix`, s.WsID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (ApprovalOverride, error) {
		var o ApprovalOverride
		return o, row.Scan(&o.Prefix, &o.Mode)
	})
}

func (r *Repo) SetApprovalOverride(ctx context.Context, s Scope, o ApprovalOverride) error {
	_, err := r.db.Write.ExecContext(ctx,
		`INSERT INTO approval_overrides (workspace_id, slug_prefix, approval_mode) VALUES (?, ?, ?)
		 ON CONFLICT (workspace_id, slug_prefix) DO UPDATE SET approval_mode = excluded.approval_mode`, s.WsID, o.Prefix, o.Mode)
	return err
}

func (r *Repo) DeleteApprovalOverride(ctx context.Context, s Scope, prefix string) error {
	return affected(r.db.Write.ExecContext(ctx, `DELETE FROM approval_overrides WHERE workspace_id = ? AND slug_prefix = ?`, s.WsID, prefix))
}
