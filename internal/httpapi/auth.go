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
	res, err := a.Auth.Login(r.Context(), clientIP(r), in.Email, in.Password)
	if a.rateLimited(w, r, err) {
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if res.Challenge != "" { // falta el segundo paso (§8.1): todavía no hay sesión
		http.SetCookie(w, &http.Cookie{Name: mfaCookie, Value: res.Challenge, Path: "/api/auth", MaxAge: 300,
			HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
		a.json(w, http.StatusOK, map[string]any{"mfa_required": true, "methods": res.Methods})
		return
	}
	setSessionCookie(w, res.Session)
	w.WriteHeader(http.StatusNoContent)
}

func setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/",
		MaxAge:   int((7 * 24 * time.Hour).Seconds()), // el servidor vence antes por inactividad (12 h)
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
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
	// El espacio personal nace la primera vez que la persona entra (§7).
	own, err := a.Personal.Of(r.Context(), u.ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	totp, err := a.Auth.TOTPStatus(r.Context(), u)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	// Los slugs viejos de sus áreas (B17): la interfaz lleva una URL vieja a la nueva.
	aliases, err := a.Read.MembershipAliases(r.Context(), u.ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	areas := make([]map[string]any, len(ms))
	for i, m := range ms {
		// viewers_can_propose: la interfaz le ofrece a un lector "Proponer un cambio" (§19.5).
		// require_change_summary: el editor marca el resumen como obligatorio (§4.3).
		// watermark_n2: el lector ve la marca de agua también en las páginas N2 (§4.15).
		areas[i] = map[string]any{"slug": m.Slug, "name": m.Name, "description": m.Description, "role": m.Role,
			"viewers_can_propose": m.ViewersCanPropose, "require_change_summary": m.RequireChangeSummary, "watermark_n2": m.WatermarkN2,
			"archived": m.Status == "archived", "aliases": append([]string{}, aliases[m.ID]...)}
	}
	a.json(w, http.StatusOK, map[string]any{
		// auth_source: una cuenta del directorio no cambia aquí su nombre ni su contraseña (§8.6.3).
		"id": u.ID, "email": u.Email, "full_name": u.FullName, "auth_source": u.AuthSource,
		"locale": u.Locale, "reading_prefs": u.ReadingPrefs, "editor_mode": u.EditorMode, "is_global_admin": u.IsGlobalAdmin, "workspaces": areas,
		"has_avatar": u.HasAvatar, "personal": map[string]string{"slug": own.Slug},
		// mfa_setup_required: la interfaz muestra solo la configuración del 2FA (§8.1); una passkey
		// lo cumple igual que el TOTP.
		"totp": totp, "mfa_setup_required": totp.Required && !totp.Enabled && u.Passkeys == 0,
		"passkeys": map[string]any{"count": u.Passkeys, "available": a.Auth.PasskeysAvailable()},
		"vault":    u.HasVault, // §6.3: sin bóveda no se le pueden compartir sellados
	})
}

// setPreferences es PATCH /me: nombre, idioma y preferencias de lectura (SPEC §14.1, §15).
func (a api) setPreferences(w http.ResponseWriter, r *http.Request) {
	var in struct {
		FullName     *string            `json:"full_name"`
		Locale       *string            `json:"locale"`
		ReadingPrefs *repo.ReadingPrefs `json:"reading_prefs"`
		EditorMode   *string            `json:"editor_mode"`
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
	if err := a.Auth.SetPreferences(r.Context(), currentUser(r).ID, auth.Preferences{Locale: in.Locale, ReadingPrefs: in.ReadingPrefs, EditorMode: in.EditorMode}); err != nil {
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
		// Sin el 2FA que se le exige, solo puede configurarlo (§8.1): la contraseña sola no basta.
		if setup, err := a.Auth.NeedsMFASetup(r.Context(), u); err != nil {
			a.fail(w, r, err)
			return
		} else if setup && !mfaSetupPaths[r.Method+" "+r.URL.Path] {
			a.error(w, r, http.StatusForbidden, "mfa_setup_required", nil)
			return
		}
		ctx := audit.WithActor(context.WithValue(r.Context(), userKey{}, u),
			audit.Actor{ID: u.ID, Email: u.Email, Name: u.FullName, IP: clientIP(r)})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
