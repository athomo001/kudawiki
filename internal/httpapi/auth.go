package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/auth"
	"github.com/athomo001/kudawiki/internal/repo"
)

const sessionCookie = "kw_session" // SPEC §8.2

type userKey struct{}

func currentUser(r *http.Request) repo.User { return r.Context().Value(userKey{}).(repo.User) }

func (a api) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	token, err := a.Auth.Login(r.Context(), clientIP(r), in.Email, in.Password)
	if a.rateLimited(w, r, err) {
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/",
		MaxAge:   int((7 * 24 * time.Hour).Seconds()), // el servidor vence antes por inactividad (12 h)
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (a api) logout(w http.ResponseWriter, r *http.Request) {
	c, _ := r.Cookie(sessionCookie)
	if err := a.Auth.Logout(r.Context(), c.Value); err != nil {
		a.fail(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

func (a api) me(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	ms, err := a.Read.Memberships(r.Context(), u.ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	areas := make([]map[string]string, len(ms))
	for i, m := range ms {
		areas[i] = map[string]string{"slug": m.Slug, "name": m.Name, "description": m.Description, "role": m.Role}
	}
	a.json(w, http.StatusOK, map[string]any{
		"id": u.ID, "email": u.Email, "full_name": u.FullName,
		"locale": u.Locale, "reading_prefs": u.ReadingPrefs, "is_global_admin": u.IsGlobalAdmin, "workspaces": areas,
		"has_avatar": u.HasAvatar,
	})
}

// setPreferences es PATCH /me: nombre, idioma y preferencias de lectura (SPEC §14.1, §15).
func (a api) setPreferences(w http.ResponseWriter, r *http.Request) {
	var in struct {
		FullName     *string            `json:"full_name"`
		Locale       *string            `json:"locale"`
		ReadingPrefs *repo.ReadingPrefs `json:"reading_prefs"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	if in.FullName != nil {
		if err := a.Auth.SetFullName(r.Context(), currentUser(r).ID, *in.FullName); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	if err := a.Auth.SetPreferences(r.Context(), currentUser(r).ID, auth.Preferences{Locale: in.Locale, ReadingPrefs: in.ReadingPrefs}); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a api) acceptInvite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token       string `json:"token"`
		NewPassword string `json:"new_password"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	if err := a.Auth.AcceptInvite(r.Context(), in.Token, in.NewPassword); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// requireSession es AuthSession de §14.5: sin sesión válida, 401.
func (a api) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			a.error(w, r, http.StatusUnauthorized, "unauthenticated", nil)
			return
		}
		u, err := a.Auth.Authenticate(r.Context(), c.Value)
		if errors.Is(err, auth.ErrNoSession) {
			a.error(w, r, http.StatusUnauthorized, "unauthenticated", nil)
			return
		}
		if err != nil {
			a.fail(w, r, err)
			return
		}
		ctx := audit.WithActor(context.WithValue(r.Context(), userKey{}, u),
			audit.Actor{ID: u.ID, Email: u.Email, Name: u.FullName, IP: clientIP(r)})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
