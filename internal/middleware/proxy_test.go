package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// §21.2.1: con trust_proxy la IP es la última de X-Forwarded-For (la que puso el proxy) y lo
// que llegó al proxy por http se redirige a base_url. Sin trust_proxy, las cabeceras no valen.
func TestProxy(t *testing.T) {
	var seen string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = r.RemoteAddr })
	do := func(trust bool, set func(h http.Header)) *httptest.ResponseRecorder {
		seen = ""
		r := httptest.NewRequest("GET", "/api/x?y=1", nil)
		r.RemoteAddr = "10.0.0.9:4000"
		set(r.Header)
		w := httptest.NewRecorder()
		Proxy(trust, "https://wiki.empresa.cl")(next).ServeHTTP(w, r)
		return w
	}

	do(true, func(h http.Header) { h.Add("X-Forwarded-For", "1.1.1.1, 203.0.113.7") })
	if seen != "203.0.113.7:0" {
		t.Errorf("trusted proxy: RemoteAddr = %q, want the last hop 203.0.113.7", seen)
	}
	do(true, func(h http.Header) { h.Add("X-Forwarded-For", "1.1.1.1"); h.Add("X-Forwarded-For", "2001:db8::1") })
	if seen != "[2001:db8::1]:0" {
		t.Errorf("two headers: RemoteAddr = %q, want the last one", seen)
	}
	do(true, func(h http.Header) { h.Set("X-Forwarded-For", "no-es-una-ip") })
	if seen != "10.0.0.9:4000" {
		t.Errorf("garbage header: RemoteAddr = %q, want the connection", seen)
	}
	do(false, func(h http.Header) { h.Set("X-Forwarded-For", "203.0.113.7") })
	if seen != "10.0.0.9:4000" {
		t.Errorf("without trust_proxy the header must be ignored, got %q", seen)
	}

	w := do(true, func(h http.Header) { h.Set("X-Forwarded-Proto", "http") })
	if w.Code != http.StatusPermanentRedirect || w.Header().Get("Location") != "https://wiki.empresa.cl/api/x?y=1" || seen != "" {
		t.Errorf("http behind the proxy: %d %q (handler ran: %v)", w.Code, w.Header().Get("Location"), seen != "")
	}
	if w := do(false, func(h http.Header) { h.Set("X-Forwarded-Proto", "http") }); w.Code != http.StatusOK {
		t.Errorf("without trust_proxy X-Forwarded-Proto must be ignored, got %d", w.Code)
	}
}
