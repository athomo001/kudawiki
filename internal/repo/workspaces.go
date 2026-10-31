package repo

import (
	"context"
	"database/sql"
	"errors"
)

// Scope es el contexto que valida WorkspaceBoundary: toda función sobre el contenido o los
// miembros de un área lo exige, y no hay otra forma de pedirlos (SPEC §3.3).
type Scope struct {
	WsID    string
	MaxRank int
}

// RoleRank aplica SPEC §3.2: el nivel de lectura es el rol. 0 = rol inválido.
func RoleRank(role string) int {
	switch role {
	case "viewer":
		return 1
	case "editor":
		return 2
	case "admin":
		return 3
	}
	return 0
}

type Workspace struct {
	ID          string
	Slug        string
	Name        string
	Description string // la escribe el admin del área (§3.1)
	// Propuestas (§5.1): direct o required, y si los lectores pueden proponer (§19.5).
	ApprovalMode      string
	ViewersCanPropose bool
	// Historial (§4.3): resumen obligatorio y retención; 0 = sin límite.
	RequireChangeSummary bool
	KeepLast, KeepDays   int
	// Vigencia (§4.8): intervalo de revisión de las páginas que no tienen el suyo; 0 = no vencen.
	ReviewDays int
	// Kind es "area" o "personal" (§7); OwnerID, el dueño de un espacio personal.
	Kind, OwnerID string
	// QuizzesEnabled: el área puede apagar los cuestionarios (§4.16.7).
	QuizzesEnabled bool
}

// Membership es un área vista por uno de sus miembros.
type Membership struct {
	Workspace
	Role string
}

type Member struct {
	UserID   string
	Email    string
	FullName string
	Role     string
}

// CreateWorkspace crea el área con una copia de las plantillas globales (§4.9).
func (r *Repo) CreateWorkspace(ctx context.Context, w Workspace) error {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspaces (id, slug, name) VALUES (?, ?, ?)`, w.ID, w.Slug, w.Name); err != nil {
		return conflict(err)
	}
	if err := copyGlobalTemplates(ctx, tx, w.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateWorkspace cambia la configuración del área del Scope (§3.1, §5.1).
func (r *Repo) UpdateWorkspace(ctx context.Context, s Scope, w Workspace) error {
	_, err := r.db.Write.ExecContext(ctx, `UPDATE workspaces SET name = ?, description = ?, approval_mode = ?, viewers_can_propose = ?,
		 require_change_summary = ?, revisions_keep_last = nullif(?, 0), revisions_keep_days = nullif(?, 0), default_review_days = nullif(?, 0),
		 quizzes_enabled = ? WHERE id = ?`,
		w.Name, w.Description, w.ApprovalMode, w.ViewersCanPropose, w.RequireChangeSummary, w.KeepLast, w.KeepDays, w.ReviewDays, w.QuizzesEnabled, s.WsID)
	return err
}

const workspaceColumns = `w.id, w.slug, w.name, w.description, w.approval_mode, w.viewers_can_propose,
	w.require_change_summary, coalesce(w.revisions_keep_last, 0), coalesce(w.revisions_keep_days, 0), coalesce(w.default_review_days, 0),
	w.kind, coalesce(w.owner_user_id, ''), w.quizzes_enabled`

// ListWorkspaces y WorkspaceByID son del panel del superadmin, que administra áreas sin ser miembro.
// Traen también los espacios personales (Kind): el panel los deja afuera y doctor los revisa.
func (r *Repo) ListWorkspaces(ctx context.Context) ([]Workspace, error) {
	rows, err := r.db.Read.QueryContext(ctx, `SELECT `+workspaceColumns+` FROM workspaces w ORDER BY w.name`)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanWorkspace)
}

func (r *Repo) WorkspaceByID(ctx context.Context, id string) (Workspace, error) {
	return scanWorkspace(r.db.Read.QueryRowContext(ctx, `SELECT `+workspaceColumns+` FROM workspaces w WHERE w.id = ?`, id))
}

func scanWorkspace(row scanner) (Workspace, error) {
	var w Workspace
	err := row.Scan(&w.ID, &w.Slug, &w.Name, &w.Description, &w.ApprovalMode, &w.ViewersCanPropose, &w.RequireChangeSummary, &w.KeepLast, &w.KeepDays, &w.ReviewDays, &w.Kind, &w.OwnerID, &w.QuizzesEnabled)
	if errors.Is(err, sql.ErrNoRows) {
		return Workspace{}, ErrNotFound
	}
	return w, err
}

// MembershipBySlug es la consulta de la frontera de área: área inexistente y área sin
// membresía dan el mismo ErrNotFound, en una sola consulta, para que no se distingan (SPEC §3.3).
func (r *Repo) MembershipBySlug(ctx context.Context, slug, userID string) (Membership, error) {
	return scanMembership(r.db.Read.QueryRowContext(ctx,
		`SELECT `+workspaceColumns+`, m.role FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = ? AND m.user_id = ?`, slug, userID))
}

// Memberships lista las áreas de una persona (para /auth/me), sin su espacio personal, que no va
// en el selector de áreas (§7).
func (r *Repo) Memberships(ctx context.Context, userID string) ([]Membership, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT `+workspaceColumns+`, m.role FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE m.user_id = ? AND w.kind = 'area' ORDER BY w.name`, userID)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanMembership)
}

func scanMembership(row scanner) (Membership, error) {
	var m Membership
	err := row.Scan(&m.ID, &m.Slug, &m.Name, &m.Description, &m.ApprovalMode, &m.ViewersCanPropose,
		&m.RequireChangeSummary, &m.KeepLast, &m.KeepDays, &m.ReviewDays, &m.Kind, &m.OwnerID, &m.QuizzesEnabled, &m.Role)
	if errors.Is(err, sql.ErrNoRows) {
		return Membership{}, ErrNotFound
	}
	return m, err
}

func (r *Repo) Members(ctx context.Context, s Scope) ([]Member, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT u.id, u.email, u.full_name, m.role FROM workspace_members m
		 JOIN users u ON u.id = m.user_id
		 WHERE m.workspace_id = ? ORDER BY u.full_name`, s.WsID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (Member, error) {
		var m Member
		return m, row.Scan(&m.UserID, &m.Email, &m.FullName, &m.Role)
	})
}

// MemberRole devuelve "" si la persona no es miembro.
func (r *Repo) MemberRole(ctx context.Context, s Scope, userID string) (string, error) {
	var role string
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT role FROM workspace_members WHERE workspace_id = ? AND user_id = ?`, s.WsID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return role, err
}

// SetMember agrega o cambia el rol (upsert).
func (r *Repo) SetMember(ctx context.Context, s Scope, userID, role string) error {
	_, err := r.db.Write.ExecContext(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES (?, ?, ?)
		 ON CONFLICT (workspace_id, user_id) DO UPDATE SET role = excluded.role`, s.WsID, userID, role)
	return err
}

func (r *Repo) RemoveMember(ctx context.Context, s Scope, userID string) error {
	_, err := r.db.Write.ExecContext(ctx,
		`DELETE FROM workspace_members WHERE workspace_id = ? AND user_id = ?`, s.WsID, userID)
	return err
}
