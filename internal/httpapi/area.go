package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/athomo001/kudawiki/internal/workspace"
	"github.com/go-chi/chi/v5"
)

type scopeKey struct{}

func withScope(r *http.Request, s repo.Scope) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), scopeKey{}, s))
}

func scopeOf(r *http.Request) repo.Scope { return r.Context().Value(scopeKey{}).(repo.Scope) }

// areaRoutes es /api/w/{ws} (SPEC §14.3), detrás de WorkspaceBoundary.
func (a api) areaRoutes(r chi.Router) {
	r.Use(a.workspaceBoundary)
	a.pageRoutes(r)
	a.proposalRoutes(r)
	a.areaHomeRoutes(r)
	a.draftRoutes(r)
	r.With(a.requireRank(repo.RoleRank("admin"))).Get("/settings", a.areaSettings)
	r.With(a.requireRank(repo.RoleRank("admin"))).Patch("/settings", a.updateAreaSettings)
	r.With(a.requireRank(repo.RoleRank("admin"))).Get("/health", a.areaHealth)
	r.Route("/members", func(r chi.Router) {
		r.Use(a.requireRank(repo.RoleRank("admin")))
		a.memberRoutes(r)
	})
}

// workspaceBoundary inyecta el Scope. Área inexistente y área sin membresía dan el mismo
// 404, con una sola consulta en los dos casos (SPEC §3.3).
func (a api) workspaceBoundary(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m, err := a.Read.MembershipBySlug(r.Context(), chi.URLParam(r, "ws"), currentUser(r).ID)
		if errors.Is(err, repo.ErrNotFound) {
			a.notFound(w, r)
			return
		}
		if err != nil {
			a.fail(w, r, err)
			return
		}
		next.ServeHTTP(w, withScope(r, repo.Scope{WsID: m.ID, MaxRank: repo.RoleRank(m.Role)}))
	})
}

// requireRank es RoleGuard: sin el rol, el mismo 404 que si la ruta no existiera (SPEC §3.2).
func (a api) requireRank(min int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if scopeOf(r).MaxRank < min {
				a.notFound(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// memberRoutes las comparten el admin de área y el panel del superadmin; las diferencias
// de permisos (quién da el rol admin) las decide workspace.Service, no la ruta.
func (a api) memberRoutes(r chi.Router) {
	r.Get("/", a.listMembers)
	r.Post("/", a.setMember)
	r.Delete("/{user_id}", a.removeMember)
}

func actor(r *http.Request) workspace.Actor {
	u := currentUser(r)
	return workspace.Actor{UserID: u.ID, Superadmin: u.IsGlobalAdmin}
}

func (a api) listMembers(w http.ResponseWriter, r *http.Request) {
	ms, err := a.Read.Members(r.Context(), scopeOf(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]map[string]string, len(ms))
	for i, m := range ms {
		out[i] = map[string]string{"user_id": m.UserID, "email": m.Email, "full_name": m.FullName, "role": m.Role}
	}
	a.json(w, http.StatusOK, out)
}

func (a api) setMember(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	if err := a.Workspaces.SetMember(r.Context(), actor(r), scopeOf(r), in.Email, in.Role); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a api) removeMember(w http.ResponseWriter, r *http.Request) {
	if err := a.Workspaces.RemoveMember(r.Context(), actor(r), scopeOf(r), chi.URLParam(r, "user_id")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// areaSettings es la configuración del área para su admin (SPEC §14.3): nombre, descripción,
// propuestas (§5.1) e historial (§4.3); las excepciones por subárbol van en /approval-overrides.
func (a api) areaSettings(w http.ResponseWriter, r *http.Request) {
	ws, err := a.Read.WorkspaceByID(r.Context(), scopeOf(r).WsID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, map[string]any{"name": ws.Name, "description": ws.Description,
		"approval_mode": ws.ApprovalMode, "viewers_can_propose": ws.ViewersCanPropose,
		"require_change_summary": ws.RequireChangeSummary, "keep_last": ws.KeepLast, "keep_days": ws.KeepDays})
}

func (a api) updateAreaSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name              *string `json:"name"`
		Description       *string `json:"description"`
		ApprovalMode      *string `json:"approval_mode"`
		ViewersCanPropose *bool   `json:"viewers_can_propose"`
		RequireSummary    *bool   `json:"require_change_summary"`
		KeepLast          *int    `json:"keep_last"`
		KeepDays          *int    `json:"keep_days"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	s := workspace.Settings{Name: in.Name, Description: in.Description, ApprovalMode: in.ApprovalMode, ViewersCanPropose: in.ViewersCanPropose,
		RequireChangeSummary: in.RequireSummary, KeepLast: in.KeepLast, KeepDays: in.KeepDays}
	if err := a.Workspaces.Update(r.Context(), scopeOf(r), s); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// areaHealth es la salud del área para su admin (SPEC §13.7).
func (a api) areaHealth(w http.ResponseWriter, r *http.Request) {
	ws, err := a.Read.WorkspaceByID(r.Context(), scopeOf(r).WsID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.health(w, r, []repo.Workspace{ws}, false)
}
