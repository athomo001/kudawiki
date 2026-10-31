package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// El área puede exigir el resumen del cambio (§4.3): al cambiar una página que existe, al
// guardar directo y al proponer. Crear una página no lo pide.
func TestRequireChangeSummary(t *testing.T) {
	f := newFixture(t)
	if w := f.do("adminTI", "PATCH", "/api/w/ti/settings", `{"require_change_summary":true}`); w.Code != http.StatusNoContent {
		t.Fatalf("settings: %d %s", w.Code, w.Body)
	}
	if body := f.do("adminTI", "GET", "/api/w/ti/settings", "").Body.String(); !strings.Contains(body, `"require_change_summary":true`) {
		t.Errorf("settings: %s", body)
	}
	if body := f.do("editorTI", "GET", "/api/auth/me", "").Body.String(); !strings.Contains(body, `"require_change_summary":true`) {
		t.Errorf("the editor is not told: %s", body)
	}
	for _, c := range []struct {
		name, method, path, body string
		want                     int
	}{
		{"create without summary", "PUT", "/api/w/ti/pages/vpn", `{"title":"VPN","markdown":"a","base_revision":0}`, 204},
		{"change without summary", "PUT", "/api/w/ti/pages/vpn", `{"title":"VPN","markdown":"b","base_revision":1}`, 422},
		{"blank summary", "PUT", "/api/w/ti/pages/vpn", `{"title":"VPN","markdown":"b","base_revision":1,"change_summary":"  "}`, 422},
		{"change with summary", "PUT", "/api/w/ti/pages/vpn", `{"title":"VPN","markdown":"b","base_revision":1,"change_summary":"Puerto nuevo"}`, 204},
		{"propose without summary", "POST", "/api/w/ti/proposals", `{"slug":"vpn","title":"VPN","markdown":"c","base_revision":2}`, 422},
		{"propose with summary", "POST", "/api/w/ti/proposals", `{"slug":"vpn","title":"VPN","markdown":"c","base_revision":2,"change_summary":"Otro"}`, 201},
		{"propose a new page", "POST", "/api/w/ti/proposals", `{"slug":"nueva","title":"Nueva","markdown":"c","base_revision":0}`, 201},
	} {
		w := f.do("editorTI", c.method, c.path, c.body)
		if w.Code != c.want || (c.want == 422 && !strings.Contains(w.Body.String(), "summary_required")) {
			t.Errorf("%s: %d %s, want %d", c.name, w.Code, w.Body, c.want)
		}
	}
	// La retención: una de las dos, dentro de los topes. En orden (no un mapa): el PATCH deja lo
	// que no se manda, así que "keep_last" solo, con días ya puestos, serían los dos topes.
	for _, c := range []struct {
		body string
		want int
	}{
		{`{"keep_last":5,"keep_days":10}`, 422},
		{`{"keep_last":-1}`, 422},
		{`{"keep_days":40000}`, 422},
		{`{"keep_last":20}`, 204},
		{`{"keep_last":0,"keep_days":90}`, 204},
		{`{"keep_last":20}`, 422},
	} {
		if w := f.do("adminTI", "PATCH", "/api/w/ti/settings", c.body); w.Code != c.want {
			t.Errorf("retention %s: %d, want %d", c.body, w.Code, c.want)
		}
	}
	if body := f.do("adminTI", "GET", "/api/w/ti/settings", "").Body.String(); !strings.Contains(body, `"keep_days":90`) || !strings.Contains(body, `"keep_last":0`) {
		t.Errorf("retention settings: %s", body)
	}
}

// El navegador de archivos (§4.4) muestra lo que cada editor puede ver, con su página; borrar
// manda el archivo a la papelera, salvo que una página que quien borra ve todavía lo use.
func TestMediaBrowser(t *testing.T) {
	f := newFixture(t)
	up := func(who, name string, data []byte) string {
		t.Helper()
		var m struct{ ID, URL string }
		w := f.upload(who, "ti", name, data)
		if json.Unmarshal(w.Body.Bytes(), &m); w.Code != http.StatusCreated {
			t.Fatalf("upload %s: %d %s", name, w.Code, w.Body)
		}
		return m.URL
	}
	save := func(who, slug, md string, base int) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"title": slug, "markdown": md, "base_revision": base})
		if w := f.do(who, "PUT", "/api/w/ti/pages/"+slug, string(body)); w.Code != http.StatusNoContent {
			t.Fatalf("save %s: %d %s", slug, w.Code, w.Body)
		}
	}
	list := func(who, q string) (int, []map[string]any) {
		t.Helper()
		w := f.do(who, "GET", "/api/w/ti/media?q="+q, "")
		var out []map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	names := func(items []map[string]any) string {
		var n []string
		for _, m := range items {
			n = append(n, m["name"].(string))
		}
		return strings.Join(n, ",")
	}

	used := up("editorTI", "usada.png", pngBytes(t))
	loose := up("editorTI", "suelta.txt", []byte("hola"))
	save("editorTI", "redes", "![x]("+used+")", 0)
	secret := up("adminTI", "secreta.png", pngBytes(t))
	save("adminTI", "alto", "![x]("+secret+")", 0)
	setLevel(t, f, "alto", 3)

	code, items := list("editorTI", "")
	if code != http.StatusOK || names(items) != "suelta.txt,usada.png" {
		t.Fatalf("editor's files: %d %s", code, names(items))
	}
	if p := items[1]["page"].(map[string]any); p["slug"] != "redes" || items[1]["thumb"] != true || items[1]["uploaded_by"] != "editorTI" {
		t.Errorf("file details: %v", items[1])
	}
	if items[0]["page"] != nil {
		t.Errorf("loose file has a page: %v", items[0]["page"])
	}
	if _, items := list("adminTI", ""); !strings.Contains(names(items), "secreta.png") {
		t.Errorf("the admin does not see the level-3 file: %s", names(items))
	}
	if _, items := list("editorTI", "SUEL"); names(items) != "suelta.txt" {
		t.Errorf("search: %s", names(items))
	}
	if code, _ := list("viewerTI", ""); code != http.StatusNotFound {
		t.Errorf("viewer: %d, want 404", code)
	}

	id := func(url string) string { return url[strings.LastIndex(url, "/")+1:] }
	// En uso: 409 con la página, y nada cambia.
	w := f.do("editorTI", "DELETE", "/api/w/ti/media/"+id(used), "")
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"slug":"redes"`) {
		t.Errorf("delete a used file: %d %s", w.Code, w.Body)
	}
	if w := f.do("editorTI", "DELETE", "/api/w/ti/media/"+id(secret), ""); w.Code != http.StatusNotFound {
		t.Errorf("delete a file above the editor's level: %d", w.Code)
	}
	if w := f.do("editorTI", "DELETE", "/api/w/ti/media/"+id(loose), ""); w.Code != http.StatusNoContent {
		t.Errorf("delete: %d %s", w.Code, w.Body)
	}
	if w := f.do("editorTI", "GET", loose, ""); w.Code != http.StatusNotFound {
		t.Errorf("deleted file still served: %d", w.Code)
	}
	if _, items := list("editorTI", ""); names(items) != "usada.png" {
		t.Errorf("after deleting: %s", names(items))
	}
	if n := count(t, f, `SELECT COUNT(*) FROM audit_log WHERE action = 'media.delete'`); n != 1 {
		t.Errorf("%d media.delete events, want 1", n)
	}

	// Una página que el editor no ve y que usa el archivo no se cuenta: decir que existe sería una
	// fuga (§17). Se borra, y esa página lo verá roto.
	save("adminTI", "alto", "![x]("+secret+") ![y]("+used+")", 1)
	save("editorTI", "redes", "sin imagen", 1)
	if w := f.do("editorTI", "DELETE", "/api/w/ti/media/"+id(used), ""); w.Code != http.StatusNoContent {
		t.Errorf("a hidden page's use leaked: %d %s", w.Code, w.Body)
	}
}
