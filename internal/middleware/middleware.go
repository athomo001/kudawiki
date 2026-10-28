// Package middleware tiene las piezas del pipeline de SPEC §14.5 que no dependen del dominio.
package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/oklog/ulid/v2"
)

type ctxKey struct{}

// RequestID devuelve el ID del request para correlacionar logs.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Log asigna el ID del request, recupera panics y deja una línea JSON por request.
// Nunca registra el cuerpo ni la query: pueden llevar contraseñas o tokens (SPEC §13.5).
func Log(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := ulid.Make().String()
			w.Header().Set("X-Request-ID", id)
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			defer func() {
				if p := recover(); p != nil {
					logger.Error("panic", "request_id", id, "panic", p)
					sw.WriteHeader(http.StatusInternalServerError)
				}
				// El patrón de chi (/w/{ws}/…) y no la ruta real: la ruta puede llevar slugs (SPEC §13.5).
				route := "unmatched"
				if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
					route = rc.RoutePattern()
				}
				logger.Info("request", "request_id", id, "method", r.Method, "route", route,
					"status", sw.status, "duration_ms", time.Since(start).Milliseconds())
			}()
			next.ServeHTTP(sw, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
		})
	}
}

// CSP es la política estricta de SPEC §14.5: sin 'unsafe-inline' y sin orígenes externos.
// Con nonce, solo los <style> que lo llevan pasan: es lo que usa el editor (CodeMirror) y lo
// que usará la marca de la empresa (§15.1.2).
func CSP(nonce string) string {
	style := "'self'"
	if nonce != "" {
		style += " 'nonce-" + nonce + "'"
	}
	return "default-src 'self'; script-src 'self'; style-src " + style + "; " +
		"img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; " +
		"base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
}

// SecurityHeaders aplica la CSP y las demás cabeceras de SPEC §14.5.
func SecurityHeaders(https bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", CSP(""))
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "same-origin")
			h.Set("X-Robots-Tag", "noindex") // wiki interna: nunca indexable (SPEC §20.4)
			if https {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CSRF rechaza todo método que cambia estado si no viene del propio sitio (SPEC §8.2):
// Sec-Fetch-Site cuando el navegador lo manda, y si no, Origin contra base_url.
func CSRF(origin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
			default:
				site := r.Header.Get("Sec-Fetch-Site")
				if (site != "" && site != "same-origin") || (site == "" && r.Header.Get("Origin") != origin) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusForbidden)
					w.Write([]byte(`{"error":{"code":"csrf","message":"cross-site request rejected"}}`))
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
