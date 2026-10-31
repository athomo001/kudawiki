package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

// TestPersonalHome es el inicio personal (§15, B5): lo que se sigue y lo propio por revisar, de
// todas las áreas de quien pregunta, sin nada que su nivel no le deje leer (§17).
func TestPersonalHome(t *testing.T) {
	f := newFixture(t)
	f.expect(t, "adminTI", "PUT", "/api/w/ti/pages/uno", `{"title":"Uno kwvisible","markdown":"x","base_revision":0}`, http.StatusNoContent)
	f.expect(t, "adminTI", "PUT", "/api/w/ti/pages/subida", `{"title":"Subida kwleakdos","markdown":"x","base_revision":0}`, http.StatusNoContent)
	f.expect(t, "editorTI", "PUT", "/api/w/ti/pages/propia", `{"title":"Propia kwpropia","markdown":"x","base_revision":0}`, http.StatusNoContent)
	f.expect(t, "adminFin", "PUT", "/api/w/finanzas/pages/plan", `{"title":"Plan kwleakfin","markdown":"x","base_revision":0}`, http.StatusNoContent)
	for _, slug := range []string{"uno", "subida"} {
		f.expect(t, "viewerTI", "PUT", "/api/w/ti/watch/"+slug, `{"subtree":false}`, http.StatusNoContent)
	}
	// La sube a N2 después de que el lector la siguió: deja de existir para él.
	f.expect(t, "adminTI", "PATCH", "/api/w/ti/meta/subida", `{"level":2}`, http.StatusNoContent)
	// Lo que se sigue en el espacio personal no sale: el inicio es de las áreas.
	own := "/api/w/" + f.personalSlug(t, "viewerTI")
	f.expect(t, "viewerTI", "PUT", own+"/pages/mio", `{"title":"Mío kwpersonal","markdown":"x","base_revision":0}`, http.StatusNoContent)
	f.expect(t, "viewerTI", "PUT", own+"/watch/mio", `{"subtree":false}`, http.StatusNoContent)

	body := f.expect(t, "viewerTI", "GET", "/api/me/watching", "", http.StatusOK)
	if !strings.Contains(body, "kwvisible") || !strings.Contains(body, `"slug":"ti"`) {
		t.Errorf("watching misses a followed page: %s", body)
	}
	for _, mark := range []string{"kwleakdos", "kwpersonal", "kwleakfin"} {
		if strings.Contains(body, mark) {
			t.Errorf("watching leaks %s: %s", mark, body)
		}
	}
	if body := f.expect(t, "adminFin", "GET", "/api/me/watching", "", http.StatusOK); body != "[]\n" && strings.TrimSpace(body) != "[]" {
		t.Errorf("someone else's follows: %s", body)
	}

	// Por revisar: las vencidas propias, en cualquier área, mientras se puedan leer.
	f.expect(t, "editorTI", "PATCH", "/api/w/ti/meta/propia", `{"review_interval_days":30}`, http.StatusNoContent)
	f.db.Write.Exec(`UPDATE pages SET verified_at = datetime('now', '-40 days') WHERE slug IN ('propia', 'uno')`)
	if body := f.expect(t, "editorTI", "GET", "/api/me/review-queue", "", http.StatusOK); !strings.Contains(body, "kwpropia") || strings.Contains(body, "kwvisible") {
		t.Errorf("own review queue: %s", body)
	}
	f.expect(t, "adminTI", "PATCH", "/api/w/ti/meta/propia", `{"level":3}`, http.StatusNoContent)
	if body := f.expect(t, "editorTI", "GET", "/api/me/review-queue", "", http.StatusOK); strings.Contains(body, "kwpropia") {
		t.Errorf("a page the owner can no longer read: %s", body)
	}
}
