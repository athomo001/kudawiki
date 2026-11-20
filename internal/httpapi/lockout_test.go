package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

// QA-04 (§8.2): el 3.er fallo ya responde 429 con la espera, en el encabezado y en el cuerpo,
// para que la pantalla de login diga cuánto falta.
func TestLoginLockoutTellsTheWait(t *testing.T) {
	f := newFixture(t)
	bad := `{"email":"viewerTI@empresa.cl","password":"mala-clave-123"}`
	for range 2 {
		if w := f.do("", "POST", "/api/auth/login", bad); w.Code != http.StatusUnauthorized {
			t.Fatalf("before the 3rd: %d %s", w.Code, w.Body)
		}
	}
	w := f.do("", "POST", "/api/auth/login", bad)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "60" ||
		!strings.Contains(w.Body.String(), `"code":"rate_limited"`) || !strings.Contains(w.Body.String(), `"retry_after":60`) {
		t.Errorf("3rd: %d %q %s", w.Code, w.Header().Get("Retry-After"), w.Body)
	}
}
