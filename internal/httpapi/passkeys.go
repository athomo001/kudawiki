package httpapi

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// passkeyCookie lleva la ceremonia del login con passkey (§8.1): como kw_mfa, vence a los 5
// minutos y solo viaja a /api/auth.
const passkeyCookie = "kw_passkey"

// Registrar y quitar una passkey piden reautenticarse (§8.2): con una sesión robada se podría
// sumar una passkey propia y entrar sin contraseña aunque después se cierre esa sesión.
func (a api) passkeyRoutes(r chi.Router) {
	r.Get("/me/passkeys", a.listPasskeys)
	r.With(a.requireReauth).Post("/me/passkeys/begin", a.beginPasskey)
	r.Post("/me/passkeys/finish", a.finishPasskey)
	r.With(a.requireReauth).Delete("/me/passkeys/{id}", a.deletePasskey)
	r.Post("/auth/reauth/passkey/begin", a.beginPasskeyReauth)
	r.Post("/auth/reauth/passkey/finish", a.finishPasskeyReauth)
}

func (a api) listPasskeys(w http.ResponseWriter, r *http.Request) {
	list, err := a.Auth.Passkeys(r.Context(), currentUser(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, list)
}

// beginPasskey devuelve las opciones de navigator.credentials.create(), con el título del sitio
// como nombre del RP (§15.1).
func (a api) beginPasskey(w http.ResponseWriter, r *http.Request) {
	site, err := a.Branding.Site(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	c, _ := r.Cookie(sessionCookie) // requireSession ya comprobó que está
	opts, err := a.Auth.BeginPasskey(r.Context(), currentUser(r), c.Value, site.Title)
	a.options(w, r, opts, err)
}

func (a api) finishPasskey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name       string          `json:"name"`
		Credential json.RawMessage `json:"credential"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	c, _ := r.Cookie(sessionCookie)
	p, err := a.Auth.FinishPasskey(r.Context(), currentUser(r), c.Value, in.Name, in.Credential)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusCreated, p)
}

func (a api) deletePasskey(w http.ResponseWriter, r *http.Request) {
	if err := a.Auth.DeletePasskey(r.Context(), currentUser(r), chi.URLParam(r, "id")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// beginPasskeyLogin es POST /auth/passkey/begin: sin sesión ni email.
func (a api) beginPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	opts, token, err := a.Auth.BeginPasskeyLogin(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: passkeyCookie, Value: token, Path: "/api/auth", MaxAge: 300,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	a.options(w, r, opts, nil)
}

// finishPasskeyLogin es POST /auth/passkey/finish: el cuerpo es la credencial tal como la da
// PublicKeyCredential.toJSON(). Si hay un segundo paso pendiente (kw_mfa), también lo cierra.
func (a api) finishPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	credential, ok := a.credential(w, r)
	if !ok {
		return
	}
	var token, challenge string
	if c, err := r.Cookie(passkeyCookie); err == nil {
		token = c.Value
	}
	if c, err := r.Cookie(mfaCookie); err == nil {
		challenge = c.Value
	}
	session, err := a.Auth.FinishPasskeyLogin(r.Context(), clientIP(r), token, challenge, credential)
	if a.rateLimited(w, r, err) {
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	for _, name := range []string{passkeyCookie, mfaCookie} {
		http.SetCookie(w, &http.Cookie{Name: name, Path: "/api/auth", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	}
	setSessionCookie(w, session)
	w.WriteHeader(http.StatusNoContent)
}

func (a api) beginPasskeyReauth(w http.ResponseWriter, r *http.Request) {
	c, _ := r.Cookie(sessionCookie)
	opts, err := a.Auth.BeginPasskeyReauth(r.Context(), currentUser(r), c.Value)
	a.options(w, r, opts, err)
}

func (a api) finishPasskeyReauth(w http.ResponseWriter, r *http.Request) {
	credential, ok := a.credential(w, r)
	if !ok {
		return
	}
	c, _ := r.Cookie(sessionCookie)
	err := a.Auth.FinishPasskeyReauth(r.Context(), currentUser(r), c.Value, credential)
	if a.rateLimited(w, r, err) {
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// options responde las opciones de una ceremonia WebAuthn, que llevan un desafío de un solo uso:
// ninguna cache las guarda.
func (a api) options(w http.ResponseWriter, r *http.Request, opts json.RawMessage, err error) {
	if err != nil {
		a.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	a.json(w, http.StatusOK, opts)
}

// credential lee el cuerpo como la credencial que firmó el navegador; la valida la librería.
func (a api) credential(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil || !json.Valid(body) {
		a.error(w, r, http.StatusBadRequest, "invalid_json", nil)
		return nil, false
	}
	return body, true
}
