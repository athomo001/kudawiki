package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// personalSlug entra como la persona (/auth/me crea su espacio) y devuelve la ruta de su espacio.
func (f fixture) personalSlug(t *testing.T, who string) string {
	t.Helper()
	var me struct {
		Personal   struct{ Slug string }
		Workspaces []struct{ Slug string }
	}
	json.Unmarshal([]byte(f.expect(t, who, "GET", "/api/auth/me", "", http.StatusOK)), &me)
	if !strings.HasPrefix(me.Personal.Slug, "~") {
		t.Fatalf("%s: no personal space in /auth/me: %+v", who, me)
	}
	for _, w := range me.Workspaces {
		if w.Slug == me.Personal.Slug {
			t.Errorf("%s: the personal space is in the area switcher", who)
		}
	}
	return me.Personal.Slug
}

// TestPersonalSpace es la contención del espacio personal (§7, §17): solo lo ve su dueño y el
// superadmin en modo auditoría (solo lectura, con traza); para los demás es el 404 uniforme.
func TestPersonalSpace(t *testing.T) {
	f := newFixture(t)
	uniform := f.do("adminTI", "GET", "/api/w/no-existe/tree", "")
	own := f.personalSlug(t, "editorTI")
	if again := f.personalSlug(t, "editorTI"); again != own {
		t.Fatalf("a second /auth/me made another space: %s vs %s", again, own)
	}
	base := "/api/w/" + own
	w := f.upload("editorTI", own, "kwpersonal.png", pngBytes(t))
	var up struct{ URL string }
	json.Unmarshal(w.Body.Bytes(), &up)
	if w.Code != http.StatusCreated || !strings.HasPrefix(up.URL, base+"/media/") {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	f.expect(t, "editorTI", "PUT", base+"/pages/borrador", `{"title":"Borrador kwpersonal","markdown":"Uno","base_revision":0}`, http.StatusNoContent)
	f.expect(t, "editorTI", "PUT", base+"/pages/borrador", `{"title":"Borrador kwpersonal","markdown":"Dos ![x](`+up.URL+`)","base_revision":1}`, http.StatusNoContent)

	uniformFor := func(who, method, path, body string) {
		t.Helper()
		if w := f.do(who, method, path, body); w.Code != uniform.Code || w.Body.String() != uniform.Body.String() {
			t.Errorf("%s %s %s: %d %s, want the uniform 404", who, method, path, w.Code, w.Body)
		}
	}
	for _, who := range []string{"adminTI", "viewerTI", "adminFin", "outsider"} {
		for _, path := range []string{"/tree", "/pages/borrador", "/search?q=kwpersonal", strings.TrimPrefix(up.URL, base)} {
			uniformFor(who, "GET", base+path, "")
		}
		uniformFor(who, "PUT", base+"/pages/otra", `{"title":"x","markdown":"x","base_revision":0}`)
	}

	// Modo auditoría: el superadmin lee, no escribe, y cada lectura queda registrada.
	if body := f.expect(t, "super", "GET", base+"/pages/borrador", "", http.StatusOK); !strings.Contains(body, "kwpersonal") {
		t.Errorf("superadmin audit read: %s", body)
	}
	uniformFor("super", "PUT", base+"/pages/borrador", `{"title":"x","markdown":"x","base_revision":2}`)
	uniformFor("super", "POST", base+"/publish/borrador", `{"area":"ti","to":"x"}`)
	uniformFor("super", "GET", "/api/w/~01arz3ndektsv4rrffq69g5fav/tree", "")
	if e := find(auditOf(t, f, "super", "/api/admin/audit"), "personal.audit_open", "super"); e == nil || e.Meta["path"] != "/pages/borrador" {
		t.Errorf("audit mode left no trace: %+v", e)
	}
	// El panel del superadmin no lista espacios personales ni los administra por id.
	if body := f.expect(t, "super", "GET", "/api/admin/workspaces", "", http.StatusOK); strings.Contains(body, own) {
		t.Errorf("the admin panel lists a personal space: %s", body)
	}
	var spaceID string
	f.db.Read.QueryRow(`SELECT id FROM workspaces WHERE slug = ?`, own).Scan(&spaceID)
	uniformFor("super", "GET", "/api/admin/workspaces/"+spaceID+"/members/", "")

	// Lo que en un espacio de una sola persona no tiene sentido.
	for _, c := range [][3]string{{"GET", "/settings", ""}, {"GET", "/members/", ""}, {"GET", "/proposals", ""},
		{"POST", "/proposals", `{"slug":"n","title":"n","markdown":"n","base_revision":0}`}, {"GET", "/access/borrador", ""},
		{"POST", "/announcements", `{"title":"x","body":"y"}`}, {"GET", "/approval-overrides", ""}} {
		uniformFor("editorTI", c[0], base+c[1], c[2])
	}
	// Publicar solo existe en el espacio personal, y solo hacia un área donde se es editor o admin.
	uniformFor("editorTI", "POST", "/api/w/ti/publish/borrador", `{"area":"ti","to":"x"}`)
	viewerSpace := "/api/w/" + f.personalSlug(t, "viewerTI")
	f.expect(t, "viewerTI", "PUT", viewerSpace+"/pages/mio", `{"title":"Mío","markdown":"x","base_revision":0}`, http.StatusNoContent)
	uniformFor("viewerTI", "POST", viewerSpace+"/publish/mio", `{"area":"ti","to":"mio"}`)
	uniformFor("editorTI", "POST", base+"/publish/borrador", `{"area":"finanzas","to":"x"}`)
	uniformFor("editorTI", "POST", base+"/publish/borrador", `{"area":"`+viewerSpace[len("/api/w/"):]+`","to":"x"}`)
	f.expect(t, "editorTI", "POST", base+"/publish/borrador", `{"area":"ti","to":"Mal Ruta"}`, http.StatusUnprocessableEntity)

	// Directo: la página pasa con todo su historial y su media copiada al área.
	f.expect(t, "adminTI", "PUT", "/api/w/ti/pages/ocupada", `{"title":"x","markdown":"x","base_revision":0}`, http.StatusNoContent)
	f.expect(t, "editorTI", "POST", base+"/publish/borrador", `{"area":"ti","to":"ocupada"}`, http.StatusConflict)
	f.expect(t, "editorTI", "POST", base+"/publish/borrador", `{"area":"ti","to":"manuales/borrador"}`, http.StatusOK)
	uniformFor("editorTI", "GET", base+"/pages/borrador", "")
	var page struct {
		Markdown string
		Revision int
	}
	json.Unmarshal([]byte(f.expect(t, "viewerTI", "GET", "/api/w/ti/pages/manuales/borrador", "", http.StatusOK)), &page)
	if page.Revision != 3 || strings.Contains(page.Markdown, own) || !strings.Contains(page.Markdown, "/api/w/ti/media/") {
		t.Fatalf("published page: %+v", page)
	}
	media := page.Markdown[strings.Index(page.Markdown, "/api/w/ti/media/") : len(page.Markdown)-1]
	f.expect(t, "viewerTI", "GET", media, "", http.StatusOK)
	if body := f.expect(t, "viewerTI", "GET", "/api/w/ti/history/manuales/borrador", "", http.StatusOK); strings.Count(body, `"revision"`) != 3 {
		t.Errorf("history did not travel: %s", body)
	}
	if body := f.expect(t, "viewerTI", "GET", "/api/w/ti/revisions/2/manuales/borrador", "", http.StatusOK); strings.Contains(body, own) {
		t.Errorf("an old revision still points to the personal space: %s", body)
	}
	if body := f.expect(t, "viewerTI", "GET", "/api/w/ti/search?q=kwpersonal", "", http.StatusOK); !strings.Contains(body, "manuales/borrador") {
		t.Errorf("the published page is not searchable in the area: %s", body)
	}
	if body := f.expect(t, "editorTI", "GET", base+"/search?q=kwpersonal", "", http.StatusOK); strings.Contains(body, "borrador") {
		t.Errorf("the personal space still finds the published page: %s", body)
	}
	if e := find(auditOf(t, f, "adminTI", "/api/w/ti/audit"), "page.publish", "editorTI"); e == nil {
		t.Error("publishing left no audit trail in the area")
	}

	// Con aprobación: queda una propuesta y el borrador sigue en el espacio personal con su estado.
	f.expect(t, "adminTI", "PATCH", "/api/w/ti/settings", `{"approval_mode":"required"}`, http.StatusNoContent)
	// La media que quedó sin página al publicar la primera se la lleva esta, como cualquier huérfana.
	f.expect(t, "editorTI", "PUT", base+"/pages/otro", `{"title":"Otro","markdown":"Otro ![x](`+up.URL+`)","base_revision":0}`, http.StatusNoContent)
	var sent struct{ Proposal string }
	json.Unmarshal([]byte(f.expect(t, "editorTI", "POST", base+"/publish/otro", `{"area":"ti","to":"otro"}`, http.StatusAccepted)), &sent)
	status := func(want string) {
		t.Helper()
		var p struct {
			Sent []struct {
				ID, Status string
				Area       struct{ Slug string }
			}
		}
		json.Unmarshal([]byte(f.expect(t, "editorTI", "GET", base+"/pages/otro", "", http.StatusOK)), &p)
		if len(p.Sent) != 1 || p.Sent[0].ID != sent.Proposal || p.Sent[0].Status != want || p.Sent[0].Area.Slug != "ti" {
			t.Errorf("sent: %+v, want %s", p.Sent, want)
		}
	}
	status("pending")
	f.expect(t, "adminTI", "POST", "/api/w/ti/proposals/"+sent.Proposal+"/approve", `{}`, http.StatusOK)
	status("merged")
	json.Unmarshal([]byte(f.expect(t, "viewerTI", "GET", "/api/w/ti/pages/otro", "", http.StatusOK)), &page)
	if strings.Contains(page.Markdown, own) {
		t.Errorf("the approved page points to the personal space: %s", page.Markdown)
	}
	f.expect(t, "viewerTI", "GET", page.Markdown[strings.Index(page.Markdown, "/api/w/ti/media/"):len(page.Markdown)-1], "", http.StatusOK)
	// Si la persona deja de ser miembro del área, el borrador ya no le cuenta nada de ella.
	f.expect(t, "adminTI", "DELETE", "/api/w/ti/members/"+f.users["editorTI"], "", http.StatusNoContent)
	if !strings.Contains(f.expect(t, "editorTI", "GET", base+"/pages/otro", "", http.StatusOK), `"sent":[]`) {
		t.Error("the draft still reports an area its author left")
	}
}
