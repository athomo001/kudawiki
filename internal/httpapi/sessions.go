package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// sessionRoutes es "Mis sesiones" (§8.2, §14.1): cada persona ve y cierra solo las suyas.
func (a api) sessionRoutes(r chi.Router) {
	r.Get("/me/sessions", a.mySessions)
	r.Delete("/me/sessions/{id}", a.revokeSession)
	r.Post("/me/sessions/revoke-others", a.revokeOtherSessions)
}

func (a api) mySessions(w http.ResponseWriter, r *http.Request) {
	c, _ := r.Cookie(sessionCookie) // requireSession ya comprobó que está
	list, err := a.Auth.Sessions(r.Context(), currentUser(r), c.Value)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, list)
}

func (a api) revokeSession(w http.ResponseWriter, r *http.Request) {
	if err := a.Auth.RevokeSession(r.Context(), currentUser(r), chi.URLParam(r, "id")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a api) revokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	c, _ := r.Cookie(sessionCookie)
	n, err := a.Auth.RevokeOtherSessions(r.Context(), currentUser(r), c.Value)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, map[string]int64{"sessions_closed": n})
}
