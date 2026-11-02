package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/athomo001/kudawiki/internal/ldap"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/go-chi/chi/v5"
)

// Directorio LDAP / AD (SPEC §8.6.4, §8.6.5): la conexión y su diagnóstico, solo del superadmin.
// La contraseña de la cuenta de servicio nunca sale: se devuelve enmascarada.

type ldapService interface {
	Settings(ctx context.Context) (ldap.Settings, error)
	Update(ctx context.Context, actorID string, p ldap.Patch, force bool) (ldap.Settings, *ldap.Report, error)
	TestConnection(ctx context.Context, p ldap.Patch) (ldap.Report, error)
	TestUser(ctx context.Context, p ldap.Patch, login string) (ldap.UserReport, error)
	Groups(ctx context.Context, q string) ([]ldap.Group, bool, error)
	Mappings(ctx context.Context) ([]repo.LDAPMapping, error)
	Preview(ctx context.Context, in ldap.MappingInput) (ldap.Preview, error)
	CreateMapping(ctx context.Context, actorID string, in ldap.MappingInput) (repo.LDAPMapping, error)
	UpdateMapping(ctx context.Context, id, role string) (repo.LDAPMapping, error)
	DeleteMapping(ctx context.Context, id string) error
	Available(ctx context.Context) bool
	PasswordHelpURL(ctx context.Context) string
	Link(ctx context.Context, userID, login string) (repo.User, error)
	Status(ctx context.Context, fresh bool) ldap.Status
	Sync(ctx context.Context, o ldap.SyncOptions) (ldap.SyncReport, error)
	SetDebug(ctx context.Context, on bool) time.Time
}

// ldapAdminRoutes cuelgan de /api/admin. Guardar la conexión y cambiar las reglas de mapeo pide
// reautenticarse: deciden quién entra a la wiki y con qué rol en cada área (§8.3, §8.6.3).
func (a api) ldapAdminRoutes(r chi.Router) {
	r.Get("/ldap/config", a.getLDAP)
	r.With(a.requireReauth).Put("/ldap/config", a.putLDAP)
	// Vincular una cuenta local con el directorio le quita la contraseña local (§8.6.3).
	r.With(a.requireReauth).Post("/users/{id}/link-ldap", a.linkLDAP)
	r.Post("/ldap/test", a.testLDAP)
	r.Get("/ldap/groups", a.ldapGroups)
	r.Get("/ldap/mappings", a.ldapMappings)
	r.Post("/ldap/mappings/preview", a.previewLDAPMapping)
	r.With(a.requireReauth).Post("/ldap/mappings", a.createLDAPMapping)
	r.With(a.requireReauth).Patch("/ldap/mappings/{id}", a.updateLDAPMapping)
	r.With(a.requireReauth).Delete("/ldap/mappings/{id}", a.deleteLDAPMapping)
	// La sincronización (§8.3.3): ver qué haría no cambia nada; aplicarla puede dar de baja a
	// personas, y forzarla pasa por encima de las salvaguardas (§8.3.4).
	r.Get("/ldap/status", a.ldapStatus)
	r.Post("/ldap/sync/preview", a.previewLDAPSync)
	r.With(a.requireReauth).Post("/ldap/sync", a.runLDAPSync)
	r.Put("/ldap/debug", a.setLDAPDebug)
}

func (a api) ldapStatus(w http.ResponseWriter, r *http.Request) {
	a.json(w, http.StatusOK, a.LDAP.Status(r.Context(), true))
}

func (a api) previewLDAPSync(w http.ResponseWriter, r *http.Request) {
	rep, err := a.LDAP.Sync(r.Context(), ldap.SyncOptions{ActorID: currentUser(r).ID, Trigger: "manual", DryRun: true})
	if err != nil {
		a.ldapFail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, rep)
}

func (a api) runLDAPSync(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Force bool `json:"force"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	rep, err := a.LDAP.Sync(r.Context(), ldap.SyncOptions{ActorID: currentUser(r).ID, Trigger: "manual", Force: in.Force})
	if err != nil {
		a.ldapFail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, rep)
}

// setLDAPDebug enciende el log de depuración por 30 minutos o lo apaga (§8.6.5).
func (a api) setLDAPDebug(w http.ResponseWriter, r *http.Request) {
	var in struct {
		On bool `json:"on"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	until := a.LDAP.SetDebug(r.Context(), in.On)
	out := map[string]any{"on": in.On}
	if in.On {
		out["until"] = until
	}
	a.json(w, http.StatusOK, out)
}

// ldapSettings suma los valores de cada flavor: el panel los muestra donde el campo está vacío.
type ldapSettings struct {
	ldap.Settings
	Defaults map[string]ldap.Defaults `json:"defaults"`
}

func (a api) getLDAP(w http.ResponseWriter, r *http.Request) {
	s, err := a.LDAP.Settings(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, ldapSettings{s, ldap.Flavors})
}

func (a api) putLDAP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ldap.Patch
		Force bool `json:"force"` // "Guardar igual" aunque la prueba falle (§8.6.4)
	}
	if !a.decode(w, r, &body) {
		return
	}
	s, report, err := a.LDAP.Update(r.Context(), currentUser(r).ID, body.Patch, body.Force)
	if errors.Is(err, ldap.ErrTestFailed) {
		// El informe va dentro del error, con el resto de lo que el panel muestra al fallar.
		a.json(w, http.StatusUnprocessableEntity, map[string]any{"error": map[string]any{
			"code": err.Error(), "message": http.StatusText(http.StatusUnprocessableEntity), "test": report}})
		return
	}
	if err != nil {
		a.ldapFail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, ldapSettings{s, ldap.Flavors})
}

// testLDAP prueba lo que está en el formulario, sin guardarlo: {kind: "connection"} o
// {kind: "user", login}, con los campos en config.
func (a api) testLDAP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind   string     `json:"kind"`
		Login  string     `json:"login"`
		Config ldap.Patch `json:"config"`
	}
	if !a.decode(w, r, &body) {
		return
	}
	var out any
	var err error
	switch body.Kind {
	case "connection":
		out, err = a.LDAP.TestConnection(r.Context(), body.Config)
	case "user":
		out, err = a.LDAP.TestUser(r.Context(), body.Config, body.Login)
	default:
		a.error(w, r, http.StatusBadRequest, "invalid_request", nil)
		return
	}
	if err != nil {
		a.ldapFail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, out)
}

func (a api) ldapGroups(w http.ResponseWriter, r *http.Request) {
	groups, truncated, err := a.LDAP.Groups(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		a.ldapFail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, map[string]any{"groups": groups, "truncated": truncated})
}

type ldapMappingView struct {
	ID            string `json:"id"`
	GroupGUID     string `json:"group_guid"`
	GroupSID      string `json:"group_sid,omitempty"`
	GroupDN       string `json:"group_dn"`
	GroupName     string `json:"group_name"`
	WorkspaceID   string `json:"workspace_id"`
	WorkspaceSlug string `json:"workspace_slug"`
	WorkspaceName string `json:"workspace_name"`
	Role          string `json:"role"`
}

func mappingView(m repo.LDAPMapping) ldapMappingView {
	return ldapMappingView{ID: m.ID, GroupGUID: m.GroupGUID, GroupSID: m.GroupSID, GroupDN: m.GroupDN, GroupName: m.GroupName,
		WorkspaceID: m.WsID, WorkspaceSlug: m.WsSlug, WorkspaceName: m.WsName, Role: m.Role}
}

func (a api) ldapMappings(w http.ResponseWriter, r *http.Request) {
	list, err := a.LDAP.Mappings(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]ldapMappingView, 0, len(list))
	for _, m := range list {
		out = append(out, mappingView(m))
	}
	a.json(w, http.StatusOK, out)
}

// previewLDAPMapping dice qué haría la regla antes de guardarla (§8.3.4); no cambia nada.
func (a api) previewLDAPMapping(w http.ResponseWriter, r *http.Request) {
	var in ldap.MappingInput
	if !a.decode(w, r, &in) {
		return
	}
	p, err := a.LDAP.Preview(r.Context(), in)
	if err != nil {
		a.ldapFail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, p)
}

func (a api) createLDAPMapping(w http.ResponseWriter, r *http.Request) {
	var in ldap.MappingInput
	if !a.decode(w, r, &in) {
		return
	}
	m, err := a.LDAP.CreateMapping(r.Context(), currentUser(r).ID, in)
	if err != nil {
		a.ldapFail(w, r, err)
		return
	}
	a.json(w, http.StatusCreated, mappingView(m))
}

func (a api) updateLDAPMapping(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Role string `json:"role"`
	}
	if !a.decode(w, r, &body) {
		return
	}
	m, err := a.LDAP.UpdateMapping(r.Context(), chi.URLParam(r, "id"), body.Role)
	if err != nil {
		a.ldapFail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, mappingView(m))
}

func (a api) deleteLDAPMapping(w http.ResponseWriter, r *http.Request) {
	if err := a.LDAP.DeleteMapping(r.Context(), chi.URLParam(r, "id")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ldapFail suma al error el campo que hay que corregir o lo que contestó el directorio: es lo
// que el superadmin necesita para arreglar la configuración.
func (a api) linkLDAP(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Login string `json:"login"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	u, err := a.LDAP.Link(r.Context(), chi.URLParam(r, "id"), in.Login)
	if err != nil {
		a.ldapFail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, map[string]string{"id": u.ID, "email": u.Email, "full_name": u.FullName})
}

func (a api) ldapFail(w http.ResponseWriter, r *http.Request, err error) {
	var fe *ldap.FieldError
	var ue *ldap.UnavailableError
	switch {
	case errors.As(err, &fe):
		status := http.StatusUnprocessableEntity
		if errors.Is(err, ldap.ErrLocked) {
			status = http.StatusConflict
		}
		a.json(w, status, map[string]any{"error": map[string]string{
			"code": err.Error(), "message": http.StatusText(status), "field": fe.Field}})
	case errors.As(err, &ue):
		a.json(w, http.StatusBadGateway, map[string]any{"error": map[string]string{
			"code": err.Error(), "message": http.StatusText(http.StatusBadGateway), "detail": ue.Detail()}})
	default:
		a.fail(w, r, err)
	}
}
