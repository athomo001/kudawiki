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

func (r *Repo) CreateWorkspace(ctx context.Context, w Workspace) error {
	_, err := r.db.Write.ExecContext(ctx, `INSERT INTO workspaces (id, slug, name) VALUES (?, ?, ?)`, w.ID, w.Slug, w.Name)
	return conflict(err)
}

// UpdateWorkspace cambia el nombre y la descripción del área del Scope (§3.1).
func (r *Repo) UpdateWorkspace(ctx context.Context, s Scope, name, description string) error {
	_, err := r.db.Write.ExecContext(ctx, `UPDATE workspaces SET name = ?, description = ? WHERE id = ?`, name, description, s.WsID)
	return err
}

// ListWorkspaces y WorkspaceByID son del panel del superadmin, que administra áreas sin ser miembro.
func (r *Repo) ListWorkspaces(ctx context.Context) ([]Workspace, error) {
	rows, err := r.db.Read.QueryContext(ctx, `SELECT id, slug, name, description FROM workspaces ORDER BY name`)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanWorkspace)
}

func (r *Repo) WorkspaceByID(ctx context.Context, id string) (Workspace, error) {
	return scanWorkspace(r.db.Read.QueryRowContext(ctx, `SELECT id, slug, name, description FROM workspaces WHERE id = ?`, id))
}

func scanWorkspace(row scanner) (Workspace, error) {
	var w Workspace
	err := row.Scan(&w.ID, &w.Slug, &w.Name, &w.Description)
	if errors.Is(err, sql.ErrNoRows) {
		return Workspace{}, ErrNotFound
	}
	return w, err
}

// MembershipBySlug es la consulta de la frontera de área: área inexistente y área sin
// membresía dan el mismo ErrNotFound, en una sola consulta, para que no se distingan (SPEC §3.3).
func (r *Repo) MembershipBySlug(ctx context.Context, slug, userID string) (Membership, error) {
	return scanMembership(r.db.Read.QueryRowContext(ctx,
		`SELECT w.id, w.slug, w.name, w.description, m.role FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = ? AND m.user_id = ?`, slug, userID))
}

// Memberships lista las áreas de una persona (para /auth/me).
func (r *Repo) Memberships(ctx context.Context, userID string) ([]Membership, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT w.id, w.slug, w.name, w.description, m.role FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE m.user_id = ? ORDER BY w.name`, userID)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanMembership)
}

func scanMembership(row scanner) (Membership, error) {
	var m Membership
	err := row.Scan(&m.ID, &m.Slug, &m.Name, &m.Description, &m.Role)
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
