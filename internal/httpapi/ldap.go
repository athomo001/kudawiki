package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/athomo001/kudawiki/internal/ldap"
	"github.com/go-chi/chi/v5"
)

// Directorio LDAP / AD (SPEC §8.6.4, §8.6.5): la conexión y su diagnóstico, solo del superadmin.
// La contraseña de la cuenta de servicio nunca sale: se devuelve enmascarada.

type ldapService interface {
	Settings(ctx context.Context) (ldap.Settings, error)
	Update(ctx context.Context, actorID string, p ldap.Patch, force bool) (ldap.Settings, *ldap.Report, error)
	TestConnection(ctx context.Context, p ldap.Patch) (ldap.Report, error)
	TestUser(ctx context.Context, p ldap.Patch, login string) (ldap.UserReport, error)
}

// ldapAdminRoutes cuelgan de /api/admin. Guardar pide reautenticarse: la conexión al directorio
// decide, desde F6.2b, quién entra a la wiki.
func (a api) ldapAdminRoutes(r chi.Router) {
	r.Get("/ldap/config", a.getLDAP)
	r.With(a.requireReauth).Put("/ldap/config", a.putLDAP)
	r.Post("/ldap/test", a.testLDAP)
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

// ldapFail suma al error el campo que hay que corregir o lo que contestó el directorio: es lo
// que el superadmin necesita para arreglar la configuración.
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
