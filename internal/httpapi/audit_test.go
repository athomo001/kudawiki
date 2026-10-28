package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

type auditEvent struct {
	Action, Page, IP, Workspace, TargetID string
	Actor                                 *struct{ Email string }
	Meta                                  map[string]any
}

func auditOf(t *testing.T, f fixture, who, path string) []auditEvent {
	t.Helper()
	w := f.do(who, "GET", path, "")
	if w.Code != http.StatusOK {
		t.Fatalf("%s %s: %d %s", who, path, w.Code, w.Body)
	}
	var out struct{ Events []auditEvent }
	json.Unmarshal(w.Body.Bytes(), &out)
	return out.Events
}

func find(es []auditEvent, action, actor string) *auditEvent {
	for i, e := range es {
		if (action == "" || e.Action == action) && (actor == "" || (e.Actor != nil && e.Actor.Email == actor+"@empresa.cl")) {
			return &es[i]
		}
	}
	return nil
}

// SPEC §11: el admin del área ve los eventos de su área, con la ruta de la página y sin IP; el
// superadmin ve todas las áreas con la IP, y nunca rutas ni títulos; nadie más ve la auditoría.
func TestAuditViews(t *testing.T) {
	f := newFixture(t)
	create(t, f, "editorTI", "red")
	level(t, f, "adminTI", "red", "3")
	f.do("adminTI", "GET", "/api/w/ti/pages/red", "")
	if w := f.do("editorTI", "PUT", "/api/w/ti/pages/red", `{"title":"x","markdown":"x","base_revision":0}`); w.Code != http.StatusConflict {
		t.Fatalf("collision with a hidden page: %d", w.Code)
	}
	if w := f.do("adminFin", "PUT", "/api/w/finanzas/pages/plan", `{"title":"Plan secreto","markdown":"x","base_revision":0}`); w.Code != http.StatusNoContent {
		t.Fatal(w.Body)
	}

	area := auditOf(t, f, "adminTI", "/api/w/ti/audit")
	if e := find(area, "page.create", "editorTI"); e == nil || e.Page != "red" || e.IP != "" {
		t.Errorf("page.create in the area view: %+v", e)
	}
	if e := find(area, "page.level_change", "adminTI"); e == nil || e.Meta["from"] != 1.0 || e.Meta["to"] != 3.0 {
		t.Errorf("page.level_change: %+v", e)
	}
	if find(area, "page.read", "adminTI") == nil {
		t.Error("reading an N3 page is not audited")
	}
	if e := find(area, "page.path_unavailable", "editorTI"); e == nil || e.Meta["slug"] != nil {
		t.Errorf("path_unavailable (without the requested path): %+v", e)
	}
	if find(area, "", "adminFin") != nil || find(area, "auth.login", "") != nil {
		t.Error("the area view shows events from another area or global events")
	}
	for _, who := range []string{"editorTI", "viewerTI", "super"} {
		if c := f.do(who, "GET", "/api/w/ti/audit", "").Code; c != http.StatusNotFound {
			t.Errorf("%s sees the area audit: %d", who, c)
		}
	}

	all := auditOf(t, f, "super", "/api/admin/audit")
	if e := find(all, "auth.login", "viewerTI"); e == nil || e.IP == "" {
		t.Errorf("auth.login in the superadmin view: %+v", e)
	}
	raw := f.do("super", "GET", "/api/admin/audit", "").Body.String()
	if strings.Contains(raw, `"page":`) || strings.Contains(raw, "Plan secreto") {
		t.Errorf("the superadmin view reveals paths or titles: %s", raw)
	}
	if c := f.do("adminTI", "GET", "/api/admin/audit", "").Code; c != http.StatusNotFound {
		t.Errorf("an area admin sees the global audit: %d", c)
	}
	for _, e := range auditOf(t, f, "super", "/api/admin/audit?ws=finanzas") {
		if e.Workspace != "finanzas" {
			t.Errorf("ws filter returned %+v", e)
		}
	}
}

// §11.2: el login es público; los fallos no inundan la auditoría. Por persona e IP va el
// primero de la ventana y el bloqueo; contra un email que no existe, nada.
func TestFailedLoginsDoNotFloodTheAudit(t *testing.T) {
	f := newFixture(t)
	for range 10 {
		f.do("", "POST", "/api/auth/login", `{"email":"viewerTI@empresa.cl","password":"mala-clave-123"}`)
		f.do("", "POST", "/api/auth/login", `{"email":"nadie@empresa.cl","password":"mala-clave-123"}`)
	}
	count := map[string]int{}
	for _, e := range auditOf(t, f, "super", "/api/admin/audit?actor=viewerTI@empresa.cl") {
		count[e.Action]++
	}
	if count["auth.login_failed"] != 1 || count["auth.lockout"] != 1 {
		t.Errorf("got %v, want one auth.login_failed and one auth.lockout", count)
	}
	if strings.Contains(f.do("super", "GET", "/api/admin/audit", "").Body.String(), "nadie@") {
		t.Error("attempts against a nonexistent email were audited")
	}
}
