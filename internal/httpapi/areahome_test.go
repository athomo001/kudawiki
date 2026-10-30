package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

type announcementOut struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	HTML      string `json:"html"`
	Author    string `json:"author"`
	CanDelete bool   `json:"can_delete"`
}

func (f fixture) announcements(t *testing.T, who string) []announcementOut {
	t.Helper()
	w := f.do(who, "GET", "/api/w/ti/announcements", "")
	if w.Code != http.StatusOK {
		t.Fatalf("%s announcements: %d %s", who, w.Code, w.Body)
	}
	var out []announcementOut
	json.Unmarshal(w.Body.Bytes(), &out)
	return out
}

func (f fixture) announce(t *testing.T, who, title string) string {
	t.Helper()
	w := f.do(who, "POST", "/api/w/ti/announcements", `{"title":"`+title+`","body":"Mañana **a las 8**. Ver [[nivel/tres]]."}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("%s announce: %d %s", who, w.Code, w.Body)
	}
	var out struct{ ID string }
	json.Unmarshal(w.Body.Bytes(), &out)
	return out.ID
}

// §4.17: los anuncios los publican editores y admins, los ven todos los miembros y los borra
// quien los escribió o un admin; para cualquier otro, el anuncio no existe.
func TestAnnouncements(t *testing.T) {
	f := newFixture(t)
	seed(t, f)
	byEditor := f.announce(t, "editorTI", "Corte de red")
	byAdmin := f.announce(t, "adminTI", "Nueva guía")

	list := f.announcements(t, "viewerTI")
	if len(list) != 2 || list[0].Title != "Nueva guía" || list[1].Author != "editorTI" {
		t.Fatalf("viewer list (newest first): %+v", list)
	}
	if !strings.Contains(list[1].HTML, "<strong>a las 8</strong>") || list[0].CanDelete || list[1].CanDelete {
		t.Fatalf("viewer item: %+v", list[1])
	}
	// El enlace a una página que quien lee no ve sale roto, como en una página (§17).
	if !strings.Contains(list[1].HTML, "broken") {
		t.Errorf("a link to a hidden page must look broken to the viewer: %s", list[1].HTML)
	}
	if strings.Contains(f.announcements(t, "adminTI")[1].HTML, "broken") {
		t.Errorf("the admin sees nivel/tres: the link must not be broken")
	}
	for who, want := range map[string][2]bool{"editorTI": {false, true}, "adminTI": {true, true}} {
		got := f.announcements(t, who)
		if got[0].CanDelete != want[0] || got[1].CanDelete != want[1] {
			t.Errorf("%s can_delete: %v %v, want %v", who, got[0].CanDelete, got[1].CanDelete, want)
		}
	}

	uniform := f.do("adminTI", "GET", "/api/w/no-existe/tree", "").Body.String()
	for _, c := range []struct{ who, method, path, body string }{
		{"viewerTI", "POST", "/api/w/ti/announcements", `{"title":"x","body":"y"}`},
		{"adminFin", "GET", "/api/w/ti/announcements", ""},
		{"outsider", "GET", "/api/w/ti/announcements", ""},
		{"viewerTI", "DELETE", "/api/w/ti/announcements/" + byEditor, ""},
		{"editorTI", "DELETE", "/api/w/ti/announcements/" + byAdmin, ""}, // de otra persona
		{"adminFin", "DELETE", "/api/w/finanzas/announcements/" + byEditor, ""},
	} {
		if w := f.do(c.who, c.method, c.path, c.body); w.Code != http.StatusNotFound || w.Body.String() != uniform {
			t.Errorf("%s %s %s: %d %s, want the uniform 404", c.who, c.method, c.path, w.Code, w.Body)
		}
	}
	for _, body := range []string{`{"title":"  ","body":"y"}`, `{"title":"` + strings.Repeat("x", 201) + `","body":""}`,
		`{"title":"x","body":"` + strings.Repeat("y", 20<<10+1) + `"}`} {
		if w := f.do("editorTI", "POST", "/api/w/ti/announcements", body); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "invalid_announcement") {
			t.Errorf("invalid announcement: %d %s", w.Code, w.Body)
		}
	}

	if w := f.do("editorTI", "DELETE", "/api/w/ti/announcements/"+byEditor, ""); w.Code != http.StatusNoContent {
		t.Fatalf("author delete: %d %s", w.Code, w.Body)
	}
	if w := f.do("adminTI", "DELETE", "/api/w/ti/announcements/"+byAdmin, ""); w.Code != http.StatusNoContent {
		t.Fatalf("admin delete: %d %s", w.Code, w.Body)
	}
	if got := f.announcements(t, "viewerTI"); len(got) != 0 {
		t.Fatalf("after delete: %+v", got)
	}
	audit := f.do("adminTI", "GET", "/api/w/ti/audit", "").Body.String()
	for _, action := range []string{"announcement.create", "announcement.delete"} {
		if !strings.Contains(audit, action) {
			t.Errorf("area audit lacks %s", action)
		}
	}
	if strings.Contains(audit, "Corte de red") {
		t.Errorf("the audit must not carry the announcement's text")
	}
}

type statsOut struct {
	Pages     int `json:"pages"`
	Changes   int `json:"changes_30d"`
	New       int `json:"new_30d"`
	Recent    []struct{ Slug string }
	TopViewed []struct {
		Slug  string
		Views int
	} `json:"top_viewed"`
}

// §4.17: la actividad y las más vistas cuentan solo lo que quien mira puede leer, y una visita
// a una página que no ve no existe.
func TestAreaStats(t *testing.T) {
	f := newFixture(t)
	s := seed(t, f)
	for _, v := range []struct {
		who, slug string
		code      int
	}{
		{"viewerTI", s.slug[1], 204}, {"viewerTI", s.slug[1], 204}, {"adminTI", s.slug[3], 204},
		{"viewerTI", s.slug[3], 404}, {"viewerTI", s.slug[0], 404}, {"adminFin", s.slug[1], 404},
	} {
		if w := f.do(v.who, "POST", "/api/w/ti/views/"+v.slug, ""); w.Code != v.code {
			t.Errorf("%s view %s: %d, want %d", v.who, v.slug, w.Code, v.code)
		}
	}
	stats := func(who string) statsOut {
		w := f.do(who, "GET", "/api/w/ti/stats", "")
		if w.Code != http.StatusOK {
			t.Fatalf("%s stats: %d %s", who, w.Code, w.Body)
		}
		var out statsOut
		json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	// Visibles para el viewer: nivel/uno y la página viva en la ruta de la borrada.
	viewer, admin := stats("viewerTI"), stats("adminTI")
	if viewer.Pages != 2 || viewer.Changes != 2 || viewer.New != 2 || len(viewer.Recent) != 2 {
		t.Errorf("viewer stats: %+v", viewer)
	}
	if admin.Pages != 4 || admin.Changes != 4 || admin.New != 4 || len(admin.Recent) != 4 {
		t.Errorf("admin stats: %+v", admin)
	}
	if len(viewer.TopViewed) != 1 || viewer.TopViewed[0].Slug != s.slug[1] || viewer.TopViewed[0].Views != 2 {
		t.Errorf("viewer top viewed: %+v", viewer.TopViewed)
	}
	if len(admin.TopViewed) != 2 || admin.TopViewed[1].Slug != s.slug[3] || admin.TopViewed[1].Views != 1 {
		t.Errorf("admin top viewed: %+v", admin.TopViewed)
	}
}
