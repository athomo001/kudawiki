package httpapi

import (
	"net/http"
	"slices"
	"testing"
	"time"
)

// A6 (§3.3.3): los 404 uniformes tampoco se distinguen por el tiempo. Área inexistente y área sin
// membresía hacen la misma consulta; página inexistente y página de un nivel que no alcanza,
// también. Se comparan las medianas de tandas de 20 pedidos, intercaladas (una sola respuesta
// queda por debajo de la resolución del reloj en Windows): una diferencia que se pueda medir
// desde afuera (más de 1 ms y más de la mitad de la mediana) delataría qué existe.
func TestUniform404Timing(t *testing.T) {
	if testing.Short() {
		t.Skip("timing")
	}
	f := newFixture(t)
	f.expect(t, "adminTI", "PUT", "/api/w/ti/pages/n3", `{"title":"N3","markdown":"x","base_revision":0}`, http.StatusNoContent)
	f.expect(t, "adminTI", "PATCH", "/api/w/ti/meta/n3", `{"level":3}`, http.StatusNoContent)
	for _, pair := range []struct {
		name     string
		who      string
		exists   string
		notExist string
	}{
		{"area", "adminFin", "/api/w/ti/tree", "/api/w/no-existe/tree"},
		{"page", "viewerTI", "/api/w/ti/pages/n3", "/api/w/ti/pages/no-existe"},
	} {
		var a, b []time.Duration
		for i := 0; i < 40; i++ {
			for j, path := range []string{pair.exists, pair.notExist} {
				start := time.Now()
				for k := 0; k < 20; k++ {
					if w := f.do(pair.who, "GET", path, ""); w.Code != http.StatusNotFound {
						t.Fatalf("%s %s: %d", pair.who, path, w.Code)
					}
				}
				d := time.Since(start) / 20
				if j == 0 {
					a = append(a, d)
				} else {
					b = append(b, d)
				}
			}
		}
		ma, mb := median(a), median(b)
		diff := ma - mb
		if diff < 0 {
			diff = -diff
		}
		t.Logf("%s: hidden %v, missing %v", pair.name, ma, mb)
		if diff > time.Millisecond && diff > max(ma, mb)/2 {
			t.Errorf("%s: the 404s differ in time: %v vs %v", pair.name, ma, mb)
		}
	}
}

func median(ds []time.Duration) time.Duration {
	s := slices.Clone(ds)
	slices.Sort(s)
	return s[len(s)/2]
}
