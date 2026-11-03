package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

// B7 (§15): el orden manual por carpeta. Se ordenan hermanas visibles; una N3 para un editor es
// el 404 de siempre y nada cambia; mover una página a otra carpeta le quita la posición.
func TestTreeOrder(t *testing.T) {
	f := newFixture(t)
	for _, slug := range []string{"redes", "redes/a", "redes/b", "redes/c", "otra"} {
		f.expect(t, "adminTI", "PUT", "/api/w/ti/pages/"+slug, `{"title":"`+slug+`","markdown":"x","base_revision":0}`, http.StatusNoContent)
	}
	f.expect(t, "adminTI", "PATCH", "/api/w/ti/meta/redes/c", `{"level":3}`, http.StatusNoContent)
	positions := func(who string) map[string]int {
		var refs []struct {
			Slug     string
			Position int
		}
		json.Unmarshal([]byte(f.expect(t, who, "GET", "/api/w/ti/tree", "", http.StatusOK)), &refs)
		out := map[string]int{}
		for _, r := range refs {
			out[r.Slug] = r.Position
		}
		return out
	}
	f.expect(t, "editorTI", "PUT", "/api/w/ti/order", `{"parent":"redes","slugs":["redes/b","redes/a"]}`, http.StatusNoContent)
	if p := positions("viewerTI"); p["redes/b"] != 1 || p["redes/a"] != 2 || p["redes"] != 0 {
		t.Errorf("positions: %v", p)
	}
	// Una hermana que no ve: 404 uniforme, y el orden anterior queda.
	f.expect(t, "editorTI", "PUT", "/api/w/ti/order", `{"parent":"redes","slugs":["redes/a","redes/c"]}`, http.StatusNotFound)
	if p := positions("adminTI"); p["redes/a"] != 2 || p["redes/c"] != 0 {
		t.Errorf("a rejected order changed something: %v", p)
	}
	for _, body := range []string{`{"parent":"redes","slugs":["otra"]}`, `{"parent":"","slugs":["redes/a"]}`,
		`{"parent":"redes","slugs":["redes/a","redes/a"]}`, `{"parent":"redes","slugs":[]}`} {
		f.expect(t, "editorTI", "PUT", "/api/w/ti/order", body, http.StatusUnprocessableEntity)
	}
	f.expect(t, "viewerTI", "PUT", "/api/w/ti/order", `{"parent":"redes","slugs":["redes/a"]}`, http.StatusNotFound)
	f.expect(t, "editorTI", "POST", "/api/w/ti/move/redes/b", `{"to":"otra/b"}`, http.StatusNoContent)
	if p := positions("adminTI"); p["otra/b"] != 0 {
		t.Errorf("a moved page kept its position: %v", p)
	}
}
