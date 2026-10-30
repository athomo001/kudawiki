package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/athomo001/kudawiki/internal/auth"
	"github.com/go-chi/chi/v5"
)

// Perfil de la persona misma (SPEC §8.5.2, §14.1): contraseña y foto. El nombre, el idioma y
// las preferencias de lectura van por PATCH /me (setPreferences).

func (a api) profileRoutes(r chi.Router) {
	r.Post("/me/password", a.changePassword)
	r.Get("/me/avatar", a.avatar)
	r.Put("/me/avatar", a.setAvatar)
	r.Delete("/me/avatar", a.deleteAvatar)
}

func (a api) changePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Current      string `json:"current_password"`
		New          string `json:"new_password"`
		RevokeOthers bool   `json:"revoke_others"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	c, _ := r.Cookie(sessionCookie) // requireSession ya comprobó que está
	err := a.Auth.ChangePassword(r.Context(), currentUser(r).ID, c.Value, in.Current, in.New, in.RevokeOthers)
	if a.rateLimited(w, r, err) {
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// avatar sirve la foto propia. private: es de una persona y no la guarda un proxy; no-cache con
// ETag: una foto nueva se ve al instante y la misma no se vuelve a bajar.
func (a api) avatar(w http.ResponseWriter, r *http.Request) {
	png, err := a.Auth.Avatar(r.Context(), currentUser(r).ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	sum := sha256.Sum256(png)
	h := w.Header()
	h.Set("Content-Type", "image/png")
	h.Set("Cache-Control", "private, no-cache")
	h.Set("ETag", `"`+hex.EncodeToString(sum[:])+`"`)
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(png))
}

func (a api) setAvatar(w http.ResponseWriter, r *http.Request) {
	data, ok := a.uploadedFile(w, r, auth.MaxAvatarBytes)
	if !ok {
		return
	}
	if err := a.Auth.SetAvatar(r.Context(), currentUser(r).ID, data); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a api) deleteAvatar(w http.ResponseWriter, r *http.Request) {
	if err := a.Auth.DeleteAvatar(r.Context(), currentUser(r).ID); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// uploadedFile lee el campo "file" de un multipart, con un byte más que el máximo para que el
// servicio note que se pasó. Si falta o no se puede leer, ya respondió 400.
func (a api) uploadedFile(w http.ResponseWriter, r *http.Request, max int64) ([]byte, bool) {
	mr, err := r.MultipartReader()
	if err != nil {
		a.error(w, r, http.StatusBadRequest, "invalid_request", nil)
		return nil, false
	}
	for {
		part, err := mr.NextPart()
		if err != nil { // io.EOF incluido: no vino el campo "file"
			a.error(w, r, http.StatusBadRequest, "invalid_request", nil)
			return nil, false
		}
		if part.FormName() != "file" {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(part, max+1))
		if err != nil {
			a.error(w, r, http.StatusBadRequest, "invalid_request", nil)
			return nil, false
		}
		return data, true
	}
}

// rateLimited responde 429 con Retry-After si el error es un tope de intentos (§8.2).
func (a api) rateLimited(w http.ResponseWriter, r *http.Request, err error) bool {
	var limited auth.RateLimitedError
	if !errors.As(err, &limited) {
		return false
	}
	w.Header().Set("Retry-After", strconv.Itoa(int(limited.RetryAfter.Seconds())+1))
	a.error(w, r, http.StatusTooManyRequests, "rate_limited", nil)
	return true
}
