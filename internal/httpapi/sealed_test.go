package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// TestSealed recorre los sellados de §6 con personas reales: sellar, quién sabe que existe (el
// 404 uniforme para el resto), abrir con la contraseña y con la llave maestra, guardar, la ruta
// reservada, la auditoría del área, el borrado definitivo, y que el texto nunca quede en claro.
func TestSealed(t *testing.T) {
	f := newFixture(t)
	const pass = "una clave bien larga"
	create := func(slug string, code int) string {
		t.Helper()
		var out struct{ ID string }
		json.Unmarshal([]byte(f.expect(t, "editorTI", "POST", "/api/w/ti/sealed",
			`{"slug":"`+slug+`","title":"kwsellado","markdown":"kwsellado: la clave del router","password":"`+pass+`"}`, code)), &out)
		return out.ID
	}
	create("../fuera", http.StatusUnprocessableEntity)
	f.expect(t, "editorTI", "POST", "/api/w/ti/sealed", `{"slug":"legal/contrato","title":"x","markdown":"x","password":"corta"}`, http.StatusUnprocessableEntity)
	f.expect(t, "viewerTI", "POST", "/api/w/ti/sealed", `{"slug":"legal/contrato","title":"x","markdown":"x","password":"`+pass+`"}`, http.StatusNotFound)
	id := create("legal/contrato", http.StatusCreated)
	doc := "/api/sealed/" + id

	// Quien lo crea lo deja abierto en su sesión; nada de eso se guarda en la cache HTTP.
	w := f.do("editorTI", "GET", doc, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "la clave del router") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("author view: %d %q %s", w.Code, w.Header().Get("Cache-Control"), w.Body)
	}

	// Saben que existe el autor, los admins del área y el superadmin; el resto, el 404 de siempre.
	for _, who := range []string{"viewerTI", "adminFin", "outsider"} {
		for _, req := range []struct{ method, path, body string }{
			{"GET", doc, ""}, {"POST", doc + "/unlock", `{"password":"` + pass + `"}`}, {"PUT", doc, `{"title":"x","markdown":"x","base_revision":1}`},
			{"GET", doc + "/history", ""}, {"DELETE", doc, `{"confirm":"BORRAR"}`},
		} {
			f.expect(t, who, req.method, req.path, req.body, http.StatusNotFound)
		}
	}
	if got := f.expect(t, "viewerTI", "GET", "/api/w/ti/sealed", "", http.StatusOK); strings.TrimSpace(got) != "[]" {
		t.Errorf("viewer tree: %s", got)
	}
	if got := f.expect(t, "adminTI", "GET", "/api/w/ti/sealed", "", http.StatusOK); !strings.Contains(got, `"slug":"legal/contrato"`) {
		t.Errorf("admin tree: %s", got)
	}

	// Otra sesión de la misma persona lo ve cerrado; también tras 15 minutos sin usarlo.
	own := f.cookies["editorTI"]
	f.cookies["editorTI"] = f.do("", "POST", "/api/auth/login", `{"email":"editorTI@empresa.cl","password":"`+password+`"}`).Result().Cookies()[0]
	if got := f.expect(t, "editorTI", "GET", doc, "", http.StatusOK); !strings.Contains(got, `"locked":true`) || strings.Contains(got, "kwsellado") {
		t.Errorf("other session: %s", got)
	}
	f.cookies["editorTI"] = own
	*f.clock = f.clock.Add(16 * time.Minute)
	f.expect(t, "editorTI", "PUT", doc, `{"title":"kwsellado","markdown":"v2","base_revision":1}`, http.StatusLocked)
	f.expect(t, "editorTI", "POST", doc+"/unlock", `{"password":"`+pass+`"}`, http.StatusNoContent)

	// Guardar: sin propuestas, con la revisión de base para no pisar a nadie.
	f.expect(t, "editorTI", "PUT", doc, `{"title":"kwsellado","markdown":"kwsellado v2","base_revision":1}`, http.StatusOK)
	f.expect(t, "editorTI", "PUT", doc, `{"title":"kwsellado","markdown":"v3","base_revision":1}`, http.StatusConflict)
	if got := f.expect(t, "editorTI", "GET", doc+"/history", "", http.StatusOK); strings.Count(got, `"revision"`) != 2 {
		t.Errorf("history: %s", got)
	}
	if got := f.expect(t, "editorTI", "GET", doc+"?revision=1", "", http.StatusOK); !strings.Contains(got, "la clave del router") {
		t.Errorf("old revision: %s", got)
	}

	// El admin del área lo ve cerrado. La contraseña tiene tope de intentos.
	if got := f.expect(t, "adminTI", "GET", doc, "", http.StatusOK); !strings.Contains(got, `"locked":true`) || strings.Contains(got, "kwsellado") {
		t.Errorf("admin locked view: %s", got)
	}
	for range 5 {
		f.expect(t, "adminTI", "POST", doc+"/unlock", `{"password":"no es esta"}`, http.StatusUnprocessableEntity)
	}
	if w := f.do("adminTI", "POST", doc+"/unlock", `{"password":"`+pass+`"}`); w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Errorf("rate limit: %d %v", w.Code, w.Header())
	}

	// Con la llave maestra: sesión reautenticada, solo lectura, y el autor recibe el aviso.
	f.expect(t, "editorTI", "POST", doc+"/unlock", `{"as_admin":true}`, http.StatusUnprocessableEntity) // no es admin
	f.expect(t, "adminTI", "POST", doc+"/unlock", `{"as_admin":true}`, http.StatusUnauthorized)
	f.reauth(t, "adminTI")
	f.expect(t, "adminTI", "POST", doc+"/unlock", `{"as_admin":true}`, http.StatusNoContent)
	if got := f.expect(t, "adminTI", "GET", doc, "", http.StatusOK); !strings.Contains(got, "kwsellado v2") || !strings.Contains(got, `"can_edit":false`) {
		t.Errorf("admin open view: %s", got)
	}
	f.expect(t, "adminTI", "PUT", doc, `{"title":"x","markdown":"x","base_revision":2}`, http.StatusConflict)
	if got := f.expect(t, "editorTI", "GET", "/api/me/notifications", "", http.StatusOK); !strings.Contains(got, `"kind":"sealed_admin_access"`) ||
		!strings.Contains(got, `"sealed":"`+id+`"`) || strings.Contains(got, "kwsellado") {
		t.Errorf("author notice: %s", got)
	}

	// El superadmin también lo abre. Su acceso no sale en la auditoría del área (§11.2).
	f.reauth(t, "super")
	f.expect(t, "super", "POST", doc+"/unlock", `{"as_admin":true}`, http.StatusNoContent)
	opened := func(who, path string) (by []string) {
		t.Helper()
		var out struct {
			Events []struct{ Actor struct{ Email string } }
		}
		json.Unmarshal([]byte(f.expect(t, who, "GET", path+"?action=sealed.admin_open", "", http.StatusOK)), &out)
		for _, e := range out.Events {
			by = append(by, e.Actor.Email)
		}
		return by
	}
	if by := opened("adminTI", "/api/w/ti/audit"); len(by) != 1 || by[0] != "adminTI@empresa.cl" {
		t.Errorf("area audit admin_open by %v, want only adminTI's own", by)
	}
	if by := opened("super", "/api/admin/audit"); len(by) != 2 {
		t.Errorf("superadmin audit admin_open by %v, want both", by)
	}

	// La ruta queda reservada: crear, mover o restaurar una página encima es una ruta ocupada más.
	f.expect(t, "adminTI", "PUT", "/api/w/ti/pages/legal/contrato", `{"title":"x","markdown":"x","base_revision":0}`, http.StatusConflict)
	f.expect(t, "adminTI", "PUT", "/api/w/ti/pages/borrador", `{"title":"x","markdown":"x","base_revision":0}`, http.StatusNoContent)
	f.expect(t, "adminTI", "POST", "/api/w/ti/move/borrador", `{"to":"legal/contrato"}`, http.StatusConflict)
	f.expect(t, "adminTI", "DELETE", "/api/w/ti/pages/borrador", "", http.StatusNoContent)
	create("borrador", http.StatusCreated)
	var trash []struct{ ID string }
	json.Unmarshal([]byte(f.expect(t, "adminTI", "GET", "/api/w/ti/trash", "", http.StatusOK)), &trash)
	f.expect(t, "adminTI", "POST", "/api/w/ti/trash/"+trash[0].ID+"/restore", "", http.StatusConflict)
	create("legal/contrato", http.StatusConflict)

	// Borrar para siempre pide escribir BORRAR, y avisa a los admins del área.
	f.expect(t, "editorTI", "DELETE", doc, `{"confirm":"borrar esto"}`, http.StatusUnprocessableEntity)
	f.expect(t, "editorTI", "DELETE", doc, `{"confirm":"BORRAR"}`, http.StatusNoContent)
	f.expect(t, "editorTI", "GET", doc, "", http.StatusNotFound)
	f.expect(t, "adminTI", "GET", doc, "", http.StatusNotFound)
	if got := f.expect(t, "adminTI", "GET", "/api/me/notifications", "", http.StatusOK); !strings.Contains(got, `"kind":"sealed_purged"`) {
		t.Errorf("purge notice: %s", got)
	}
	var left int
	f.db.Read.QueryRow(`SELECT (SELECT COUNT(*) FROM sealed_keys WHERE page_id = ?1) + (SELECT COUNT(*) FROM sealed_revisions WHERE page_id = ?1)`, id).Scan(&left)
	if left != 0 {
		t.Errorf("%d rows left after purge", left)
	}

	// El texto nunca queda en claro: ni en la BD, ni en su WAL, ni en los avisos o la auditoría.
	var file string
	f.db.Read.QueryRow(`SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&file)
	for _, p := range []string{file, file + "-wal"} {
		if b, err := os.ReadFile(p); err == nil && bytes.Contains(b, []byte("kwsellado")) {
			t.Errorf("plaintext found in %s", p)
		}
	}
}

// TestSealedOffboarding es lo de los sellados en la baja (§8.4): los del área pasan al admin
// receptor sin la contraseña de quien se fue, y él pone la suya al abrirlos; los del espacio
// personal se borran para siempre. Quitar a alguien de un área le cierra sus sellados de ahí.
func TestSealedOffboarding(t *testing.T) {
	f := newFixture(t)
	const pass = "una clave bien larga"
	create := func(ws, slug string) string {
		t.Helper()
		var out struct{ ID string }
		json.Unmarshal([]byte(f.expect(t, "editorTI", "POST", "/api/w/"+ws+"/sealed",
			`{"slug":"`+slug+`","title":"kwsellado","markdown":"kwsellado del área","password":"`+pass+`"}`, http.StatusCreated)), &out)
		return out.ID
	}
	area, personal := create("ti", "legal/contrato"), create(f.personalSlug(t, "editorTI"), "mio")

	plan := "/api/admin/users/" + f.users["editorTI"] + "/offboarding"
	f.reauth(t, "super")
	if got := f.expect(t, "super", "GET", plan, "", http.StatusOK); !strings.Contains(got, `"sealed":1`) || !strings.Contains(got, `"personal_sealed":1`) {
		t.Fatalf("plan: %s", got)
	}
	if got := f.expect(t, "super", "POST", plan, `{"now":true}`, http.StatusOK); !strings.Contains(got, `"sealed":1`) || !strings.Contains(got, `"personal_sealed":1`) {
		t.Fatalf("offboarding: %s", got)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM sealed_pages WHERE id = ?`, personal); n != 0 {
		t.Error("the personal sealed document survived the offboarding")
	}

	// El receptor es el autor; la contraseña de quien se fue ya no sirve. La suya, con reauth.
	doc := "/api/sealed/" + area
	if got := f.expect(t, "adminTI", "GET", doc, "", http.StatusOK); !strings.Contains(got, `"is_author":true`) || !strings.Contains(got, `"needs_password":true`) {
		t.Fatalf("receiver view: %s", got)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM sealed_keys WHERE page_id = ? AND holder_type = 'password'`, area); n != 0 {
		t.Fatal("the password key of the person who left is still there")
	}
	f.expect(t, "adminTI", "POST", doc+"/unlock", `{"password":"la nueva del receptor"}`, http.StatusUnauthorized)
	f.reauth(t, "adminTI")
	f.expect(t, "adminTI", "POST", doc+"/unlock", `{"password":"corta"}`, http.StatusUnprocessableEntity)
	f.expect(t, "adminTI", "POST", doc+"/unlock", `{"password":"la nueva del receptor"}`, http.StatusNoContent)
	if got := f.expect(t, "adminTI", "GET", doc, "", http.StatusOK); !strings.Contains(got, "kwsellado del área") || !strings.Contains(got, `"can_edit":true`) {
		t.Fatalf("receiver open view: %s", got)
	}
	f.expect(t, "adminTI", "PUT", doc, `{"title":"kwsellado","markdown":"del receptor","base_revision":1}`, http.StatusOK)
	var events []string
	rows, _ := f.db.Read.Query(`SELECT action FROM audit_log WHERE target_id = ? ORDER BY id`, area)
	for rows.Next() {
		var a string
		rows.Scan(&a)
		events = append(events, a)
	}
	rows.Close()
	if s := strings.Join(events, ","); !strings.Contains(s, "sealed.owner_transfer") || !strings.Contains(s, "sealed.password_set") {
		t.Errorf("audit: %s", s)
	}

	// Un autor que sale del área deja de verlo, como cualquier otra persona sin membresía (§3.3).
	members := "/api/admin/workspaces/" + wsID(t, f, "ti") + "/members"
	f.expect(t, "super", "POST", members, `{"email":"viewerTI@empresa.cl","role":"editor"}`, http.StatusNoContent)
	f.expect(t, "viewerTI", "POST", "/api/w/ti/sealed", `{"slug":"de-viewer","title":"x","markdown":"x","password":"`+pass+`"}`, http.StatusCreated)
	var mine string
	f.db.Read.QueryRow(`SELECT id FROM sealed_pages WHERE slug = 'de-viewer'`).Scan(&mine)
	f.expect(t, "super", "DELETE", members+"/"+f.users["viewerTI"], "", http.StatusNoContent)
	f.expect(t, "viewerTI", "GET", "/api/sealed/"+mine, "", http.StatusNotFound)
}

// TestSealedGrants son la bóveda y los accesos de §6.1–§6.3: solo el autor y el superadmin los
// dan, a personas de cualquier área que tengan bóveda; quien lo recibe lo abre con su clave de
// bóveda y con el permiso dado; quitarlo lo cierra en el acto. Poner una bóveda nueva pierde lo
// compartido con la anterior.
func TestSealedGrants(t *testing.T) {
	f := newFixture(t)
	const pass, vault = "una clave bien larga", "mi clave de bóveda"
	var out struct{ ID string }
	json.Unmarshal([]byte(f.expect(t, "editorTI", "POST", "/api/w/ti/sealed",
		`{"slug":"legal/contrato","title":"kwsellado","markdown":"kwsellado compartido","password":"`+pass+`"}`, http.StatusCreated)), &out)
	doc := "/api/sealed/" + out.ID
	grant := func(who, email, perm string, code int) {
		t.Helper()
		f.expect(t, who, "POST", doc+"/grants", `{"email":"`+email+`","permission":"`+perm+`"}`, code)
	}

	// La bóveda pide la sesión reautenticada y una clave de 12 caracteres o más.
	f.expect(t, "adminFin", "POST", "/api/me/vault", `{"vault_key":"`+vault+`"}`, http.StatusUnauthorized)
	f.reauth(t, "adminFin")
	f.expect(t, "adminFin", "POST", "/api/me/vault", `{"vault_key":"corta"}`, http.StatusUnprocessableEntity)
	f.expect(t, "adminFin", "POST", "/api/me/vault", `{"vault_key":"`+vault+`"}`, http.StatusOK)
	if got := f.expect(t, "adminFin", "GET", "/api/auth/me", "", http.StatusOK); !strings.Contains(got, `"vault":true`) {
		t.Errorf("me: %s", got)
	}

	// Dan acceso el autor y el superadmin; el admin del área no (§19.1); el resto, ni sabe que existe.
	grant("adminTI", "adminFin@empresa.cl", "read", http.StatusConflict)
	f.expect(t, "adminTI", "GET", doc+"/grants", "", http.StatusConflict)
	for _, who := range []string{"viewerTI", "outsider", "adminFin"} {
		f.expect(t, who, "GET", doc+"/grants", "", http.StatusNotFound)
		grant(who, "adminFin@empresa.cl", "read", http.StatusNotFound)
		f.expect(t, who, "DELETE", doc+"/grants/"+f.users["adminFin"], "", http.StatusNotFound)
	}
	grant("editorTI", "nadie@empresa.cl", "read", http.StatusUnprocessableEntity)
	grant("editorTI", "editorTI@empresa.cl", "read", http.StatusUnprocessableEntity)
	grant("editorTI", "adminFin@empresa.cl", "admin", http.StatusUnprocessableEntity)
	grant("editorTI", "viewerTI@empresa.cl", "read", http.StatusConflict) // sin bóveda
	grant("editorTI", "adminFin@empresa.cl", "read", http.StatusNoContent)
	if got := f.expect(t, "editorTI", "GET", doc+"/grants", "", http.StatusOK); !strings.Contains(got, `"email":"adminFin@empresa.cl"`) || !strings.Contains(got, `"permission":"read"`) {
		t.Errorf("grants: %s", got)
	}
	f.reauth(t, "super")
	f.expect(t, "super", "GET", doc+"/grants", "", http.StatusOK)

	// Quien lo recibe, de otra área, lo encuentra en "mis sellados" y lo abre con su bóveda.
	if got := f.expect(t, "adminFin", "GET", "/api/me/sealed", "", http.StatusOK); !strings.Contains(got, out.ID) || !strings.Contains(got, `"access":"read"`) {
		t.Errorf("my sealed: %s", got)
	}
	if got := f.expect(t, "adminFin", "GET", doc, "", http.StatusOK); !strings.Contains(got, `"locked":true`) || !strings.Contains(got, `"grant":"read"`) {
		t.Errorf("grantee locked view: %s", got)
	}
	f.expect(t, "adminFin", "POST", doc+"/unlock", `{"vault_key":"no es mi clave"}`, http.StatusUnprocessableEntity)
	f.expect(t, "adminFin", "POST", doc+"/unlock", `{"password":"`+pass+`"}`, http.StatusNoContent) // también la contraseña, si se la dieron
	*f.clock = f.clock.Add(16 * time.Minute)
	f.expect(t, "adminFin", "POST", doc+"/unlock", `{"vault_key":"`+vault+`"}`, http.StatusNoContent)
	if got := f.expect(t, "adminFin", "GET", doc, "", http.StatusOK); !strings.Contains(got, "kwsellado compartido") || !strings.Contains(got, `"can_edit":false`) {
		t.Errorf("grantee open view: %s", got)
	}
	f.expect(t, "adminFin", "PUT", doc, `{"title":"x","markdown":"x","base_revision":1}`, http.StatusConflict)
	f.expect(t, "adminFin", "DELETE", doc, `{"confirm":"BORRAR"}`, http.StatusConflict)

	// Cambiarle el permiso lo cierra; abierto de nuevo, edita. Dar acceso pide tenerlo abierto.
	grant("editorTI", "adminFin@empresa.cl", "edit", http.StatusLocked) // pasaron los 15 minutos
	f.expect(t, "editorTI", "POST", doc+"/unlock", `{"password":"`+pass+`"}`, http.StatusNoContent)
	grant("editorTI", "adminFin@empresa.cl", "edit", http.StatusNoContent)
	f.expect(t, "adminFin", "PUT", doc, `{"title":"kwsellado","markdown":"de Finanzas","base_revision":1}`, http.StatusLocked)
	f.expect(t, "adminFin", "POST", doc+"/unlock", `{"vault_key":"`+vault+`"}`, http.StatusNoContent)
	f.expect(t, "adminFin", "PUT", doc, `{"title":"kwsellado","markdown":"de Finanzas","base_revision":1}`, http.StatusOK)

	// Quitarlo: deja de existir para esa persona en el acto, aunque lo tuviera abierto.
	f.expect(t, "editorTI", "DELETE", doc+"/grants/"+f.users["adminFin"], "", http.StatusNoContent)
	f.expect(t, "editorTI", "DELETE", doc+"/grants/"+f.users["adminFin"], "", http.StatusNotFound)
	f.expect(t, "adminFin", "GET", doc, "", http.StatusNotFound)
	if got := f.expect(t, "adminFin", "GET", "/api/me/sealed", "", http.StatusOK); strings.TrimSpace(got) != "[]" {
		t.Errorf("my sealed after revoke: %s", got)
	}

	// Una bóveda nueva (la anterior se olvidó) pierde lo compartido: hay que volver a compartirlo.
	grant("editorTI", "adminFin@empresa.cl", "read", http.StatusNoContent)
	f.reauth(t, "adminFin")
	if got := f.expect(t, "adminFin", "POST", "/api/me/vault", `{"vault_key":"otra clave de bóveda"}`, http.StatusOK); !strings.Contains(got, `"lost":1`) {
		t.Errorf("rotate: %s", got)
	}
	f.expect(t, "adminFin", "GET", doc, "", http.StatusNotFound)
	grant("editorTI", "adminFin@empresa.cl", "read", http.StatusNoContent)
	f.expect(t, "adminFin", "POST", doc+"/unlock", `{"vault_key":"otra clave de bóveda"}`, http.StatusNoContent)

	// La baja de quien recibió el acceso borra su DEK envuelta y su bóveda (§8.4).
	f.expect(t, "super", "POST", "/api/admin/workspaces/"+wsID(t, f, "finanzas")+"/members", `{"email":"outsider@empresa.cl","role":"admin"}`, http.StatusNoContent)
	f.expect(t, "super", "POST", "/api/admin/users/"+f.users["adminFin"]+"/offboarding", `{"now":true}`, http.StatusOK)
	if n := f.count(t, `SELECT COUNT(*) FROM sealed_keys WHERE holder_user_id = ?`, f.users["adminFin"]); n != 0 {
		t.Error("the grant of the person who left survived")
	}
	if n := f.count(t, `SELECT COUNT(*) FROM users WHERE id = ? AND vault_pubkey IS NOT NULL`, f.users["adminFin"]); n != 0 {
		t.Error("the vault of the person who left survived")
	}
}

// TestSealedDeclassify es desclasificar (§6.1): solo el superadmin, con la sesión
// reautenticada. El sellado deja de existir y en su ruta queda una página del área, indexada,
// con el nivel del autor; las subpáginas que ya había suben a ese nivel.
func TestSealedDeclassify(t *testing.T) {
	f := newFixture(t)
	f.expect(t, "editorTI", "PUT", "/api/w/ti/pages/legal/acta/anexo", `{"title":"Anexo","markdown":"anexo","base_revision":0}`, http.StatusNoContent)
	var out struct{ ID string }
	json.Unmarshal([]byte(f.expect(t, "editorTI", "POST", "/api/w/ti/sealed",
		`{"slug":"legal/acta","title":"Acta kwdesclas","markdown":"El acuerdo kwdesclas.","password":"una clave bien larga"}`, http.StatusCreated)), &out)
	doc := "/api/sealed/" + out.ID
	f.expect(t, "editorTI", "PUT", doc, `{"title":"Acta kwdesclas","markdown":"El acuerdo final kwdesclas.","base_revision":1}`, http.StatusOK)
	if got := f.expect(t, "adminTI", "GET", "/api/w/ti/search?q=kwdesclas", "", http.StatusOK); strings.Contains(got, "legal/acta") {
		t.Fatalf("sealed is searchable: %s", got)
	}

	// Quien no lo ve, el 404 de siempre; el autor y el admin del área lo ven pero no pueden.
	f.expect(t, "viewerTI", "POST", doc+"/declassify", "", http.StatusNotFound)
	f.expect(t, "editorTI", "POST", doc+"/declassify", "", http.StatusUnprocessableEntity)
	f.expect(t, "adminTI", "POST", doc+"/declassify", "", http.StatusUnprocessableEntity)
	f.expect(t, "super", "POST", doc+"/declassify", "", http.StatusUnauthorized)
	if got := f.expect(t, "super", "GET", doc, "", http.StatusOK); !strings.Contains(got, `"can_declassify":true`) {
		t.Errorf("super view: %s", got)
	}
	f.reauth(t, "super")
	if got := f.expect(t, "super", "POST", doc+"/declassify", "", http.StatusOK); strings.TrimSpace(got) != `{"area":"ti","slug":"legal/acta"}` {
		t.Errorf("declassify: %q", got)
	}

	// El sellado ya no existe; la página tiene la revisión vigente, con el autor y su nivel (N2).
	f.expect(t, "editorTI", "GET", doc, "", http.StatusNotFound)
	f.expect(t, "super", "GET", doc, "", http.StatusNotFound)
	page := f.expect(t, "editorTI", "GET", "/api/w/ti/pages/legal/acta", "", http.StatusOK)
	if !strings.Contains(page, "El acuerdo final kwdesclas.") || !strings.Contains(page, `"level":2`) {
		t.Errorf("page: %s", page)
	}
	f.expect(t, "viewerTI", "GET", "/api/w/ti/pages/legal/acta", "", http.StatusNotFound)
	if got := f.expect(t, "editorTI", "GET", "/api/w/ti/pages/legal/acta/anexo", "", http.StatusOK); !strings.Contains(got, `"level":2`) {
		t.Errorf("child level: %s", got)
	}
	if got := f.expect(t, "editorTI", "GET", "/api/w/ti/search?q=kwdesclas", "", http.StatusOK); !strings.Contains(got, "legal/acta") {
		t.Errorf("not indexed: %s", got)
	}
	var owner string
	f.db.Read.QueryRow(`SELECT owner_id FROM pages WHERE slug = 'legal/acta'`).Scan(&owner)
	if owner != f.users["editorTI"] {
		t.Errorf("owner %q, want the author", owner)
	}
	var left int
	f.db.Read.QueryRow(`SELECT (SELECT COUNT(*) FROM sealed_keys WHERE page_id = ?1) + (SELECT COUNT(*) FROM sealed_revisions WHERE page_id = ?1)`, out.ID).Scan(&left)
	if left != 0 {
		t.Errorf("%d sealed rows left", left)
	}
	var events struct {
		Events []struct{ Meta map[string]any }
	}
	json.Unmarshal([]byte(f.expect(t, "super", "GET", "/api/admin/audit?action=sealed.declassify", "", http.StatusOK)), &events)
	if len(events.Events) != 1 || events.Events[0].Meta["level"] != 2.0 {
		t.Errorf("audit: %+v", events)
	}
}

// TestSealedPasswordReset es restablecer la contraseña olvidada (§6.3): el autor pide, el
// superadmin aprueba con la sesión reautenticada (vale 24 horas) y el autor pone la nueva, también
// reautenticado. El superadmin nunca la conoce; la vieja deja de servir y se cierran las sesiones.
func TestSealedPasswordReset(t *testing.T) {
	f := newFixture(t)
	var out struct{ ID string }
	json.Unmarshal([]byte(f.expect(t, "editorTI", "POST", "/api/w/ti/sealed",
		`{"slug":"rrhh/sueldos","title":"kwreset","markdown":"kwreset texto","password":"la clave olvidada"}`, http.StatusCreated)), &out)
	doc := "/api/sealed/" + out.ID
	const newPass = `{"new_password":"la clave nueva y larga"}`
	f.reauth(t, "adminTI")
	f.expect(t, "adminTI", "POST", doc+"/unlock", `{"as_admin":true}`, http.StatusNoContent)

	// Solo el autor pide; sin aprobación no se completa.
	f.expect(t, "viewerTI", "POST", doc+"/password-reset", "", http.StatusNotFound)
	f.expect(t, "adminTI", "POST", doc+"/password-reset", "", http.StatusUnprocessableEntity)
	f.expect(t, "editorTI", "POST", doc+"/password-reset", "", http.StatusNoContent)
	if got := f.expect(t, "editorTI", "GET", doc, "", http.StatusOK); !strings.Contains(got, `"reset":"requested"`) {
		t.Errorf("author view: %s", got)
	}
	if got := f.expect(t, "adminTI", "GET", doc, "", http.StatusOK); strings.Contains(got, `"reset"`) {
		t.Errorf("area admin sees the reset: %s", got)
	}
	f.reauth(t, "editorTI")
	f.expect(t, "editorTI", "POST", doc+"/password-reset/complete", newPass, http.StatusConflict)

	// El superadmin lo ve en su lista, primero, y lo aprueba con la sesión reautenticada.
	f.expect(t, "editorTI", "POST", "/api/w/ti/sealed", `{"slug":"rrhh/otro","title":"x","markdown":"x","password":"una clave bien larga"}`, http.StatusCreated)
	f.expect(t, "adminTI", "GET", "/api/admin/sealed", "", http.StatusNotFound)
	var list []struct{ ID, Slug, Reset string }
	json.Unmarshal([]byte(f.expect(t, "super", "GET", "/api/admin/sealed", "", http.StatusOK)), &list)
	if len(list) != 2 || list[0].ID != out.ID || list[0].Reset != "requested" || list[1].Reset != "" {
		t.Errorf("admin list: %+v", list)
	}
	f.expect(t, "adminTI", "POST", doc+"/password-reset/approve", "", http.StatusUnprocessableEntity)
	f.expect(t, "super", "POST", doc+"/password-reset/approve", "", http.StatusUnauthorized)
	f.reauth(t, "super")
	f.expect(t, "super", "POST", doc+"/password-reset/approve", "", http.StatusNoContent)
	if got := f.expect(t, "editorTI", "GET", "/api/me/notifications", "", http.StatusOK); !strings.Contains(got, `"kind":"sealed_reset_approved"`) ||
		!strings.Contains(got, `"sealed":"`+out.ID+`"`) {
		t.Errorf("author notice: %s", got)
	}

	// El autor la pone, con la sesión reautenticada; la vieja ya no sirve y el admin queda fuera.
	f.expect(t, "adminTI", "POST", doc+"/password-reset/complete", newPass, http.StatusUnprocessableEntity)
	f.expect(t, "editorTI", "POST", doc+"/password-reset/complete", `{"new_password":"corta"}`, http.StatusUnprocessableEntity)
	f.expect(t, "editorTI", "POST", doc+"/password-reset/complete", newPass, http.StatusNoContent)
	if got := f.expect(t, "editorTI", "GET", doc, "", http.StatusOK); !strings.Contains(got, "kwreset texto") || !strings.Contains(got, `"reset":""`) {
		t.Errorf("after reset: %s", got)
	}
	if got := f.expect(t, "adminTI", "GET", doc, "", http.StatusOK); !strings.Contains(got, `"locked":true`) {
		t.Errorf("admin copy still open: %s", got)
	}
	f.expect(t, "editorTI", "POST", doc+"/password-reset/complete", newPass, http.StatusConflict) // ya se usó
	own := f.cookies["editorTI"]
	f.cookies["editorTI"] = f.do("", "POST", "/api/auth/login", `{"email":"editorTI@empresa.cl","password":"`+password+`"}`).Result().Cookies()[0]
	f.expect(t, "editorTI", "POST", doc+"/unlock", `{"password":"la clave olvidada"}`, http.StatusUnprocessableEntity)
	f.expect(t, "editorTI", "POST", doc+"/unlock", `{"password":"la clave nueva y larga"}`, http.StatusNoContent)
	f.cookies["editorTI"] = own

	// La aprobación vale 24 horas, también la que el superadmin da sin pedido.
	f.reauth(t, "super")
	f.expect(t, "super", "POST", doc+"/password-reset/approve", "", http.StatusNoContent)
	if got := f.expect(t, "editorTI", "GET", doc, "", http.StatusOK); !strings.Contains(got, `"reset":"approved"`) {
		t.Errorf("approved without request: %s", got)
	}
	*f.clock = f.clock.Add(25 * time.Hour)
	f.reauth(t, "editorTI")
	f.expect(t, "editorTI", "POST", doc+"/password-reset/complete", newPass, http.StatusConflict)

	for action, want := range map[string]int{"sealed.password_reset_request": 1, "sealed.password_reset_approve": 2, "sealed.password_reset_complete": 1} {
		if n := f.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = ? AND target_id = '`+out.ID+`'`, action); n != want {
			t.Errorf("%s: %d events, want %d", action, n, want)
		}
	}
}
