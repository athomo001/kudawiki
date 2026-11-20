package httpapi

import "net/http"

// Política de contraseñas locales (SPEC §8.1, QA-06): el mínimo de caracteres lo fija el
// superadmin en Administración → Personas, entre auth.MinPasswordLength y auth.MaxPasswordMin.

type passwordPolicy struct {
	MinLength int `json:"min_length"`
}

func (a api) getPasswordPolicy(w http.ResponseWriter, r *http.Request) {
	n, err := a.Auth.PasswordMin(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, passwordPolicy{MinLength: n})
}

func (a api) putPasswordPolicy(w http.ResponseWriter, r *http.Request) {
	var p passwordPolicy
	if !a.decode(w, r, &p) {
		return
	}
	if err := a.Auth.SetPasswordMin(r.Context(), currentUser(r).ID, p.MinLength); err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, p)
}
