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

// §8.3.1: las reglas grupo → (área, rol) son del superadmin; cambiarlas pide reautenticarse.
func TestLDAPMappings(t *testing.T) {
	f := newFixture(t)
	routes := [][3]string{
		{"GET", "/api/admin/ldap/groups?q=wiki", ""}, {"GET", "/api/admin/ldap/mappings", ""},
		{"POST", "/api/admin/ldap/mappings/preview", `{}`}, {"POST", "/api/admin/ldap/mappings", `{}`},
		{"PATCH", "/api/admin/ldap/mappings/x", `{"role":"viewer"}`}, {"DELETE", "/api/admin/ldap/mappings/x", ""},
	}
	for _, who := range []string{"adminTI", "editorTI", "viewerTI", "outsider", ""} {
		for _, req := range routes {
			want := http.StatusNotFound
			if who == "" {
				want = http.StatusUnauthorized
			}
			if w := f.do(who, req[0], req[1], req[2]); w.Code != want {
				t.Errorf("%s %s %s: %d, want %d", who, req[0], req[1], w.Code, want)
			}
		}
	}
	if w := f.do("super", "GET", "/api/admin/ldap/mappings", ""); w.Code != http.StatusOK || w.Body.String() != "[]\n" {
		t.Errorf("empty list: %d %q", w.Code, w.Body)
	}
	if w := f.do("super", "GET", "/api/admin/ldap/groups?q=wiki", ""); w.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(w.Body.String(), `"code":"ldap_incomplete"`) {
		t.Errorf("groups without a connection: %d %s", w.Code, w.Body)
	}

	d := ldaptest.New(t, ldaptest.Options{LDAPS: true})
	for _, dn := range []string{"DC=acme,DC=cl", "OU=Grupos,DC=acme,DC=cl"} {
		d.Add(ldaptest.Entry{DN: dn, Attrs: map[string][]string{"objectClass": {"container"}}}, "")
	}
	d.Add(ldaptest.Entry{DN: "CN=svc,DC=acme,DC=cl", Attrs: map[string][]string{"objectClass": {"user"}}}, "clave-svc")
	d.Add(ldaptest.Entry{DN: "CN=GG-Wiki-TI,OU=Grupos,DC=acme,DC=cl", Attrs: map[string][]string{"objectClass": {"group"},
		"cn": {"GG-Wiki-TI"}, "objectGUID": {"0123456789abcdef"}, "objectSid": {"\x01\x01\x00\x00\x00\x00\x00\x05\x20\x02\x00\x00"}}}, "")
	d.Add(ldaptest.Entry{DN: "CN=Ana,DC=acme,DC=cl", Attrs: map[string][]string{"objectClass": {"user"}, "objectCategory": {"person"},
		"sAMAccountName": {"ana"}, "userAccountControl": {"512"}, "memberOf": {"CN=GG-Wiki-TI,OU=Grupos,DC=acme,DC=cl"}}}, "")
	for _, req := range routes[3:] {
		if w := f.do("super", req[0], req[1], req[2]); w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "reauth_required") {
			t.Errorf("%s %s without reauth: %d %s", req[0], req[1], w.Code, w.Body)
		}
	}
	f.reauth(t, "super")
	if w := f.do("super", "PUT", "/api/admin/ldap/config", `{"urls":["`+d.URL+`"],"ca_file":`+mustJSON(d.CAFile(t))+
		`,"bind_dn":"CN=svc,DC=acme,DC=cl","bind_password":"clave-svc","user_base_dn":"DC=acme,DC=cl","group_base_dn":"OU=Grupos,DC=acme,DC=cl"}`); w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}

	w := f.do("super", "GET", "/api/admin/ldap/groups?q=wiki", "")
	var found struct {
		Groups []struct {
			GUID string `json:"guid"`
			SID  string `json:"sid"`
		} `json:"groups"`
	}
	if json.Unmarshal(w.Body.Bytes(), &found); w.Code != http.StatusOK || len(found.Groups) != 1 || found.Groups[0].SID != "S-1-5-544" {
		t.Fatalf("groups: %d %s", w.Code, w.Body)
	}
	rule := `{"group_guid":"` + found.Groups[0].GUID + `","workspace_id":"` + f.wsID(t, "ti") + `","role":"editor"}`
	if w := f.do("super", "POST", "/api/admin/ldap/mappings/preview", rule); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"members":1`) {
		t.Errorf("preview: %d %s", w.Code, w.Body)
	}
	if w := f.do("super", "POST", "/api/admin/ldap/mappings", strings.Replace(rule, found.Groups[0].GUID, "00000000-0000-0000-0000-000000000000", 1)); w.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(w.Body.String(), `"code":"ldap_group_not_found","field":"group_guid"`) {
		t.Errorf("unknown group: %d %s", w.Code, w.Body)
	}
	w = f.do("super", "POST", "/api/admin/ldap/mappings", rule)
	var created struct {
		ID        string `json:"id"`
		GroupName string `json:"group_name"`
	}
	if json.Unmarshal(w.Body.Bytes(), &created); w.Code != http.StatusCreated || created.GroupName != "GG-Wiki-TI" {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if w := f.do("super", "POST", "/api/admin/ldap/mappings", rule); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "ldap_mapping_exists") {
		t.Errorf("duplicate: %d %s", w.Code, w.Body)
	}
	if w := f.do("super", "PATCH", "/api/admin/ldap/mappings/"+created.ID, `{"role":"admin"}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"role":"admin"`) {
		t.Errorf("update: %d %s", w.Code, w.Body)
	}
	if w := f.do("super", "DELETE", "/api/admin/ldap/mappings/"+created.ID, ""); w.Code != http.StatusNoContent {
		t.Errorf("delete: %d %s", w.Code, w.Body)
	}
	if w := f.do("super", "DELETE", "/api/admin/ldap/mappings/"+created.ID, ""); w.Code != http.StatusNotFound {
		t.Errorf("delete twice: %d %s", w.Code, w.Body)
	}
}

// §8.6.2, §8.6.3: el login con el directorio da la cuenta y las áreas por los grupos; el
// nombre y la contraseña vienen del directorio, y lo que da un grupo no se cambia en el área.
func TestLDAPLogin(t *testing.T) {
	f := newFixture(t)
	adDirectory(t, f, "lucia", "tomas")

	for login, want := range map[string]struct {
		password string
		status   int
		code     string
	}{
		"lucia": {"otra", http.StatusUnauthorized, "invalid_credentials"},
		"tomas": {"clave-tomas", http.StatusUnauthorized, "invalid_credentials"}, // deshabilitada
		"dani":  {"clave-dani", http.StatusForbidden, "no_directory_access"},
	} {
		if w := f.do("", "POST", "/api/auth/login", `{"email":"`+login+`","password":"`+want.password+`"}`); w.Code != want.status || !strings.Contains(w.Body.String(), want.code) {
			t.Errorf("%s: %d %s", login, w.Code, w.Body)
		}
	}
	w := f.do("", "POST", "/api/auth/login", `{"email":"lucia","password":"clave-lucia"}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	f.cookies["lucia"] = w.Result().Cookies()[0]
	w = f.do("lucia", "GET", "/api/auth/me", "")
	var me struct {
		ID         string `json:"id"`
		AuthSource string `json:"auth_source"`
		Workspaces []struct {
			Slug, Role string
		} `json:"workspaces"`
	}
	if json.Unmarshal(w.Body.Bytes(), &me); me.AuthSource != "ldap" || len(me.Workspaces) != 1 || me.Workspaces[0].Slug != "ti" || me.Workspaces[0].Role != "editor" {
		t.Fatalf("me: %s", w.Body)
	}

	// El nombre y la contraseña son los del directorio; la reautenticación es un bind.
	if w := f.do("lucia", "PATCH", "/api/me", `{"full_name":"Otra"}`); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "directory_managed") {
		t.Errorf("rename: %d %s", w.Code, w.Body)
	}
	if w := f.do("lucia", "POST", "/api/me/password", `{"current_password":"clave-lucia","new_password":"una-clave-nueva-larga"}`); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "directory_managed") {
		t.Errorf("change password: %d %s", w.Code, w.Body)
	}
	if w := f.do("lucia", "POST", "/api/auth/reauth", `{"password":"otra"}`); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("reauth with a wrong password: %d %s", w.Code, w.Body)
	}
	if w := f.do("lucia", "POST", "/api/auth/reauth", `{"password":"clave-lucia"}`); w.Code != http.StatusNoContent {
		t.Errorf("reauth: %d %s", w.Code, w.Body)
	}

	// Lo que da un grupo no se cambia en el área: se cambia el grupo en el directorio.
	f.reauth(t, "adminTI")
	if w := f.do("adminTI", "POST", "/api/w/ti/members", `{"email":"lucia@acme.cl","role":"viewer"}`); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "managed_by_directory") {
		t.Errorf("area admin changes the role: %d %s", w.Code, w.Body)
	}
	if w := f.do("adminTI", "DELETE", "/api/w/ti/members/"+me.ID, ""); w.Code != http.StatusConflict {
		t.Errorf("area admin removes: %d %s", w.Code, w.Body)
	}
}
