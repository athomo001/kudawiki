package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// §14.5: límite general por IP, un balde que se rellena con el tiempo. Pasado el límite, 429
// con Retry-After y el formato de error de la API; otra IP no se entera.
func TestRateLimit(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	h := RateLimit(Limit{PerSecond: 2, Burst: 3}, func() time.Time { return now })(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	do := func(ip string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/x", nil)
		r.RemoteAddr = ip + ":5000"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for i := 0; i < 3; i++ {
		if w := do("10.0.0.1"); w.Code != http.StatusNoContent {
			t.Fatalf("request %d within the burst: %d", i, w.Code)
		}
	}
	w := do("10.0.0.1")
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "1" || w.Body.String() != `{"error":{"code":"rate_limited","message":"Too Many Requests"}}` {
		t.Errorf("over the limit: %d, Retry-After %q, %s", w.Code, w.Header().Get("Retry-After"), w.Body)
	}
	if w := do("10.0.0.2"); w.Code != http.StatusNoContent {
		t.Errorf("another IP: %d", w.Code)
	}
	// Medio segundo después hay una ficha nueva (2 por segundo), no dos.
	now = now.Add(500 * time.Millisecond)
	if w := do("10.0.0.1"); w.Code != http.StatusNoContent {
		t.Errorf("after refill: %d", w.Code)
	}
	if w := do("10.0.0.1"); w.Code != http.StatusTooManyRequests {
		t.Errorf("refill gave more than one token: %d", w.Code)
	}
}
