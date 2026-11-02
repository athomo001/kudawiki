package httpapi

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/athomo001/kudawiki/internal/ldap/ldaptest"
)

const (
	adGroups, adPeople = "OU=Grupos,DC=acme,DC=cl", "OU=Personas,DC=acme,DC=cl"
	adGroup            = "CN=GG-Wiki-TI," + adGroups
	adSID              = "\x01\x01\x00\x00\x00\x00\x00\x05\x20\x02\x00\x00"
)

// adDirectory arma un AD de prueba con el grupo GG-Wiki-TI y cuatro personas: lucia, dani,
// marta y tomas (deshabilitado); inGroup dice quiénes están en el grupo. Lo deja activo, con la
// regla "GG-Wiki-TI → editor en TI" y el superadmin reautenticado.
func adDirectory(t *testing.T, f fixture, inGroup ...string) *ldaptest.Server {
	t.Helper()
	d := ldaptest.New(t, ldaptest.Options{LDAPS: true})
	d.RootDSE = map[string][]string{"supportedCapabilities": {"1.2.840.113556.1.4.800"}}
	for _, dn := range []string{"DC=acme,DC=cl", adGroups, adPeople} {
		d.Add(ldaptest.Entry{DN: dn, Attrs: map[string][]string{"objectClass": {"container"}}}, "")
	}
	d.Add(ldaptest.Entry{DN: "CN=svc,DC=acme,DC=cl", Attrs: map[string][]string{"objectClass": {"user"}}}, "clave-svc")
	d.Add(ldaptest.Entry{DN: adGroup, Attrs: map[string][]string{"objectClass": {"group"}, "cn": {"GG-Wiki-TI"},
		"objectGUID": {"0123456789abcdef"}, "objectSid": {adSID}}}, "")
	d.Operational = []string{"tokenGroups"}
	for _, p := range []struct{ login, uac string }{{"lucia", "512"}, {"dani", "512"}, {"marta", "512"}, {"tomas", "514"}} {
		adPerson(d, p.login, p.uac, slices.Contains(inGroup, p.login))
	}
	f.reauth(t, "super")
	if w := f.do("super", "PUT", "/api/admin/ldap/config", `{"enabled":true,"urls":["`+d.URL+`"],"ca_file":`+mustJSON(d.CAFile(t))+
		`,"bind_dn":"CN=svc,DC=acme,DC=cl","bind_password":"clave-svc","user_base_dn":"`+adPeople+`","group_base_dn":"`+adGroups+`"}`); w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	w := f.do("super", "GET", "/api/admin/ldap/groups?q=wiki", "")
	var found struct {
		Groups []struct {
			GUID string `json:"guid"`
		} `json:"groups"`
	}
	if json.Unmarshal(w.Body.Bytes(), &found); len(found.Groups) != 1 {
		t.Fatalf("groups: %d %s", w.Code, w.Body)
	}
	if w := f.do("super", "POST", "/api/admin/ldap/mappings", `{"group_guid":"`+found.Groups[0].GUID+`","workspace_id":"`+f.wsID(t, "ti")+`","role":"editor"}`); w.Code != http.StatusCreated {
		t.Fatalf("mapping: %d %s", w.Code, w.Body)
	}
	return d
}

// adPerson agrega a una persona. En el grupo, como en AD: tokenGroups para el login y
// memberOf para listar los miembros del grupo.
func adPerson(d *ldaptest.Server, login, uac string, inGroup bool) {
	attrs := map[string][]string{"objectClass": {"user"}, "objectCategory": {"person"},
		"sAMAccountName": {login}, "mail": {login + "@acme.cl"}, "displayName": {strings.ToUpper(login[:1]) + login[1:]},
		"userAccountControl": {uac}, "objectGUID": {login + strings.Repeat("-", 16-len(login))}}
	if inGroup {
		attrs["tokenGroups"], attrs["memberOf"] = []string{adSID}, []string{adGroup}
	}
	d.Add(ldaptest.Entry{DN: "CN=" + login + "," + adPeople, Attrs: attrs}, "clave-"+login)
}

type memberRow struct {
	UserID     string `json:"user_id"`
	Role       string `json:"role"`
	ManualRole string `json:"manual_role"`
	LDAPRole   string `json:"ldap_role"`
	Group      string `json:"ldap_group"`
}

func (f fixture) member(t *testing.T, ws, userID string) memberRow {
	t.Helper()
	w := f.do("super", "GET", "/api/admin/workspaces/"+f.wsID(t, ws)+"/members/", "")
	var rows []memberRow
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("members: %d %s", w.Code, w.Body)
	}
	for _, m := range rows {
		if m.UserID == userID {
			return m
		}
	}
	return memberRow{}
}

// §8.6.3, §8.3.2: vincular una cuenta local la pasa al directorio sin perder lo suyo; la
// etiqueta "Gestionado por AD" sale de ldap_role y ldap_group, y desanclar es del superadmin.
func TestLDAPLinkAndDetach(t *testing.T) {
	f := newFixture(t)
	adDirectory(t, f, "lucia")
	id := f.users["viewerTI"]
	link := func(who, userID, login string) *http.Response {
		return f.do(who, "POST", "/api/admin/users/"+userID+"/link-ldap", `{"login":"`+login+`"}`).Result()
	}

	if r := link("adminTI", id, "lucia"); r.StatusCode != http.StatusNotFound {
		t.Errorf("area admin links: %d", r.StatusCode)
	}
	for _, c := range []struct {
		user, login, code string
		status            int
	}{
		{f.users["editorTI"], "dani", "ldap_no_mapped_group", http.StatusUnprocessableEntity},
		{f.users["editorTI"], "nadie", "ldap_user_not_found", http.StatusUnprocessableEntity},
		{f.users["super"], "lucia", "last_local_superadmin", http.StatusConflict},
	} {
		if w := f.do("super", "POST", "/api/admin/users/"+c.user+"/link-ldap", `{"login":"`+c.login+`"}`); w.Code != c.status || !strings.Contains(w.Body.String(), c.code) {
			t.Errorf("link %s: %d %s", c.login, w.Code, w.Body)
		}
	}
	w := f.do("super", "POST", "/api/admin/users/"+id+"/link-ldap", `{"login":"lucia"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"email":"lucia@acme.cl"`) {
		t.Fatalf("link: %d %s", w.Code, w.Body)
	}
	// Se cerraron sus sesiones y la contraseña local ya no sirve: entra con la del directorio.
	if w := f.do("viewerTI", "GET", "/api/auth/me", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("old session: %d", w.Code)
	}
	if w := f.do("", "POST", "/api/auth/login", `{"email":"lucia@acme.cl","password":"`+password+`"}`); w.Code != http.StatusUnauthorized {
		t.Errorf("local password: %d %s", w.Code, w.Body)
	}
	if w := f.do("", "POST", "/api/auth/login", `{"email":"lucia","password":"clave-lucia"}`); w.Code != http.StatusNoContent {
		t.Errorf("directory login: %d %s", w.Code, w.Body)
	}
	if w := f.do("super", "POST", "/api/admin/users/"+id+"/link-ldap", `{"login":"lucia"}`); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "ldap_already_linked") {
		t.Errorf("link twice: %d %s", w.Code, w.Body)
	}

	// Era lectora a mano; el grupo la hace editora: vale el más alto.
	if m := f.member(t, "ti", id); m.Role != "editor" || m.ManualRole != "viewer" || m.LDAPRole != "editor" || m.Group != "GG-Wiki-TI" {
		t.Errorf("member after link: %+v", m)
	}
	// ¿Quién puede ver esto? dice por qué: su rol viene del grupo (§3.4).
	f.expect(t, "adminTI", "PUT", "/api/w/ti/pages/guia", `{"title":"Guía","markdown":"x","base_revision":0}`, http.StatusNoContent)
	byGroup := func() string {
		var acc struct {
			Members []struct{ Email, Group string } `json:"members"`
		}
		json.Unmarshal(f.do("adminTI", "GET", "/api/w/ti/access/guia", "").Body.Bytes(), &acc)
		for _, m := range acc.Members {
			if m.Email == "lucia@acme.cl" {
				return m.Group
			}
		}
		return "missing"
	}
	if g := byGroup(); g != "GG-Wiki-TI" {
		t.Errorf("access after link: %q", g)
	}

	// Desanclar: solo el superadmin, con reautenticación; lo del grupo pasa a ser manual.
	if w := f.do("adminTI", "POST", "/api/w/ti/members/"+id+"/detach", ""); w.Code == http.StatusNoContent {
		t.Errorf("area admin detaches: %d", w.Code)
	}
	if w := f.do("super", "POST", "/api/admin/workspaces/"+f.wsID(t, "ti")+"/members/"+id+"/detach", ""); w.Code != http.StatusNoContent {
		t.Fatalf("detach: %d %s", w.Code, w.Body)
	}
	if m := f.member(t, "ti", id); m.Role != "editor" || m.ManualRole != "editor" || m.LDAPRole != "" {
		t.Errorf("member after detach: %+v", m)
	}
	if g := byGroup(); g != "" {
		t.Errorf("access after detach: %q", g)
	}
}

type syncReport struct {
	DryRun      bool    `json:"dry_run"`
	Aborted     string  `json:"aborted"`
	Created     int     `json:"created"`
	Added       int     `json:"added"`
	Removed     int     `json:"removed"`
	Managed     int     `json:"managed"`
	Ratio       float64 `json:"ratio"`
	Deactivated []struct {
		Email  string `json:"email"`
		Reason string `json:"reason"`
	} `json:"deactivated"`
}

type adminUser struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	AuthSource    string `json:"auth_source"`
	IsActive      bool   `json:"is_active"`
	OffboardingAt string `json:"offboarding_at"`
}

func (f fixture) adminUser(t *testing.T, email string) adminUser {
	t.Helper()
	w := f.do("super", "GET", "/api/admin/users", "")
	var users []adminUser
	if err := json.Unmarshal(w.Body.Bytes(), &users); err != nil {
		t.Fatalf("users: %d %s", w.Code, w.Body)
	}
	for _, u := range users {
		if u.Email == email {
			return u
		}
	}
	t.Fatalf("no user %s", email)
	return adminUser{}
}

func (f fixture) sync(t *testing.T, path, body string) syncReport {
	t.Helper()
	w := f.do("super", "POST", path, body)
	var r syncReport
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &r) != nil {
		t.Fatalf("%s: %d %s", path, w.Code, w.Body)
	}
	return r
}

// §8.3.3, §8.3.4, §8.6.5: la sincronización crea de antemano a quien está en un grupo con regla,
// da de baja (§8.4) a quien el directorio deshabilitó, y se detiene si quitaría demasiado salvo
// que el superadmin la fuerce. El estado se ve en el panel y en /readyz.
func TestLDAPSync(t *testing.T) {
	f := newFixture(t)
	d := adDirectory(t, f, "lucia", "marta")

	for _, who := range []string{"adminTI", "outsider"} {
		for _, req := range [][3]string{{"GET", "/api/admin/ldap/status", ""}, {"POST", "/api/admin/ldap/sync/preview", ""},
			{"POST", "/api/admin/ldap/sync", `{}`}, {"PUT", "/api/admin/ldap/debug", `{"on":true}`}} {
			if w := f.do(who, req[0], req[1], req[2]); w.Code != http.StatusNotFound {
				t.Errorf("%s %s %s: %d", who, req[0], req[1], w.Code)
			}
		}
	}

	// Ver qué haría no cambia nada.
	if r := f.sync(t, "/api/admin/ldap/sync/preview", ""); !r.DryRun || r.Created != 2 || r.Added != 2 || r.Aborted != "" {
		t.Errorf("preview: %+v", r)
	}
	if w := f.do("super", "GET", "/api/admin/users", ""); strings.Contains(w.Body.String(), "marta@acme.cl") {
		t.Fatal("preview created an account")
	}
	if r := f.sync(t, "/api/admin/ldap/sync", `{}`); r.DryRun || r.Created != 2 || r.Added != 2 {
		t.Errorf("sync: %+v", r)
	}
	lucia := f.adminUser(t, "lucia@acme.cl")
	if lucia.AuthSource != "ldap" || !lucia.IsActive || f.member(t, "ti", lucia.ID).LDAPRole != "editor" {
		t.Fatalf("precreated: %+v", lucia)
	}

	// El directorio deshabilita a lucia: quitaría 1 de 2 accesos, más que el 10 %.
	d.Set("CN=lucia,"+adPeople, "userAccountControl", "514")
	r := f.sync(t, "/api/admin/ldap/sync", `{}`)
	if r.Aborted != "removal_ratio" || r.Removed != 1 || r.Managed != 2 || len(r.Deactivated) != 1 || r.Deactivated[0].Reason != "disabled" {
		t.Fatalf("aborted: %+v", r)
	}
	if w := f.do("super", "GET", "/api/admin/ldap/status", ""); !strings.Contains(w.Body.String(), `"aborted":"removal_ratio"`) {
		t.Errorf("status after abort: %s", w.Body)
	}
	if u := f.adminUser(t, "lucia@acme.cl"); !u.IsActive || u.OffboardingAt != "" {
		t.Fatalf("an aborted sync deactivated someone: %+v", u)
	}

	// Forzada, aplica la baja de §8.4: desactiva ya y transfiere al terminar la gracia.
	if r := f.sync(t, "/api/admin/ldap/sync", `{"force":true}`); r.Aborted != "" || len(r.Deactivated) != 1 {
		t.Fatalf("forced: %+v", r)
	}
	if u := f.adminUser(t, "lucia@acme.cl"); u.IsActive || u.OffboardingAt == "" {
		t.Errorf("lucia after the forced sync: %+v", u)
	}
	if w := f.do("", "POST", "/api/auth/login", `{"email":"lucia","password":"clave-lucia"}`); w.Code != http.StatusUnauthorized {
		t.Errorf("deactivated login: %d %s", w.Code, w.Body)
	}
	if n := f.count(t, `SELECT count(*) FROM audit_log WHERE action = 'user.deactivated_by_ldap' AND target_id = ?`, lucia.ID); n != 1 {
		t.Errorf("audit: %d", n)
	}
	// Una baja en curso no se repite en la sincronización siguiente.
	if r := f.sync(t, "/api/admin/ldap/sync", `{}`); r.Aborted != "" || len(r.Deactivated) != 0 {
		t.Errorf("next sync: %+v", r)
	}

	// El log de depuración se apaga solo; el estado lo muestra en el panel, no en /readyz.
	if w := f.do("super", "PUT", "/api/admin/ldap/debug", `{"on":true}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"until"`) {
		t.Errorf("debug: %d %s", w.Code, w.Body)
	}
	if w := f.do("super", "GET", "/api/admin/ldap/status", ""); !strings.Contains(w.Body.String(), `"debug_until"`) ||
		!strings.Contains(w.Body.String(), `"cert_days"`) || !strings.Contains(w.Body.String(), `"last_sync":{`) {
		t.Errorf("status: %s", w.Body)
	}
	w := f.do("", "GET", "/readyz", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ldap":{"enabled":true`) || strings.Contains(w.Body.String(), "debug_until") {
		t.Errorf("readyz: %d %s", w.Code, w.Body)
	}
}
