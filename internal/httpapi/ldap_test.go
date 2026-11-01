package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/athomo001/kudawiki/internal/ldap/ldaptest"
)

// §8.6.4: la conexión al directorio la configura solo el superadmin; a cualquier otra persona
// el panel le responde el 404 uniforme. Guardar pide reautenticarse y la contraseña nunca
// vuelve al navegador.
func TestLDAPAdmin(t *testing.T) {
	f := newFixture(t)
	for _, who := range []string{"adminTI", "editorTI", "viewerTI", "outsider", ""} {
		for _, req := range [][3]string{
			{"GET", "/api/admin/ldap/config", ""}, {"PUT", "/api/admin/ldap/config", `{"flavor":"generic"}`},
			{"POST", "/api/admin/ldap/test", `{"kind":"connection"}`},
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

	w := f.do("super", "GET", "/api/admin/ldap/config", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"locked":[]`) || !strings.Contains(w.Body.String(), `"defaults":{"ad":`) {
		t.Errorf("get: %d %s", w.Code, w.Body)
	}
	if w := f.do("super", "PUT", "/api/admin/ldap/config", `{"flavor":"generic"}`); w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "reauth_required") {
		t.Errorf("put without reauth: %d %s", w.Code, w.Body)
	}
	f.reauth(t, "super")
	if w := f.do("super", "PUT", "/api/admin/ldap/config", `{"urls":["ldap://dc01"]}`); w.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(w.Body.String(), `"field":"start_tls"`) {
		t.Errorf("plaintext: %d %s", w.Code, w.Body)
	}
	if w := f.do("super", "POST", "/api/admin/ldap/test", `{"kind":"connection"}`); w.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(w.Body.String(), `"code":"ldap_incomplete","field":"urls"`) {
		t.Errorf("test without config: %d %s", w.Code, w.Body)
	}
	if w := f.do("super", "POST", "/api/admin/ldap/test", `{"kind":"other"}`); w.Code != http.StatusBadRequest {
		t.Errorf("unknown kind: %d %s", w.Code, w.Body)
	}

	d := ldaptest.New(t, ldaptest.Options{LDAPS: true})
	d.Add(ldaptest.Entry{DN: "DC=acme,DC=cl", Attrs: map[string][]string{"objectClass": {"domain"}}}, "")
	d.Add(ldaptest.Entry{DN: "CN=svc,DC=acme,DC=cl", Attrs: map[string][]string{"objectClass": {"user"}}}, "clave-svc")
	d.Add(ldaptest.Entry{DN: "CN=Ana,DC=acme,DC=cl", Attrs: map[string][]string{"objectClass": {"user"}, "objectCategory": {"person"},
		"sAMAccountName": {"ana"}, "mail": {"ana@acme.cl"}, "displayName": {"Ana"}, "userAccountControl": {"512"}}}, "")
	cfg := `{"urls":["` + d.URL + `"],"ca_file":` + mustJSON(d.CAFile(t)) + `,"bind_dn":"CN=svc,DC=acme,DC=cl","bind_password":"clave-svc","user_base_dn":"DC=acme,DC=cl"`

	// Activar con una contraseña equivocada no se guarda; el informe dice por qué.
	w = f.do("super", "PUT", "/api/admin/ldap/config", strings.Replace(cfg, "clave-svc", "otra", 1)+`,"enabled":true}`)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), `"code":"ldap_test_failed"`) ||
		!strings.Contains(w.Body.String(), "bind_invalid_credentials") {
		t.Errorf("failing save: %d %s", w.Code, w.Body)
	}
	w = f.do("super", "PUT", "/api/admin/ldap/config", cfg+`,"enabled":true}`)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "clave-svc") || !strings.Contains(w.Body.String(), `"bind_password":"••••••"`) {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	w = f.do("super", "POST", "/api/admin/ldap/test", `{"kind":"connection","config":{}}`)
	var report struct {
		OK      bool `json:"ok"`
		Servers []struct {
			Users int `json:"users"`
		} `json:"servers"`
	}
	if json.Unmarshal(w.Body.Bytes(), &report); w.Code != http.StatusOK || !report.OK || report.Servers[0].Users != 1 {
		t.Errorf("test: %d %s", w.Code, w.Body)
	}
	w = f.do("super", "POST", "/api/admin/ldap/test", `{"kind":"user","login":"ana","config":{}}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status":"found"`) || !strings.Contains(w.Body.String(), `"email":"ana@acme.cl"`) {
		t.Errorf("test user: %d %s", w.Code, w.Body)
	}
	// Otro destino sin volver a escribir la contraseña.
	w = f.do("super", "POST", "/api/admin/ldap/test", `{"kind":"connection","config":{"urls":["ldaps://otro.acme.cl"]}}`)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), `"code":"ldap_password_required","field":"bind_password"`) {
		t.Errorf("new destination: %d %s", w.Code, w.Body)
	}
	// Un directorio que no responde: 502 con lo que pasó.
	w = f.do("super", "POST", "/api/admin/ldap/test", `{"kind":"user","login":"ana","config":{"urls":["ldaps://localhost:1"],"bind_password":"clave-svc"}}`)
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), `"code":"ldap_unavailable"`) || !strings.Contains(w.Body.String(), `"detail":`) {
		t.Errorf("unavailable: %d %s", w.Code, w.Body)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
