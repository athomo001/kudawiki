package httpapi

import (
	"net/http"

	"github.com/athomo001/kudawiki/internal/auth"
	"github.com/go-chi/chi/v5"
)

// mfaCookie lleva el desafío del segundo paso del login (§8.1). Vence a los 5 minutos, igual
// que el desafío en el servidor, y solo viaja a /api/auth.
const mfaCookie = "kw_mfa"

// mfaSetupPaths es lo único que puede pedir quien tiene que configurar el 2FA (§8.1): verse a sí
// mismo, salir y configurarlo. Lo demás responde 403 mfa_setup_required.
var mfaSetupPaths = map[string]bool{
	"GET /api/auth/me":          true,
	"POST /api/auth/logout":     true,
	"POST /api/me/totp":         true,
	"POST /api/me/totp/confirm": true,
}

func (a api) totpRoutes(r chi.Router) {
	r.Post("/me/totp", a.beginTOTP)
	r.Post("/me/totp/confirm", a.confirmTOTP)
	r.Delete("/me/totp", a.disableTOTP)
}

// verifyTOTP es POST /auth/totp, el segundo paso del login: sin sesión, con el desafío.
func (a api) verifyTOTP(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	c, err := r.Cookie(mfaCookie)
	if err != nil {
		a.fail(w, r, auth.ErrMFAExpired)
		return
	}
	token, err := a.Auth.VerifyLogin(r.Context(), c.Value, in.Code)
	if a.rateLimited(w, r, err) {
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: mfaCookie, Path: "/api/auth", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	setSessionCookie(w, token)
	w.WriteHeader(http.StatusNoContent)
}

// beginTOTP es POST /me/totp: el secreto nuevo, con el título del sitio como emisor (§15.1).
func (a api) beginTOTP(w http.ResponseWriter, r *http.Request) {
	site, err := a.Branding.Site(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	setup, err := a.Auth.BeginTOTP(r.Context(), currentUser(r), site.Title)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	// El secreto no se guarda en ninguna cache (§6: lo secreto nunca llega a la cache HTTP).
	w.Header().Set("Cache-Control", "no-store")
	a.json(w, http.StatusOK, setup)
}

func (a api) confirmTOTP(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code         string `json:"code"`
		RevokeOthers bool   `json:"revoke_others"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	c, _ := r.Cookie(sessionCookie) // requireSession ya comprobó que está
	codes, err := a.Auth.ConfirmTOTP(r.Context(), currentUser(r), c.Value, in.Code, in.RevokeOthers)
	if a.rateLimited(w, r, err) {
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	a.json(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

func (a api) disableTOTP(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	err := a.Auth.DisableTOTP(r.Context(), currentUser(r), in.Password, in.Code)
	if a.rateLimited(w, r, err) {
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// patchUser es PATCH /admin/users/{id} (§14.2). Por ahora solo restablece el 2FA; activar y dar
// superadmin llegan con la baja de usuarios (B15, F6).
func (a api) patchUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ResetTOTP bool `json:"reset_totp"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	if in.ResetTOTP {
		if err := a.Auth.ResetTOTP(r.Context(), chi.URLParam(r, "id")); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
