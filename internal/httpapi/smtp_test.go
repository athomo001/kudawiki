package httpapi

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"
)

// §12: el correo lo configura solo el superadmin; a cualquier otra persona el panel le responde
// el 404 uniforme. La contraseña nunca vuelve al navegador.
func TestSMTPAdmin(t *testing.T) {
	f := newFixture(t)
	for _, who := range []string{"adminTI", "editorTI", "viewerTI", "outsider", ""} {
		for _, req := range [][3]string{
			{"GET", "/api/admin/smtp", ""}, {"PUT", "/api/admin/smtp", `{"host":"smtp.acme.cl"}`}, {"POST", "/api/admin/smtp/test", ""},
		} {
			want := http.StatusNotFound
			if who == "" {
				want = http.StatusUnauthorized
			}
			if w := f.do(who, req[0], req[1], req[2]); w.Code != want {
				t.Errorf("%s %s %s: %d, want %d", who, req[0], req[1], w.Code, want)
			}
		}
	}

	if w := f.do("super", "GET", "/api/admin/smtp", ""); !strings.Contains(w.Body.String(), `"locked":[]`) {
		t.Errorf("locked must be a list, even empty: %s", w.Body)
	}
	if w := f.do("super", "POST", "/api/admin/smtp/test", ""); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "smtp_not_configured") {
		t.Errorf("test without SMTP: %d %s", w.Code, w.Body)
	}
	if w := f.do("super", "PUT", "/api/admin/smtp", `{"port":99999}`); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad port: %d %s", w.Code, w.Body)
	}

	// Un puerto donde nadie escucha: el correo de prueba falla y la respuesta dice por qué.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	body, _ := json.Marshal(map[string]any{"host": "127.0.0.1", "port": port, "security": "none", "password": "secreta-del-correo", "from": "wiki@empresa.cl"})
	w := f.do("super", "PUT", "/api/admin/smtp", string(body))
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "secreta-del-correo") || !strings.Contains(w.Body.String(), `"password":"••••••"`) ||
		!strings.Contains(w.Body.String(), `"enabled":true`) {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	if w := f.do("super", "GET", "/api/admin/smtp", ""); strings.Contains(w.Body.String(), "secreta-del-correo") {
		t.Errorf("get leaks the password: %s", w.Body)
	}
	w = f.do("super", "POST", "/api/admin/smtp/test", "")
	var out struct{ Error struct{ Code, Detail string } }
	json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != http.StatusBadGateway || out.Error.Code != "smtp_failed" || !strings.Contains(out.Error.Detail, "127.0.0.1") {
		t.Errorf("test against a closed port: %d %s", w.Code, w.Body)
	}
}
