package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// §13.5: /metrics solo responde a metrics.allow_cidrs (a las demás, el 404 de siempre) y nunca
// lleva un título ni una ruta; /readyz informa el estado de los respaldos y la cola de correo.
func TestMetricsAndReadyz(t *testing.T) {
	f := newFixture(t)
	f.expect(t, "editorTI", "PUT", "/api/w/ti/pages/secreto-rrhh", `{"title":"Plan secreto","markdown":"x","base_revision":0}`, http.StatusNoContent)
	get := func(remote string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/metrics", nil)
		r.RemoteAddr = remote
		w := httptest.NewRecorder()
		f.h.ServeHTTP(w, r)
		return w
	}
	w := get("192.0.2.7:4000")
	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(body, "kudawiki_up 1") || !strings.Contains(body, `kudawiki_http_requests_total{code="2xx"}`) ||
		!strings.Contains(body, "kudawiki_users_active") {
		t.Fatalf("metrics: %d\n%s", w.Code, body)
	}
	if strings.Contains(body, "secreto") || strings.Contains(body, "Plan") || strings.Contains(body, "/w/") {
		t.Errorf("a title or a path leaked into the metrics:\n%s", body)
	}
	if w := get("203.0.113.5:4000"); w.Code != http.StatusNotFound {
		t.Errorf("outside the allowed networks: %d", w.Code)
	}
	r := httptest.NewRequest("GET", "/readyz", nil)
	rw := httptest.NewRecorder()
	f.h.ServeHTTP(rw, r)
	if rw.Code != http.StatusOK || !strings.Contains(rw.Body.String(), `"backups"`) || !strings.Contains(rw.Body.String(), `"email"`) {
		t.Errorf("readyz: %d %s", rw.Code, rw.Body)
	}
}

// B11: el HTML en caché es por árbol visible. Un enlace a una página N3 sale roto para el lector
// y válido para el admin, pidan en el orden que pidan, y una página nueva cambia el HTML.
func TestRenderCacheByViewer(t *testing.T) {
	f := newFixture(t)
	f.expect(t, "adminTI", "PUT", "/api/w/ti/pages/oculta", `{"title":"Oculta","markdown":"x","base_revision":0}`, http.StatusNoContent)
	f.expect(t, "adminTI", "PATCH", "/api/w/ti/meta/oculta", `{"level":3}`, http.StatusNoContent)
	f.expect(t, "editorTI", "PUT", "/api/w/ti/pages/indice", `{"title":"Índice","markdown":"[[oculta]] [[nueva]]","base_revision":0}`, http.StatusNoContent)
	broken := func(who string) int {
		return strings.Count(f.expect(t, who, "GET", "/api/w/ti/pages/indice", "", http.StatusOK), "broken")
	}
	admin1 := broken("adminTI")
	viewer := broken("viewerTI")
	admin2 := broken("adminTI")
	if admin1 != admin2 || viewer <= admin1 {
		t.Errorf("broken links: admin %d/%d, viewer %d", admin1, admin2, viewer)
	}
	f.expect(t, "editorTI", "PUT", "/api/w/ti/pages/nueva", `{"title":"Nueva","markdown":"x","base_revision":0}`, http.StatusNoContent)
	if after := broken("viewerTI"); after >= viewer {
		t.Errorf("a new page did not change the cached HTML: %d → %d", viewer, after)
	}
}
