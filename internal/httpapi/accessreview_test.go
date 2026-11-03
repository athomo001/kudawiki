package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// §21.2.7: el admin del área ve sus miembros y confirma la lista; queda quién y cuándo, y en la
// auditoría access_review.confirm. Otra área, un editor o un admin de otra área reciben 404. El
// superadmin ve admins y superadmins con sus áreas y el estado de la revisión de cada área.
func TestAccessReview(t *testing.T) {
	f := newFixture(t)
	type review struct {
		Overdue bool `json:"overdue"`
		Last    *struct {
			At string `json:"at"`
			By string `json:"by"`
		} `json:"last"`
		DueAt   string              `json:"due_at"`
		Members []map[string]string `json:"members"`
		Admins  []struct {
			Email      string   `json:"email"`
			Superadmin bool     `json:"superadmin"`
			Areas      []string `json:"areas"`
		} `json:"admins"`
		Areas []struct {
			Slug string `json:"slug"`
			Last *struct {
				By string `json:"by"`
			} `json:"last"`
		} `json:"areas"`
	}
	get := func(who, path string) review {
		w := f.do(who, "GET", path, "")
		if w.Code != http.StatusOK {
			t.Fatalf("%s %s: %d", who, path, w.Code)
		}
		var r review
		json.Unmarshal(w.Body.Bytes(), &r)
		return r
	}

	r := get("adminTI", "/api/w/ti/access-review")
	if r.Last != nil || r.Overdue || r.DueAt == "" || len(r.Members) == 0 {
		t.Fatalf("a new area: %+v", r)
	}
	if strings.Contains(f.do("adminTI", "GET", "/api/w/ti/access-review", "").Body.String(), "adminFin") {
		t.Error("another area's member is listed")
	}
	for _, who := range []string{"editorTI", "adminFin", "outsider"} {
		for _, m := range []string{"GET", "POST"} {
			if c := f.do(who, m, "/api/w/ti/access-review", "").Code; c != http.StatusNotFound {
				t.Errorf("%s %s the TI review: %d", who, m, c)
			}
		}
	}
	if c := f.do("adminTI", "POST", "/api/w/ti/access-review", "").Code; c != http.StatusNoContent {
		t.Fatalf("confirm: %d", c)
	}
	if r := get("adminTI", "/api/w/ti/access-review"); r.Last == nil || r.Last.By != "adminTI" {
		t.Errorf("after confirming: %+v", r.Last)
	}
	if e := find(auditOf(t, f, "adminTI", "/api/w/ti/audit"), "access_review.confirm", "adminTI"); e == nil || e.Meta["entries"] == nil {
		t.Errorf("the confirmation is not audited: %+v", e)
	}

	for _, m := range []string{"GET", "POST"} {
		if c := f.do("adminTI", m, "/api/admin/access-review", "").Code; c != http.StatusNotFound {
			t.Errorf("an area admin %s the global review: %d", m, c)
		}
	}
	g := get("super", "/api/admin/access-review")
	roles := map[string]bool{}
	for _, a := range g.Admins {
		roles[a.Email] = a.Superadmin
		if a.Email == "adminTI@empresa.cl" && (len(a.Areas) != 1 || a.Superadmin) {
			t.Errorf("adminTI: %+v", a)
		}
	}
	if !roles["super@empresa.cl"] || len(roles) != 3 {
		t.Errorf("admins listed: %v", roles)
	}
	for _, a := range g.Areas {
		if (a.Slug == "ti") != (a.Last != nil) {
			t.Errorf("area %s review: %+v", a.Slug, a.Last)
		}
	}
	if c := f.do("super", "POST", "/api/admin/access-review", "").Code; c != http.StatusNoContent {
		t.Fatalf("global confirm: %d", c)
	}
	if g := get("super", "/api/admin/access-review"); g.Last == nil || g.Last.By != "super" {
		t.Errorf("after the global confirm: %+v", g.Last)
	}
}
