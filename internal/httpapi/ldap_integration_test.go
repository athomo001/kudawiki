//go:build ldap_integration

package httpapi

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	goldap "github.com/go-ldap/ldap/v3"
)

// Criterio de salida de F6 (SPEC §8.6, §17): la conexión, el login, los grupos anidados, las
// reglas, la sincronización y la baja desde el directorio, contra directorios de verdad. Corre
// dentro de la red de test/ldap/docker-compose.test.yml (test/ldap/run.sh), donde cada servidor
// tiene el nombre de su certificado.

// itDirectory es uno de los directorios del compose y cómo se le cambia el estado a lucia.
type itDirectory struct {
	name, flavor, url, ca       string
	bindDN, userBase, groupBase string
	adminDN                     string
	disabledFilter              string // vacío: el de su tipo de servidor
	group                       string // el grupo con regla, tal como lo nombra el directorio
	suggest                     string // el tipo que el Root DSE sugiere; vacío si no se mira
	identity                    string // la identidad que sugiere con él (§8.6.4)
	// precreated son las cuentas que la sincronización crea de antemano: marta, y en AD también
	// pablo, que está habilitado aunque tenga que cambiar la contraseña.
	precreated int
	// disable y enable deshabilitan y habilitan a lucia como lo haría quien administra el directorio.
	disable, enable []goldap.Change
}

func itDirectories(certs string) []itDirectory {
	const base = "dc=kudawiki,dc=test"
	locked := func(on bool) []goldap.Change {
		if on {
			return []goldap.Change{{Operation: goldap.ReplaceAttribute, Modification: goldap.PartialAttribute{Type: "nsAccountLock", Vals: []string{"TRUE"}}}}
		}
		return []goldap.Change{{Operation: goldap.ReplaceAttribute, Modification: goldap.PartialAttribute{Type: "nsAccountLock", Vals: []string{"FALSE"}}}}
	}
	inactive := func(on bool) []goldap.Change {
		if on {
			return []goldap.Change{{Operation: goldap.ReplaceAttribute, Modification: goldap.PartialAttribute{Type: "employeeType", Vals: []string{"inactive"}}}}
		}
		return []goldap.Change{{Operation: goldap.ReplaceAttribute, Modification: goldap.PartialAttribute{Type: "employeeType", Vals: []string{}}}}
	}
	uac := func(v string) []goldap.Change {
		return []goldap.Change{{Operation: goldap.ReplaceAttribute, Modification: goldap.PartialAttribute{Type: "userAccountControl", Vals: []string{v}}}}
	}
	openldap := func(name, flavor, suggest, identity string) itDirectory {
		return itDirectory{name: name, flavor: flavor, url: "ldaps://" + name + ".kudawiki.test", ca: filepath.Join(certs, "ca.crt"),
			bindDN: "cn=svc-kudawiki," + base, userBase: "ou=Personas," + base, groupBase: "ou=Grupos," + base, adminDN: "cn=admin," + base,
			disabledFilter: "(employeeType=inactive)", group: "GG-Wiki-TI", suggest: suggest, identity: identity, precreated: 1, disable: inactive(true), enable: inactive(false)}
	}
	return []itDirectory{
		{name: "samba", flavor: "ad", url: "ldaps://samba.kudawiki.test", ca: filepath.Join(certs, "ca.crt"),
			bindDN: "CN=Servicio Kudawiki,OU=Servicios,DC=kudawiki,DC=test", userBase: "OU=Personas,DC=kudawiki,DC=test",
			groupBase: "OU=Grupos,DC=kudawiki,DC=test", adminDN: "CN=Administrator,CN=Users,DC=kudawiki,DC=test",
			group: "GG-Wiki-TI", suggest: "ad", identity: "objectGUID", precreated: 2, disable: uac("514"), enable: uac("512")},
		openldap("openldap", "openldap", "openldap", "entryUUID"),
		openldap("openldap-plain", "generic", "", ""),
		{name: "dirsrv", flavor: "openldap", url: "ldaps://dirsrv.kudawiki.test:3636", ca: filepath.Join(certs, "ca.crt"),
			bindDN: "cn=svc-kudawiki," + base, userBase: "ou=Personas," + base, groupBase: "ou=Grupos," + base, adminDN: "cn=Directory Manager",
			group: "GG-Wiki-TI", suggest: "openldap", identity: "nsUniqueId", precreated: 1, disable: locked(true), enable: locked(false)},
		{name: "freeipa", flavor: "openldap", url: "ldaps://ipa.kudawiki.test", ca: filepath.Join(certs, "freeipa-ca.crt"),
			bindDN: "uid=svc-kudawiki,cn=sysaccounts,cn=etc," + base, userBase: "cn=users,cn=accounts," + base,
			groupBase: "cn=groups,cn=accounts," + base, adminDN: "cn=Directory Manager",
			group: "gg-wiki-ti", suggest: "openldap", identity: "ipaUniqueID", precreated: 1, disable: locked(true), enable: locked(false)},
	}
}

func TestLDAPIntegration(t *testing.T) {
	certs := os.Getenv("KUDAWIKI_IT_CERTS")
	if certs == "" {
		t.Skip("run with test/ldap/run.sh")
	}
	for _, d := range itDirectories(certs) {
		t.Run(d.name, func(t *testing.T) { itDirectoryFlow(t, d, certs) })
	}
}

// itAdmin conecta como quien administra el directorio, para cambiarle el estado a lucia.
func itAdmin(t *testing.T, d itDirectory) (*goldap.Conn, string) {
	t.Helper()
	pem, err := os.ReadFile(d.ca)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pem)
	conn, err := goldap.DialURL(d.url, goldap.DialWithTLSConfig(&tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.Bind(d.adminDN, os.Getenv("ADMIN_PASSWORD")); err != nil {
		t.Fatal(err)
	}
	res, err := conn.Search(goldap.NewSearchRequest(d.userBase, goldap.ScopeWholeSubtree, goldap.NeverDerefAliases, 2, 10, false,
		"(|(uid=lucia)(sAMAccountName=lucia))", []string{"dn"}, nil))
	if err != nil || len(res.Entries) != 1 {
		t.Fatalf("lucia in %s: %v", d.name, err)
	}
	return conn, res.Entries[0].DN
}

func itDirectoryFlow(t *testing.T, d itDirectory, certs string) {
	f := newFixture(t)
	admin, luciaDN := itAdmin(t, d)
	modify := func(changes []goldap.Change) {
		t.Helper()
		if err := admin.Modify(&goldap.ModifyRequest{DN: luciaDN, Changes: changes}); err != nil {
			t.Fatal(err)
		}
	}
	// Una corrida anterior que se cortó pudo dejarla deshabilitada.
	modify(d.enable)
	t.Cleanup(func() { modify(d.enable) })

	cfg := map[string]any{"enabled": true, "flavor": d.flavor, "urls": []string{d.url}, "ca_file": d.ca, "bind_dn": d.bindDN,
		"bind_password": os.Getenv("USER_PASSWORD"), "user_base_dn": d.userBase, "group_base_dn": d.groupBase,
		// El directorio de prueba es chico: una baja es la mitad de los accesos.
		"sync_max_removal_ratio": 1}
	if d.disabledFilter != "" {
		cfg["disabled_filter"] = d.disabledFilter
	}
	f.reauth(t, "super")

	// Con otra CA, no se guarda: el diagnóstico dice que no conoce la CA (§17).
	wrong := maps.Clone(cfg)
	wrong["ca_file"] = filepath.Join(certs, "other-ca.crt")
	if w := f.do("super", "PUT", "/api/admin/ldap/config", mustJSON(wrong)); w.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(w.Body.String(), "tls_unknown_ca") {
		t.Errorf("unknown CA: %d %s", w.Code, w.Body)
	}
	if w := f.do("super", "PUT", "/api/admin/ldap/config", mustJSON(cfg)); w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	w := f.do("super", "POST", "/api/admin/ldap/test", `{"kind":"connection","config":{}}`)
	var conn struct {
		OK         bool `json:"ok"`
		Suggestion *struct {
			Flavor string `json:"flavor"`
			GUID   string `json:"guid"`
		} `json:"suggestion"`
	}
	if json.Unmarshal(w.Body.Bytes(), &conn); !conn.OK || (d.suggest != "" && (conn.Suggestion == nil ||
		conn.Suggestion.Flavor != d.suggest || conn.Suggestion.GUID != d.identity)) {
		t.Fatalf("test connection: %s", w.Body)
	}
	// Usar estos valores: el tipo y la identidad que sugiere el Root DSE. Con la de su tipo
	// (entryUUID), FreeIPA no le deja leer la identidad a la cuenta de servicio.
	if d.suggest != "" {
		cfg["flavor"], cfg["attrs"] = conn.Suggestion.Flavor, map[string]string{"guid": conn.Suggestion.GUID}
		if w := f.do("super", "PUT", "/api/admin/ldap/config", mustJSON(cfg)); w.Code != http.StatusOK {
			t.Fatalf("use the suggestion: %d %s", w.Code, w.Body)
		}
	}

	// La regla: el grupo, que tiene a TI-Redes adentro, da editor en TI.
	w = f.do("super", "GET", "/api/admin/ldap/groups?q=wiki", "")
	var groups struct {
		Groups []struct{ GUID, Name string } `json:"groups"`
	}
	json.Unmarshal(w.Body.Bytes(), &groups)
	i := slices.IndexFunc(groups.Groups, func(g struct{ GUID, Name string }) bool { return g.Name == d.group })
	if i < 0 {
		t.Fatalf("groups: %s", w.Body)
	}
	if w := f.do("super", "POST", "/api/admin/ldap/mappings", `{"group_guid":"`+groups.Groups[i].GUID+`","workspace_id":"`+f.wsID(t, "ti")+`","role":"editor"}`); w.Code != http.StatusCreated {
		t.Fatalf("mapping: %d %s", w.Code, w.Body)
	}
	w = f.do("super", "POST", "/api/admin/ldap/test", `{"kind":"user","login":"lucia","config":{}}`)
	if !strings.Contains(w.Body.String(), `"status":"found"`) || !strings.Contains(w.Body.String(), `"role":"editor"`) {
		t.Errorf("test user: %s", w.Body)
	}

	// El login: lucia entra por el grupo anidado; tomas está deshabilitado y dani no tiene grupo.
	login := func(who string) *http.Response {
		return f.do("", "POST", "/api/auth/login", `{"email":"`+who+`","password":"`+os.Getenv("USER_PASSWORD")+`"}`).Result()
	}
	r := login("lucia")
	if r.StatusCode != http.StatusNoContent {
		t.Fatalf("lucia login: %d", r.StatusCode)
	}
	f.cookies["lucia"] = r.Cookies()[0]
	if w := f.do("lucia", "GET", "/api/auth/me", ""); !strings.Contains(w.Body.String(), `"slug":"ti"`) || !strings.Contains(w.Body.String(), `"role":"editor"`) {
		t.Errorf("me: %s", w.Body)
	}
	if r := login("tomas"); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("tomas (disabled): %d", r.StatusCode)
	}
	if r := login("dani"); r.StatusCode != http.StatusForbidden {
		t.Errorf("dani (no group): %d", r.StatusCode)
	}
	if d.flavor == "ad" {
		if w := f.do("", "POST", "/api/auth/login", `{"email":"pablo","password":"`+os.Getenv("USER_PASSWORD")+`"}`); w.Code != http.StatusUnauthorized ||
			!strings.Contains(w.Body.String(), "password_expired") {
			t.Errorf("pablo (must change): %d %s", w.Code, w.Body)
		}
	}

	// La sincronización crea de antemano a quien está en el grupo y todavía no entró.
	if r := f.sync(t, "/api/admin/ldap/sync/preview", ""); r.Created != d.precreated || r.Aborted != "" {
		t.Errorf("preview: %+v", r)
	}
	if r := f.sync(t, "/api/admin/ldap/sync", `{}`); r.Created != d.precreated || r.Aborted != "" {
		t.Fatalf("sync: %+v", r)
	}
	marta := f.adminUser(t, "marta@kudawiki.test")
	if marta.AuthSource != "ldap" || f.member(t, "ti", marta.ID).LDAPRole != "editor" {
		t.Errorf("marta: %+v", marta)
	}

	// La baja: lucia escribió una página; el directorio la deshabilita, la sincronización inicia la
	// baja de §8.4 y, al vencer la gracia, la página pasa al admin del área.
	f.expect(t, "lucia", "PUT", "/api/w/ti/pages/manual", `{"title":"Manual","markdown":"de lucia","base_revision":0}`, http.StatusNoContent)
	lucia := f.adminUser(t, "lucia@kudawiki.test")
	modify(d.disable)
	rep := f.sync(t, "/api/admin/ldap/sync", `{}`)
	if rep.Aborted != "" || len(rep.Deactivated) != 1 || rep.Deactivated[0].Email != "lucia@kudawiki.test" || rep.Deactivated[0].Reason != "disabled" {
		t.Fatalf("deprovision: %+v", rep)
	}
	if r := login("lucia"); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("lucia after the deprovision: %d", r.StatusCode)
	}
	if w := f.do("lucia", "GET", "/api/auth/me", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("lucia's session after the deprovision: %d", w.Code)
	}
	f.runOffboarding(t, 31*24*time.Hour)
	if n := f.count(t, `SELECT COUNT(*) FROM pages WHERE slug = 'manual' AND owner_id = ?`, f.users["adminTI"]); n != 1 {
		t.Errorf("the page was not transferred to the area admin")
	}
	if n := f.count(t, `SELECT COUNT(*) FROM users WHERE id = ? AND offboarded_at IS NOT NULL`, lucia.ID); n != 1 {
		t.Errorf("the offboarding did not complete")
	}
}
