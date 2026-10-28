package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

type trashEntry struct {
	ID    string
	Slug  string
	Pages int
}

func trashOf(t *testing.T, f fixture, who string) []trashEntry {
	t.Helper()
	var items []trashEntry
	w := f.do(who, "GET", "/api/w/ti/trash", "")
	if w.Code != http.StatusOK {
		t.Fatalf("trash for %s: %d %s", who, w.Code, w.Body)
	}
	json.Unmarshal(w.Body.Bytes(), &items)
	return items
}

func restore(t *testing.T, f fixture, who, slug string) *http.Response {
	t.Helper()
	for _, it := range trashOf(t, f, "adminTI") {
		if it.Slug == slug {
			return f.do(who, "POST", "/api/w/ti/trash/"+it.ID+"/restore", "").Result()
		}
	}
	t.Fatalf("%s is not in the trash", slug)
	return nil
}

// Matriz de contención de DELETE: borra de verdad, así que va aparte. Quien no tiene el rango o
// no ve la página recibe el 404 uniforme y la página sigue ahí; quien puede, la borra y el
// admin la restaura para el caso siguiente.
func TestDeleteContainment(t *testing.T) {
	f := newFixture(t)
	s := seed(t, f)
	uniform := f.do("adminTI", "GET", "/api/w/no-existe/tree", "")
	for who, r := range rank {
		for lvl := 0; lvl <= 3; lvl++ {
			w := f.do(who, "DELETE", "/api/w/ti/pages/"+s.slug[lvl], "")
			allowed := r >= 2 && lvl > 0 && r >= lvl
			switch {
			case allowed && w.Code != http.StatusNoContent:
				t.Errorf("%s deletes level %d: %d %s", who, lvl, w.Code, w.Body)
			case allowed:
				if res := restore(t, f, "adminTI", s.slug[lvl]); res.StatusCode != http.StatusNoContent {
					t.Fatalf("restore %s: %d", s.slug[lvl], res.StatusCode)
				}
			case w.Code != uniform.Code || w.Body.String() != uniform.Body.String():
				t.Errorf("%s deletes level %d: %d %s, want the uniform 404", who, lvl, w.Code, w.Body)
			case lvl > 0 && f.do("adminTI", "GET", "/api/w/ti/pages/"+s.slug[lvl], "").Code != http.StatusOK:
				t.Errorf("%s got a 404 but the level-%d page is gone", who, lvl)
			}
		}
	}
}

// SPEC §4.5: borrar manda el subárbol visible a la papelera como una unidad; las subpáginas
// que quien borra no ve se quedan (§19.37). Lo borrado no aparece en ninguna parte, su ruta
// queda libre, y restaurar choca si alguien la ocupó.
func TestTrashLifecycle(t *testing.T) {
	f := newFixture(t)
	create(t, f, "adminTI", "red")
	create(t, f, "adminTI", "red/vpn")
	create(t, f, "adminTI", "red/secreta")
	level(t, f, "adminTI", "red/secreta", "3")
	if w := f.do("adminTI", "PUT", "/api/w/ti/pages/indice", `{"title":"i","markdown":"[[red/vpn]]","base_revision":0}`); w.Code != http.StatusNoContent {
		t.Fatal(w.Body)
	}

	if w := f.do("editorTI", "DELETE", "/api/w/ti/pages/red", ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	tree := f.do("adminTI", "GET", "/api/w/ti/tree", "").Body.String()
	if strings.Contains(tree, `"red"`) || strings.Contains(tree, `"red/vpn"`) || !strings.Contains(tree, `"red/secreta"`) {
		t.Errorf("tree after delete (the hidden page stays): %s", tree)
	}
	for _, path := range []string{"/api/w/ti/pages/red/vpn", "/api/w/ti/history/red/vpn", "/api/w/ti/backlinks/red/vpn"} {
		if c := f.do("adminTI", "GET", path, "").Code; c != http.StatusNotFound {
			t.Errorf("GET %s of a trashed page: %d, want 404", path, c)
		}
	}
	if got := searchQ(f, "adminTI", "red"); strings.Contains(got, `"slug":"red`) && !strings.Contains(got, `"slug":"red/secreta"`) {
		t.Errorf("search finds trashed pages: %s", got)
	}

	// La papelera: el editor ve lo que borró, el lector no tiene papelera.
	items := trashOf(t, f, "editorTI")
	if len(items) != 1 || items[0].Slug != "red" || items[0].Pages != 2 {
		t.Fatalf("editor's trash: %+v", items)
	}
	if c := f.do("viewerTI", "GET", "/api/w/ti/trash", "").Code; c != http.StatusNotFound {
		t.Errorf("viewer's trash: %d, want 404", c)
	}

	// La ruta quedó libre; restaurar sobre ella no está disponible.
	create(t, f, "adminTI", "red")
	if w := f.do("editorTI", "POST", "/api/w/ti/trash/"+items[0].ID+"/restore", ""); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "path_unavailable") {
		t.Errorf("restore over an occupied path: %d %s", w.Code, w.Body)
	}
	if w := f.do("adminTI", "POST", "/api/w/ti/move/red", `{"to":"red-nueva"}`); w.Code != http.StatusNoContent {
		t.Fatalf("move: %d %s", w.Code, w.Body)
	}
	if w := f.do("editorTI", "POST", "/api/w/ti/trash/"+items[0].ID+"/restore", ""); w.Code != http.StatusNoContent {
		t.Fatalf("restore: %d %s", w.Code, w.Body)
	}
	if hist := f.do("editorTI", "GET", "/api/w/ti/history/red/vpn", "").Body.String(); !strings.Contains(hist, `"kind":"restored"`) || !strings.Contains(hist, `"kind":"deleted"`) {
		t.Errorf("history after restore: %s", hist)
	}
	if bl := f.do("adminTI", "GET", "/api/w/ti/backlinks/red/vpn", "").Body.String(); !strings.Contains(bl, `"slug":"indice"`) {
		t.Errorf("links to the restored page: %s", bl)
	}
	if left := trashOf(t, f, "adminTI"); len(left) != 0 {
		t.Errorf("trash after restore: %+v", left)
	}
}

// §19.38: el admin ve y restaura toda la papelera; un editor, solo lo que él borró.
func TestTrashOnlyShowsOwnDeletionsToEditors(t *testing.T) {
	f := newFixture(t)
	create(t, f, "adminTI", "otra")
	if w := f.do("adminTI", "DELETE", "/api/w/ti/pages/otra", ""); w.Code != http.StatusNoContent {
		t.Fatal(w.Body)
	}
	if items := trashOf(t, f, "editorTI"); len(items) != 0 {
		t.Errorf("editor sees someone else's deletion: %+v", items)
	}
	id := trashOf(t, f, "adminTI")[0].ID
	uniform := f.do("adminTI", "GET", "/api/w/no-existe/tree", "")
	if w := f.do("editorTI", "POST", "/api/w/ti/trash/"+id+"/restore", ""); w.Code != uniform.Code || w.Body.String() != uniform.Body.String() {
		t.Errorf("editor restores someone else's deletion: %d %s, want the uniform 404", w.Code, w.Body)
	}
}
