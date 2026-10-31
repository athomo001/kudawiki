package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/athomo001/kudawiki/internal/health"

	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/athomo001/kudawiki/internal/workspace"
	"github.com/go-chi/chi/v5"
)

// adminRoutes es /api/admin (SPEC §14.2): solo el superadmin. A cualquier otra persona le
// responde el 404 uniforme, sin revelar que el panel existe.
func (a api) adminRoutes(r chi.Router) {
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !currentUser(r).IsGlobalAdmin {
				a.notFound(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	r.Get("/users", a.listUsers)
	r.Post("/users", a.inviteUser)
	r.Post("/users/{id}/invite", a.resendInvite)
	r.Post("/users/{id}/password-reset", a.adminPasswordReset)
	r.Get("/workspaces", a.listWorkspaces)
	r.Post("/workspaces", a.createWorkspace)
	r.With(a.adminScope).Patch("/workspaces/{id}", a.renameWorkspace)
	r.Get("/audit", a.audit)
	r.Get("/health", a.globalHealth)
	a.siteAdminRoutes(r)
	a.smtpAdminRoutes(r)
	// El superadmin administra miembros sin ser miembro: el Scope sale del ID del área.
	r.Route("/workspaces/{id}/members", func(r chi.Router) {
		r.Use(a.adminScope)
		a.memberRoutes(r)
	})
}

func (a api) listUsers(w http.ResponseWriter, r *http.Request) {
	us, err := a.Read.ListUsers(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]map[string]any, len(us))
	for i, u := range us {
		out[i] = map[string]any{"id": u.ID, "email": u.Email, "full_name": u.FullName,
			"is_global_admin": u.IsGlobalAdmin, "invited": u.Invited}
	}
	a.json(w, http.StatusOK, out)
}

// inviteUser manda la invitación por correo o, sin correo, devuelve el enlace una sola vez; el
// token va en el fragmento (#), que el navegador no envía al servidor ni queda en logs de acceso
// (SPEC §8.5).
func (a api) inviteUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		FullName string `json:"full_name"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	token, err := a.Auth.Invite(r.Context(), currentUser(r).ID, in.Email, in.FullName)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.inviteSent(w, token)
}

// resendInvite es POST /admin/users/{id}/invite: un enlace nuevo para una invitación pendiente.
func (a api) resendInvite(w http.ResponseWriter, r *http.Request) {
	token, err := a.Auth.ResendInvite(r.Context(), currentUser(r).ID, chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.inviteSent(w, token)
}

// inviteSent: sin token, la invitación salió por correo.
func (a api) inviteSent(w http.ResponseWriter, token string) {
	if token == "" {
		a.json(w, http.StatusCreated, map[string]string{"delivery": "email"})
		return
	}
	a.json(w, http.StatusCreated, map[string]string{"delivery": "manual", "invite_url": a.inviteURL(token)})
}

// inviteURL lleva el token en el fragmento (#): el navegador no lo manda al servidor (SPEC §8.5).
func (a api) inviteURL(token string) string {
	return a.BaseURL.JoinPath("invite").String() + "#" + token
}

func (a api) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	ws, err := a.Read.ListWorkspaces(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]map[string]string, len(ws))
	for i, x := range ws {
		out[i] = map[string]string{"id": x.ID, "slug": x.Slug, "name": x.Name, "description": x.Description}
	}
	a.json(w, http.StatusOK, out)
}

// renameWorkspace cambia el nombre y la descripción de un área desde el panel, sin ser miembro.
// Valida y audita igual que la Configuración del área (ws.update); la aprobación de cambios
// queda para su admin.
func (a api) renameWorkspace(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	if err := a.Workspaces.Update(r.Context(), scopeOf(r), workspace.Settings{Name: in.Name, Description: in.Description}); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a api) createWorkspace(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	x, err := a.Workspaces.Create(r.Context(), in.Slug, in.Name)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusCreated, map[string]string{"id": x.ID, "slug": x.Slug, "name": x.Name})
}

func (a api) adminScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := a.Read.WorkspaceByID(r.Context(), chi.URLParam(r, "id"))
		if errors.Is(err, repo.ErrNotFound) {
			a.notFound(w, r)
			return
		}
		if err != nil {
			a.fail(w, r, err)
			return
		}
		next.ServeHTTP(w, withScope(r, repo.Scope{WsID: ws.ID, MaxRank: repo.RoleRank("admin")}))
	})
}

// globalHealth es la salud de todas las áreas y del índice para el superadmin (SPEC §13.7).
func (a api) globalHealth(w http.ResponseWriter, r *http.Request) {
	areas, err := a.Read.ListWorkspaces(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.health(w, r, areas, true)
}

// health responde los problemas. Sin el SHA-256 de cada archivo (puede tardar minutos con
// muchos GB): eso lo hace `kudawiki doctor`, y la respuesta lo dice.
func (a api) health(w http.ResponseWriter, r *http.Request, areas []repo.Workspace, global bool) {
	problems, err := health.Check(r.Context(), a.Read, health.Options{StorageDir: a.StorageDir, Areas: areas, Global: global, Now: time.Now()})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if problems == nil {
		problems = []health.Problem{}
	}
	a.json(w, http.StatusOK, map[string]any{"problems": problems, "hash_checked": false})
}
