// Package metrics es /metrics en el formato de texto de Prometheus (SPEC §13.5), escrito a mano:
// son pocos valores y no justifican una dependencia. Solo responde a las direcciones de
// metrics.allow_cidrs; las demás reciben el 404 de siempre. Nada por área: ni títulos, ni rutas,
// ni conteos de un área (son de negocio y quedan fuera, §13.5).
package metrics

import (
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync/atomic"
	"time"
)

// Collector cuenta los requests y arma la respuesta.
type Collector struct {
	allow    []netip.Prefix
	requests [6]atomic.Int64 // por clase de código: 1xx… 5xx (0 sin uso)
	micros   atomic.Int64
	count    atomic.Int64
}

// New lee los rangos permitidos ("10.0.0.0/8", "192.168.1.10/32"). Sin rangos, /metrics no
// responde a nadie.
func New(cidrs []string) (*Collector, error) {
	c := &Collector{}
	for _, s := range cidrs {
		p, err := netip.ParsePrefix(strings.TrimSpace(s))
		if err != nil {
			return nil, fmt.Errorf("metrics.allow_cidrs: %w", err)
		}
		c.allow = append(c.allow, p.Masked())
	}
	return c, nil
}

// Allowed dice si la dirección puede leer las métricas.
func (c *Collector) Allowed(ip string) bool {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	a = a.Unmap()
	return slices.ContainsFunc(c.allow, func(p netip.Prefix) bool { return p.Contains(a) })
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Middleware cuenta cada request por la clase de su código y suma su duración.
func (c *Collector) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(sw, r)
		if class := sw.status / 100; class >= 1 && class <= 5 {
			c.requests[class].Add(1)
		}
		c.micros.Add(time.Since(start).Microseconds())
		c.count.Add(1)
	})
}

// Sample es un valor que se lee al momento de responder.
type Sample struct {
	Name, Help, Type string // Type: gauge o counter
	Labels           string // ya formateadas: `kind="daily"`
	Value            float64
}

// Write escribe las métricas propias y las de samples.
func (c *Collector) Write(w io.Writer, samples []Sample) {
	fmt.Fprintln(w, "# HELP kudawiki_http_requests_total HTTP requests by status class.")
	fmt.Fprintln(w, "# TYPE kudawiki_http_requests_total counter")
	for class := 1; class <= 5; class++ {
		fmt.Fprintf(w, "kudawiki_http_requests_total{code=\"%dxx\"} %d\n", class, c.requests[class].Load())
	}
	fmt.Fprintln(w, "# HELP kudawiki_http_request_duration_seconds Time spent answering HTTP requests.")
	fmt.Fprintln(w, "# TYPE kudawiki_http_request_duration_seconds summary")
	fmt.Fprintf(w, "kudawiki_http_request_duration_seconds_sum %g\n", float64(c.micros.Load())/1e6)
	fmt.Fprintf(w, "kudawiki_http_request_duration_seconds_count %d\n", c.count.Load())
	seen := map[string]bool{}
	for _, s := range samples {
		if !seen[s.Name] {
			seen[s.Name] = true
			fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", s.Name, s.Help, s.Name, s.Type)
		}
		if s.Labels != "" {
			fmt.Fprintf(w, "%s{%s} %g\n", s.Name, s.Labels, s.Value)
		} else {
			fmt.Fprintf(w, "%s %g\n", s.Name, s.Value)
		}
	}
}
