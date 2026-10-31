package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Recuperación de contraseña (SPEC §8.5). Los tokens viajan en el fragmento del enlace (#), que
// el navegador no envía al servidor: la SPA los manda por POST.

// requestPasswordReset es el formulario público: responde siempre 202, exista o no la cuenta y
// aunque se haya pasado el límite (§8.5.1). Si la wiki no lo ofrece, el 404 de siempre.
func (a api) requestPasswordReset(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	if err := a.Auth.RequestPasswordReset(r.Context(), clientIP(r), in.Email); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (a api) completePasswordReset(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token       string `json:"token"`
		NewPassword string `json:"new_password"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	if err := a.Auth.CompletePasswordReset(r.Context(), in.Token, in.NewPassword); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// adminPasswordReset es POST /admin/users/{id}/password-reset (§8.5.3): {delivery: "email"}
// encola el correo; "manual" devuelve el enlace, que se muestra una sola vez.
func (a api) adminPasswordReset(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Delivery string `json:"delivery"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	if in.Delivery != "email" && in.Delivery != "manual" {
		a.error(w, r, http.StatusBadRequest, "invalid_request", nil)
		return
	}
	token, err := a.Auth.AdminPasswordReset(r.Context(), currentUser(r).ID, clientIP(r), chi.URLParam(r, "id"), in.Delivery == "email")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if token == "" {
		a.json(w, http.StatusAccepted, map[string]string{"delivery": "email"})
		return
	}
	a.json(w, http.StatusCreated, map[string]string{"delivery": "manual", "reset_url": a.BaseURL.JoinPath("reset").String() + "#" + token})
}
