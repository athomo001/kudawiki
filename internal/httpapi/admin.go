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
	r.Patch("/users/{id}", a.patchUser)
	r.Post("/users/{id}/invite", a.resendInvite)
	r.Post("/users/{id}/password-reset", a.adminPasswordReset)
	a.offboardingRoutes(r)
	r.Get("/workspaces", a.listWorkspaces)
	r.Post("/workspaces", a.createWorkspace)
	r.With(a.adminScope).Patch("/workspaces/{id}", a.renameWorkspace)
	// Ciclo de vida (§3.5): borrar pide reautenticarse; cancelar el borrado, no.
	r.Put("/workspaces/{id}/status", a.setWorkspaceStatus)
	r.Put("/workspaces/{id}/slug", a.setWorkspaceSlug)
	r.With(a.requireReauth).Post("/workspaces/{id}/delete", a.deleteWorkspace)
	r.Delete("/workspaces/{id}/delete", a.cancelWorkspaceDelete)
	// Traslado entre áreas (§4.13): la vista previa no cambia nada; el traslado pide reautenticarse.
	r.Post("/transfer/preview", a.previewTransfer)
	r.With(a.requireReauth).Post("/transfer", a.transferPages)
	r.Get("/audit", a.audit)
	r.With(noStore).Get("/audit/export", a.exportAudit)
	r.Get("/health", a.globalHealth)
	r.Get("/access-review", a.globalAccessReview)
	r.Post("/access-review", a.confirmGlobalAccessReview)
	r.With(noStore).Get("/sealed", a.allSealed)
	a.siteAdminRoutes(r)
	a.smtpAdminRoutes(r)
	a.ldapAdminRoutes(r)
	a.backupAdminRoutes(r)
	r.Group(func(r chi.Router) {
		r.Use(a.globalTemplates)
		r.Get("/templates", a.listTemplates)
		a.templateAdminRoutes(r)
	})
	// El superadmin administra miembros sin ser miembro: el Scope sale del ID del área.
	r.Route("/workspaces/{id}/members", func(r chi.Router) {
		r.Use(a.adminScope)
		a.memberRoutes(r)
		// Desanclar lo que da el directorio es solo del superadmin (§8.3.2): no está en /w/{ws}.
		r.With(a.requireReauth).Post("/{user_id}/detach", a.detachMember)
	})
}

func (a api) detachMember(w http.ResponseWriter, r *http.Request) {
	if err := a.Workspaces.Detach(r.Context(), actor(r), scopeOf(r), chi.URLParam(r, "user_id")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a api) listUsers(w http.ResponseWriter, r *http.Request) {
	us, err := a.Read.ListUsers(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	// Las áreas de cada persona con su rol, para la tabla de Personas (§15).
	memberships, err := a.Read.UserAreas(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	areas := map[string][]map[string]string{}
	for _, u := range us {
		areas[u.ID] = []map[string]string{} // [] y no null: la interfaz lo recorre sin preguntar
	}
	for _, m := range memberships {
		areas[m.UserID] = append(areas[m.UserID], map[string]string{"name": m.Name, "role": m.Role})
	}
	out := make([]map[string]any, len(us))
	for i, u := range us {
		out[i] = map[string]any{"id": u.ID, "email": u.Email, "full_name": u.FullName,
			"is_global_admin": u.IsGlobalAdmin, "is_active": u.IsActive, "invited": u.Invited, "totp_enabled": u.TOTPEnabled,
			"passkeys": u.Passkeys, "auth_source": u.AuthSource, "areas": areas[u.ID]}
		// La baja (§8.4): en curso hasta offboarding_at, o completada en offboarded_at.
		if !u.OffboardingAt.IsZero() {
			out[i]["offboarding_at"] = u.OffboardingAt
		}
		if !u.OffboardedAt.IsZero() {
			out[i]["offboarded_at"] = u.OffboardedAt
		}
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
	ws, err := a.areas(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	// Miembros, admins, páginas y última edición, para la tabla de Áreas (§15).
	stats, err := a.Read.PanelAreas(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]map[string]any, len(ws))
	for i, x := range ws {
		st := stats[x.ID]
		if st == nil {
			st = &repo.PanelArea{}
		}
		if st.Admins == nil {
			st.Admins = []string{}
		}
		out[i] = map[string]any{"id": x.ID, "slug": x.Slug, "name": x.Name, "description": x.Description,
			"members": st.Members, "pages": st.Pages, "admins": st.Admins, "status": x.Status}
		if x.DeleteAt != nil {
			out[i]["delete_at"] = x.DeleteAt.UTC()
		}
		if !st.LastEdit.IsZero() {
			out[i]["last_edit"] = st.LastEdit
		}
	}
	a.json(w, http.StatusOK, out)
}

// areas son las áreas del panel, sin los espacios personales: el superadmin los abre solo en modo
// auditoría, con traza (§7), y ni su salud ni sus miembros pasan por el panel.
func (a api) areas(r *http.Request) ([]repo.Workspace, error) {
	all, err := a.Read.ListWorkspaces(r.Context())
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, w := range all {
		if w.Kind == "area" {
			out = append(out, w)
		}
	}
	return out, nil
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
		if err == nil && ws.Kind != "area" {
			err = repo.ErrNotFound
		}
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
	areas, err := a.areas(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.health(w, r, areas, true)
}

// health responde los problemas. Sin el SHA-256 de cada archivo (puede tardar minutos con
// muchos GB): eso lo hace `kudawiki doctor`, y la respuesta lo dice.
func (a api) health(w http.ResponseWriter, r *http.Request, areas []repo.Workspace, global bool) {
	problems, err := health.Check(r.Context(), a.Read, health.Options{StorageDir: a.StorageDir, Areas: areas, Global: global, Now: time.Now(), RemindAfter: a.RemindAfter})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := map[string]any{"hash_checked": false}
	// Los respaldos son de la instalación: solo en la vista del superadmin (§21.2.4).
	if global {
		backups, ps, err := health.BackupChecks(r.Context(), a.Read, time.Now())
		if err != nil {
			a.fail(w, r, err)
			return
		}
		problems = append(ps, problems...)
		out["backups"] = backups
		clock, ok := a.Clock.State(r.Context())
		problems = append(health.ClockChecks(clock, ok), problems...)
		if ok {
			out["clock"] = clock
		}
	}
	if problems == nil {
		problems = []health.Problem{}
	}
	out["problems"] = problems
	a.json(w, http.StatusOK, out)
}
