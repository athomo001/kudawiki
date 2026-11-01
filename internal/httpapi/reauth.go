package httpapi

import "net/http"

// reauth es POST /auth/reauth (§8.2, §14.1): contraseña y código, y esta sesión puede hacer lo
// delicado durante 10 minutos.
func (a api) reauth(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	c, _ := r.Cookie(sessionCookie) // requireSession ya comprobó que está
	err := a.Auth.Reauth(r.Context(), currentUser(r), c.Value, in.Password, in.Code)
	if a.rateLimited(w, r, err) {
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// requireReauth es el paso Reauth de §14.5. Va después de RoleGuard: quien no tiene el rol sigue
// viendo el 404 uniforme y no se entera de que la ruta pide algo más.
func (a api) requireReauth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.reauthenticated(w, r) {
			next.ServeHTTP(w, r)
		}
	})
}

// reauthenticated responde 401 reauth_required si la sesión no se reautenticó hace menos de
// 10 minutos; la interfaz pide entonces contraseña y código y repite el pedido.
func (a api) reauthenticated(w http.ResponseWriter, r *http.Request) bool {
	c, _ := r.Cookie(sessionCookie)
	ok, err := a.Auth.Reauthenticated(r.Context(), c.Value)
	if err != nil {
		a.fail(w, r, err)
		return false
	}
	if !ok {
		a.error(w, r, http.StatusUnauthorized, "reauth_required", nil)
	}
	return ok
}
