package httpapi

import (
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// Prueba de carga de SPEC §9.5, de punta a punta por la API: se guardan 50 000 páginas en un
// área con PUT y se busca con GET /search, igual que la interfaz (candidatos, fragmentos,
// sección y conteos de los filtros). Mide las dos cosas, porque guardar también tiene que
// aguantar un área grande: un guardado que crece con el tamaño del área no se ve buscando.
// Tarda minutos: solo corre con KUDAWIKI_LOAD=1 (make load). El avance sale en vivo con -v, y
// el resultado queda en docs/rendimiento.md aunque la prueba pase.
func TestSearchLoad(t *testing.T) {
	if os.Getenv("KUDAWIKI_LOAD") == "" {
		t.Skip("KUDAWIKI_LOAD=1 siembra 50 000 páginas y mide guardar y buscar (§9.5); make load")
	}
	const pages, batch, queries = 50_000, 5_000, 300
	f := newFixture(t)

	// Vocabulario: palabras reales con sus variantes, más relleno, para que haya coincidencias
	// en todos los campos y bm25 tenga que ordenar.
	base := []string{"procedimiento", "conexión", "servidor", "respaldo", "usuario", "contraseña", "instalación",
		"configuración", "red", "impresora", "correo", "seguridad", "acceso", "permiso", "reunión", "factura"}
	rng := rand.New(rand.NewPCG(1, 2))
	vocab := slices.Clone(base)
	for i := 0; i < 3000; i++ {
		vocab = append(vocab, fmt.Sprintf("palabra%dx", i))
	}
	words := func(n int) string {
		w := make([]string, n)
		for i := range w {
			w[i] = vocab[rng.IntN(len(vocab))]
		}
		return strings.Join(w, " ")
	}

	// Guardar: cada tanda de 5 000 con su p50 y p95, para ver si crece con el área.
	type row struct {
		upTo     int
		p50, p95 time.Duration
	}
	var saves []row
	var seeding time.Duration
	// Rondas de búsqueda: la primera es la base tal como queda, que es lo que vive la wiki; las
	// otras miden qué gana el mantenimiento que el SPEC deja para F8 (checkpoint del WAL, §13.4).
	type round struct {
		label           string
		p50, p95, worst time.Duration
		walBytes        int64
	}
	var rounds []round
	// El reporte se escribe al final pase lo que pase: t.Fatal también corre los defer.
	defer func() {
		status := "pasó"
		if t.Failed() {
			status = "falló (ver la salida de make load)"
		}
		var b strings.Builder
		fmt.Fprintf(&b, "# Rendimiento: guardar y buscar en un área grande (SPEC §9.5)\n\n")
		fmt.Fprintf(&b, "Generado por `make load` (`internal/httpapi/load_test.go`). Se reescribe en cada corrida.\n\n")
		fmt.Fprintf(&b, "- Resultado: **%s**\n- Fecha: %s\n- Equipo: %s/%s, %d CPU, %s\n- Área: %d páginas de ~190 palabras; %d búsquedas por ronda, por la API\n- Sembrado: %s\n\n",
			status, time.Now().Format("2006-01-02 15:04"), runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.Version(),
			pages, queries, seeding.Round(time.Second))
		if len(rounds) > 0 {
			fmt.Fprintf(&b, "## Buscar (objetivo: p95 < 50 ms, sobre la primera ronda)\n\n| Ronda | p50 | p95 | máx | WAL antes |\n| :--- | ---: | ---: | ---: | ---: |\n")
			for _, r := range rounds {
				fmt.Fprintf(&b, "| %s | %s | %s | %s | %.1f MB |\n", r.label, ms(r.p50), ms(r.p95), ms(r.worst), float64(r.walBytes)/1e6)
			}
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "## Guardar, por tanda (no debe crecer con el área)\n\n| Páginas | p50 | p95 |\n| ---: | ---: | ---: |\n")
		for _, s := range saves {
			fmt.Fprintf(&b, "| %d | %s | %s |\n", s.upTo, ms(s.p50), ms(s.p95))
		}
		if err := os.WriteFile(filepath.Join("..", "..", "docs", "rendimiento.md"), []byte(b.String()), 0o644); err != nil {
			t.Errorf("write report: %v", err)
			return
		}
		t.Logf("reporte en docs/rendimiento.md (%s)", status)
	}()
	start := time.Now()
	took := make([]time.Duration, 0, batch)
	for i := 0; i < pages; i++ {
		md := "## " + words(3) + "\\n\\n" + words(120) + "\\n\\n## " + words(2) + "\\n\\n" + words(60)
		body := `{"title":"` + words(4) + `","markdown":"` + md + `","base_revision":0}`
		t0 := time.Now()
		if w := f.do("editorTI", "PUT", fmt.Sprintf("/api/w/ti/pages/s%d/p%d", i/200, i), body); w.Code != http.StatusNoContent {
			t.Fatalf("save %d: %d %s", i, w.Code, w.Body)
		}
		took = append(took, time.Since(t0))
		if n := i + 1; n%batch == 0 {
			p50, p95, _ := percentiles(took)
			saves = append(saves, row{n, p50, p95})
			took = took[:0]
			elapsed := time.Since(start)
			t.Logf("%d/%d páginas (%s, faltan ~%s) · guardar: p50 %s, p95 %s", n, pages, elapsed.Round(time.Second),
				(elapsed / time.Duration(n) * time.Duration(pages-n)).Round(time.Second), ms(p50), ms(p95))
		}
	}
	seeding = time.Since(start)

	// Las mismas consultas en cada ronda, para comparar.
	qs := make([]string, queries)
	for i := range qs {
		q := base[rng.IntN(len(base))]
		switch i % 3 {
		case 1:
			q += " " + base[rng.IntN(len(base))]
		case 2:
			q = string([]rune(q)[:min(5, len([]rune(q)))]) // prefijo (las palabras cortas quedan enteras)
		}
		qs[i] = q
	}
	var dbPath string
	if err := f.db.Read.QueryRow(`SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&dbPath); err != nil {
		t.Fatal(err)
	}
	walSize := func() int64 {
		st, err := os.Stat(dbPath + "-wal")
		if err != nil {
			return 0
		}
		return st.Size()
	}
	measure := func(label string) round {
		r := round{label: label, walBytes: walSize()}
		took := make([]time.Duration, 0, queries)
		for i, q := range qs {
			t0 := time.Now()
			if w := f.do("editorTI", "GET", "/api/w/ti/search?q="+url.QueryEscape(q), ""); w.Code != http.StatusOK {
				t.Fatalf("%s: search %q: %d %s", label, q, w.Code, w.Body)
			}
			took = append(took, time.Since(t0))
			if n := i + 1; n%100 == 0 {
				t.Logf("%s: %d/%d búsquedas (la última tardó %s)", label, n, queries, ms(took[len(took)-1]))
			}
		}
		r.p50, r.p95, r.worst = percentiles(took)
		t.Logf("%s: p50 %s, p95 %s, máx %s (WAL %.1f MB)", label, ms(r.p50), ms(r.p95), ms(r.worst), float64(r.walBytes)/1e6)
		rounds = append(rounds, r)
		return r
	}

	asIs := measure("Tal como queda")
	if _, err := f.db.Write.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	measure("Después de vaciar el WAL")
	if _, err := f.db.Write.Exec(`INSERT INTO pages_fts(pages_fts) VALUES('optimize')`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Write.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	measure("Y con el índice FTS compactado")

	if asIs.p95 > 50*time.Millisecond {
		t.Errorf("buscar: p95 = %s, objetivo < 50 ms (§9.5)", ms(asIs.p95))
	}
	// Guardar en un área de 50 000 páginas no puede costar varias veces lo que costaba vacía.
	if first, last := saves[0].p50, saves[len(saves)-1].p50; last > 4*first {
		t.Errorf("guardar crece con el área: p50 %s en la primera tanda y %s en la última", ms(first), ms(last))
	}
}

func percentiles(d []time.Duration) (p50, p95, worst time.Duration) {
	s := slices.Clone(d)
	slices.Sort(s)
	return s[len(s)/2], s[len(s)*95/100], s[len(s)-1]
}

func ms(d time.Duration) string { return fmt.Sprintf("%.1f ms", float64(d.Microseconds())/1000) }
