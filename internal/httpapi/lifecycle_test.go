package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

// §3.5, B17: archivada es solo lectura para todos (también su admin) y sigue en /auth/me;
// desactivada o con el borrado pedido da el mismo 404 que un área que no existe, y sale de
// /auth/me y del Compartido de otras áreas; el slug viejo de un área renombrada sigue sirviendo
// a sus miembros y a nadie más. Todo es solo del superadmin; borrar pide reautenticarse y
// escribir el slug.
func TestAreaLifecycle(t *testing.T) {
	f := newFixture(t)
	create(t, f, "editorTI", "red")
	id := f.wsID(t, "ti")
	base := "/api/admin/workspaces/" + id
	put := func(path, body string, want int) {
		t.Helper()
		if w := f.do("super", "PUT", base+path, body); w.Code != want {
			t.Fatalf("PUT %s %s: %d %s", path, body, w.Code, w.Body)
		}
	}
	for _, who := range []string{"adminTI", "editorTI"} {
		if c := f.do(who, "PUT", base+"/status", `{"status":"archived"}`).Code; c != http.StatusNotFound {
			t.Errorf("%s changes the status: %d", who, c)
		}
	}

	put("/status", `{"status":"archived"}`, http.StatusNoContent)
	for _, who := range []string{"editorTI", "adminTI"} {
		if w := f.do(who, "PUT", "/api/w/ti/pages/red", `{"title":"red","markdown":"y","base_revision":1}`); w.Code != http.StatusConflict ||
			!strings.Contains(w.Body.String(), "area_archived") {
			t.Errorf("%s writes in an archived area: %d %s", who, w.Code, w.Body)
		}
	}
	if c := f.do("viewerTI", "GET", "/api/w/ti/pages/red", "").Code; c != http.StatusOK {
		t.Errorf("reading an archived area: %d", c)
	}
	if b := f.do("editorTI", "GET", "/api/auth/me", "").Body.String(); !strings.Contains(b, `"archived":true`) {
		t.Errorf("me does not say archived: %s", b)
	}
	put("/status", `{"status":"active"}`, http.StatusNoContent)
	if w := f.do("editorTI", "PUT", "/api/w/ti/pages/red", `{"title":"red","markdown":"y","base_revision":1}`); w.Code != http.StatusNoContent {
		t.Errorf("writing after unarchiving: %d %s", w.Code, w.Body)
	}

	put("/status", `{"status":"deactivated"}`, http.StatusNoContent)
	for _, who := range []string{"adminTI", "viewerTI"} {
		if c := f.do(who, "GET", "/api/w/ti/tree", "").Code; c != http.StatusNotFound {
			t.Errorf("%s sees a deactivated area: %d", who, c)
		}
		if strings.Contains(f.do(who, "GET", "/api/auth/me", "").Body.String(), `"slug":"ti"`) {
			t.Errorf("%s has a deactivated area in me", who)
		}
	}
	put("/status", `{"status":"nope"}`, http.StatusUnprocessableEntity)
	put("/status", `{"status":"active"}`, http.StatusNoContent)

	put("/slug", `{"slug":"Mal Slug"}`, http.StatusUnprocessableEntity)
	put("/slug", `{"slug":"finanzas"}`, http.StatusConflict)
	put("/slug", `{"slug":"tecnologia"}`, http.StatusNoContent)
	for path, want := range map[string]int{"/api/w/ti/pages/red": 200, "/api/w/tecnologia/pages/red": 200} {
		if c := f.do("viewerTI", "GET", path, "").Code; c != want {
			t.Errorf("member %s: %d", path, c)
		}
		if c := f.do("adminFin", "GET", path, "").Code; c != http.StatusNotFound {
			t.Errorf("non-member %s: %d", path, c)
		}
	}
	if b := f.do("viewerTI", "GET", "/api/auth/me", "").Body.String(); !strings.Contains(b, `"aliases":["ti"]`) || !strings.Contains(b, `"slug":"tecnologia"`) {
		t.Errorf("me after renaming: %s", b)
	}
	if b := f.do("adminFin", "GET", "/api/auth/me", "").Body.String(); strings.Contains(b, `"ti"`) {
		t.Errorf("a non-member learns the old slug: %s", b)
	}

	if c := f.do("super", "POST", base+"/delete", `{"confirm":"tecnologia"}`).Code; c == http.StatusOK {
		t.Fatal("delete without reauthenticating")
	}
	f.reauth(t, "super")
	if w := f.do("super", "POST", base+"/delete", `{"confirm":"ti"}`); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("delete with the wrong slug: %d", w.Code)
	}
	if w := f.do("super", "POST", base+"/delete", `{"confirm":"tecnologia"}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "delete_at") {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if c := f.do("adminTI", "GET", "/api/w/tecnologia/tree", "").Code; c != http.StatusNotFound {
		t.Errorf("an area pending deletion is visible: %d", c)
	}
	put("/status", `{"status":"archived"}`, http.StatusConflict)
	if c := f.do("super", "DELETE", base+"/delete", "").Code; c != http.StatusNoContent {
		t.Fatalf("cancel: %d", c)
	}
	if c := f.do("adminTI", "GET", "/api/w/tecnologia/tree", "").Code; c != http.StatusOK {
		t.Errorf("after cancelling: %d", c)
	}
	events := auditOf(t, f, "super", "/api/admin/audit")
	for _, action := range []string{"ws.archive", "ws.unarchive", "ws.deactivate", "ws.reactivate", "ws.slug_change", "ws.delete_request", "ws.delete_cancel"} {
		if find(events, action, "super") == nil {
			t.Errorf("%s is not audited", action)
		}
	}
}
