package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

func create(t *testing.T, f fixture, who, slug string) {
	t.Helper()
	if w := f.do(who, "PUT", "/api/w/ti/pages/"+slug, `{"title":"`+slug+`","markdown":"x","base_revision":0}`); w.Code != http.StatusNoContent {
		t.Fatalf("create %s: %d %s", slug, w.Code, w.Body)
	}
}

func level(t *testing.T, f fixture, who, slug string, n string) int {
	t.Helper()
	return f.do(who, "PATCH", "/api/w/ti/meta/"+slug, `{"level":`+n+`}`).Code
}

// Reglas de nivel de SPEC §3.2.
func TestLevelRules(t *testing.T) {
	f := newFixture(t)
	create(t, f, "editorTI", "red")
	create(t, f, "editorTI", "red/vpn")

	if c := level(t, f, "editorTI", "red", "3"); c != http.StatusUnprocessableEntity {
		t.Errorf("editor raises above own level: %d, want 422", c)
	}
	if c := level(t, f, "editorTI", "red", "2"); c != http.StatusNoContent {
		t.Fatalf("editor sets N2: %d", c)
	}
	// La hija subió con el padre, con su revisión level_changed.
	if hist := f.do("editorTI", "GET", "/api/w/ti/history/red/vpn", "").Body.String(); !strings.Contains(hist, `"kind":"level_changed"`) {
		t.Errorf("child not raised with its parent: %s", hist)
	}
	if c := level(t, f, "editorTI", "red/vpn", "1"); c != http.StatusUnprocessableEntity {
		t.Errorf("child below its parent: %d, want 422", c)
	}
	// Una página nueva hereda el nivel de su padre.
	create(t, f, "editorTI", "red/wifi")
	if w := f.do("viewerTI", "GET", "/api/w/ti/pages/red/wifi", ""); w.Code != http.StatusNotFound {
		t.Errorf("new child of an N2 page visible to a viewer: %d", w.Code)
	}
	if tree := f.do("editorTI", "GET", "/api/w/ti/tree", "").Body.String(); !strings.Contains(tree, `"level":2,"slug":"red/wifi"`) {
		t.Errorf("tree levels: %s", tree)
	}
}

// §19.35: ruta ocupada por una página visible, ocupada por una oculta, o bajo un padre oculto:
// la misma respuesta, byte a byte.
func TestPathUnavailableLooksTheSame(t *testing.T) {
	f := newFixture(t)
	create(t, f, "adminTI", "visible")
	create(t, f, "adminTI", "rrhh")
	if c := level(t, f, "adminTI", "rrhh", "3"); c != http.StatusNoContent {
		t.Fatalf("admin sets N3: %d", c)
	}
	body := `{"title":"x","markdown":"x","base_revision":0}`
	want := f.do("editorTI", "PUT", "/api/w/ti/pages/visible", body)
	if want.Code != http.StatusConflict || !strings.Contains(want.Body.String(), "path_unavailable") {
		t.Fatalf("visible collision: %d %s", want.Code, want.Body)
	}
	for _, slug := range []string{"rrhh", "rrhh/sueldos"} {
		if got := f.do("editorTI", "PUT", "/api/w/ti/pages/"+slug, body); got.Code != want.Code || got.Body.String() != want.Body.String() {
			t.Errorf("create %s: %d %s, want the same as a visible collision", slug, got.Code, got.Body)
		}
		if got := f.do("editorTI", "POST", "/api/w/ti/move/visible", `{"to":"`+slug+`"}`); got.Code != want.Code || got.Body.String() != want.Body.String() {
			t.Errorf("move to %s: %d %s, want the same as a visible collision", slug, got.Code, got.Body)
		}
	}
}

// §19.36: al mover, las subpáginas que quien mueve no ve se quedan donde están, y sus enlaces no cambian.
func TestMoveLeavesHiddenPages(t *testing.T) {
	f := newFixture(t)
	create(t, f, "adminTI", "red")
	create(t, f, "adminTI", "red/cliente")
	create(t, f, "adminTI", "red/secreta")
	level(t, f, "adminTI", "red/secreta", "3")
	if w := f.do("adminTI", "PUT", "/api/w/ti/pages/indice", `{"title":"i","markdown":"[[red/cliente]] [[red/secreta]]","base_revision":0}`); w.Code != http.StatusNoContent {
		t.Fatal(w.Body)
	}
	if w := f.do("editorTI", "POST", "/api/w/ti/move/red", `{"to":"infra"}`); w.Code != http.StatusNoContent {
		t.Fatalf("move: %d %s", w.Code, w.Body)
	}
	tree := f.do("adminTI", "GET", "/api/w/ti/tree", "").Body.String()
	if !strings.Contains(tree, `"infra/cliente"`) || !strings.Contains(tree, `"red/secreta"`) || strings.Contains(tree, "infra/secreta") {
		t.Errorf("hidden page should stay in place: %s", tree)
	}
	if idx := f.do("adminTI", "GET", "/api/w/ti/pages/indice", "").Body.String(); !strings.Contains(idx, `[[infra/cliente]] [[red/secreta]]`) {
		t.Errorf("links after move: %s", idx)
	}
}

// §3.4: el panel lista a cada miembro con la misma regla que autoriza, y simula otro nivel.
func TestAccessPanel(t *testing.T) {
	f := newFixture(t)
	create(t, f, "adminTI", "red")
	got := f.do("adminTI", "GET", "/api/w/ti/access/red?simulate=2", "").Body.String()
	for _, want := range []string{
		`"can_read":true,"can_read_simulated":false,"email":"viewerTI@empresa.cl"`,
		`"can_read":true,"can_read_simulated":true,"email":"editorTI@empresa.cl"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("access panel missing %s in %s", want, got)
		}
	}
}
