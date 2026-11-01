package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

// §8.2: cambiar roles y entrar en modo auditoría piden reautenticarse; la confirmación es de la
// sesión, vence a los 10 minutos (en auth.TestReauth) y no cambia el 404 uniforme de nadie.
func TestReauth(t *testing.T) {
	f := newFixture(t)
	uniform := f.do("adminTI", "GET", "/api/w/no-existe/tree", "")
	space := "/api/w/" + f.personalSlug(t, "editorTI") + "/tree"
	add := `{"email":"outsider@empresa.cl","role":"viewer"}`
	panel := "/api/admin/workspaces/" + f.wsID(t, "ti") + "/members"

	required := func(who, method, path, body string) {
		t.Helper()
		if w := f.do(who, method, path, body); w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), `"reauth_required"`) {
			t.Errorf("%s %s %s: %d %s, want 401 reauth_required", who, method, path, w.Code, w.Body)
		}
	}
	uniformFor := func(who, method, path, body string) {
		t.Helper()
		if w := f.do(who, method, path, body); w.Code != uniform.Code || w.Body.String() != uniform.Body.String() {
			t.Errorf("%s %s %s: %d %s, want the uniform 404", who, method, path, w.Code, w.Body)
		}
	}
	required("adminTI", "POST", "/api/w/ti/members", add)
	required("adminTI", "DELETE", "/api/w/ti/members/"+f.users["viewerTI"], "")
	required("super", "POST", panel, add)
	required("super", "GET", space, "")
	// Para quien no tiene el rol, nada cambia: ni el 401 delata que la ruta existe.
	uniformFor("editorTI", "POST", "/api/w/ti/members", add)
	uniformFor("adminFin", "POST", "/api/w/ti/members", add)
	uniformFor("adminTI", "GET", space, "")
	uniformFor("adminTI", "POST", panel, add)

	// Contraseña y código, y cada error con su código.
	codes := f.totp["adminTI"].codes
	for _, c := range []struct {
		body string
		want int
		code string
	}{
		{`{"password":"otra-clave","code":"` + codes[0] + `"}`, http.StatusUnprocessableEntity, "wrong_password"},
		{`{"password":"` + password + `","code":"zzzz-zzzz-zzzz-zzzz"}`, http.StatusUnprocessableEntity, "invalid_code"},
		{`{"password":"` + password + `"}`, http.StatusUnprocessableEntity, "invalid_code"},
		{`{"password":"` + password + `","extra":1}`, http.StatusBadRequest, "invalid_request"},
	} {
		if w := f.do("adminTI", "POST", "/api/auth/reauth", c.body); w.Code != c.want || !strings.Contains(w.Body.String(), `"`+c.code+`"`) {
			t.Errorf("reauth %s: %d %s, want %d %s", c.body, w.Code, w.Body, c.want, c.code)
		}
	}
	required("adminTI", "POST", "/api/w/ti/members", add)

	f.reauth(t, "adminTI")
	f.expect(t, "adminTI", "POST", "/api/w/ti/members", add, http.StatusNoContent)
	f.expect(t, "adminTI", "DELETE", "/api/w/ti/members/"+f.users["outsider"], "", http.StatusNoContent)
	if e := find(auditOf(t, f, "super", "/api/admin/audit"), "auth.reauth", "adminTI"); e == nil || e.Meta["second_factor"] != "recovery" {
		t.Errorf("reauth left no trace: %+v", e)
	}
	// Vencida, se vuelve a pedir.
	if _, err := f.db.Write.Exec(`UPDATE sessions SET reauth_until = datetime('now', '-1 second')`); err != nil {
		t.Fatal(err)
	}
	required("adminTI", "POST", "/api/w/ti/members", add)

	// El superadmin entra en modo auditoría después de confirmar quién es.
	f.reauth(t, "super")
	f.expect(t, "super", "GET", space, "", http.StatusOK)
	f.expect(t, "super", "POST", panel, add, http.StatusNoContent)
	// Un espacio que no existe sigue siendo el 404 de siempre.
	uniformFor("super", "GET", "/api/w/~01arz3ndektsv4rrffq69g5fav/tree", "")
}
