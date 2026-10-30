package httpapi

import (
	"errors"
	"net/http"

	"github.com/athomo001/kudawiki/internal/mail"
	"github.com/go-chi/chi/v5"
)

// Correo (SPEC §12): la configuración del servidor SMTP y el correo de prueba, solo del
// superadmin. La contraseña nunca sale: se devuelve enmascarada (§1).

func (a api) smtpAdminRoutes(r chi.Router) {
	r.Get("/smtp", a.getSMTP)
	r.Put("/smtp", a.putSMTP)
	r.Post("/smtp/test", a.testSMTP)
}

func (a api) getSMTP(w http.ResponseWriter, r *http.Request) {
	s, err := a.Mail.Settings(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, s)
}

func (a api) putSMTP(w http.ResponseWriter, r *http.Request) {
	var p mail.Patch
	if !a.decode(w, r, &p) {
		return
	}
	s, err := a.Mail.Update(r.Context(), currentUser(r).ID, p)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, s)
}

// testSMTP manda el correo de prueba a quien lo pide. Si el servidor lo rechaza, la respuesta
// trae lo que contestó (error.detail): es lo que el superadmin necesita para corregir la configuración.
func (a api) testSMTP(w http.ResponseWriter, r *http.Request) {
	err := a.Mail.SendTest(r.Context(), currentUser(r))
	var se *mail.SendError
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.As(err, &se):
		a.json(w, http.StatusBadGateway, map[string]any{"error": map[string]string{
			"code": "smtp_failed", "message": http.StatusText(http.StatusBadGateway), "detail": se.Detail}})
	default:
		a.fail(w, r, err)
	}
}
