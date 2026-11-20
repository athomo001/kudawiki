package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

// QA-06: el mínimo de las contraseñas lo fija solo el superadmin, nunca por debajo de 10, y llega
// a las pantallas sin sesión (invitación, restablecimiento) en los datos del sitio.
func TestPasswordPolicy(t *testing.T) {
	f := newFixture(t)
	for _, who := range []string{"adminTI", "editorTI", "outsider"} {
		if w := f.do(who, "PUT", "/api/admin/password-policy", `{"min_length":12}`); w.Code != http.StatusNotFound {
			t.Errorf("%s: %d", who, w.Code)
		}
	}
	if w := f.do("super", "GET", "/api/admin/password-policy", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"min_length":10`) {
		t.Errorf("default: %d %s", w.Code, w.Body)
	}
	if w := f.do("super", "PUT", "/api/admin/password-policy", `{"min_length":4}`); w.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(w.Body.String(), "invalid_password_min") {
		t.Errorf("below the floor: %d %s", w.Code, w.Body)
	}
	if w := f.do("super", "PUT", "/api/admin/password-policy", `{"min_length":14}`); w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	if w := f.do("", "GET", "/login", ""); !strings.Contains(w.Body.String(), `"password_min":14`) {
		t.Errorf("site data without the minimum: %s", w.Body)
	}
}
