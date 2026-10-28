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
