package wiki_test

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/database/dbtest"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/athomo001/kudawiki/internal/search"
	"github.com/athomo001/kudawiki/internal/wiki"
	"github.com/oklog/ulid/v2"
)

// Prueba de carga de SPEC §9.5: p95 < 50 ms por búsqueda con 50 000 páginas en un área.
// Tarda minutos, así que solo corre con KUDAWIKI_LOAD=1.
func TestSearchLoad(t *testing.T) {
	if os.Getenv("KUDAWIKI_LOAD") == "" {
		t.Skip("KUDAWIKI_LOAD=1 siembra 50 000 páginas y mide la búsqueda (§9.5)")
	}
	const pages, queries = 50_000, 300
	ctx := context.Background()
	r := repo.New(dbtest.Open(t))
	uid := ulid.Make().String()
	ws := repo.Workspace{ID: ulid.Make().String(), Slug: "carga", Name: "Carga"}
	if err := r.CreateUser(ctx, repo.User{ID: uid, Email: "c@x.cl", FullName: "C", IsActive: true, Locale: "es"}); err != nil {
		t.Fatal(err)
	}
	if err := r.CreateWorkspace(ctx, ws); err != nil {
		t.Fatal(err)
	}
	sc := repo.Scope{WsID: ws.ID, MaxRank: 3}
	svc := wiki.NewService(r, audit.NewRecorder(r, time.Now, slog.New(slog.DiscardHandler)))

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

	start := time.Now()
	for i := 0; i < pages; i++ {
		md := "## " + words(3) + "\n\n" + words(120) + "\n\n## " + words(2) + "\n\n" + words(60)
		w := repo.PageWrite{Slug: fmt.Sprintf("s%d/p%d", i/200, i), Title: words(4), Markdown: md}
		if err := svc.Save(ctx, sc, uid, 0, w); err != nil {
			t.Fatal(err)
		}
		if n := i + 1; n%5000 == 0 { // progreso visible: la siembra tarda minutos
			elapsed := time.Since(start)
			t.Logf("%d/%d páginas (%s, faltan ~%s)", n, pages, elapsed.Round(time.Second),
				(elapsed / time.Duration(n) * time.Duration(pages-n)).Round(time.Second))
		}
	}
	t.Logf("sembradas %d páginas en %s", pages, time.Since(start).Round(time.Second))

	var took []time.Duration
	for i := 0; i < queries; i++ {
		q := base[rng.IntN(len(base))]
		switch i % 3 {
		case 1:
			q += " " + base[rng.IntN(len(base))]
		case 2:
			q = string([]rune(q)[:min(5, len([]rune(q)))]) // prefijo (las palabras cortas quedan enteras)
		}
		parsed, err := search.Parse(q)
		if err != nil {
			t.Fatal(err)
		}
		t0 := time.Now()
		// Lo mismo que hace la primera página de resultados: candidatos, fragmentos y conteos.
		hits, err := r.Search(ctx, sc, parsed, 200)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Snippets(ctx, sc, parsed, hits[:min(len(hits), 20)]); err != nil {
			t.Fatal(err)
		}
		ids := make([]string, len(hits))
		for j, h := range hits {
			ids[j] = h.ID
		}
		if _, _, err := r.SearchMeta(ctx, sc, ids); err != nil {
			t.Fatal(err)
		}
		took = append(took, time.Since(t0))
		if n := i + 1; n%50 == 0 { // progreso visible también en esta fase
			t.Logf("%d/%d búsquedas (la última tardó %s)", n, queries, took[len(took)-1].Round(time.Millisecond))
		}
	}
	slices.Sort(took)
	p50, p95 := took[len(took)/2], took[len(took)*95/100]
	t.Logf("búsquedas: p50 %s, p95 %s, máx %s", p50, p95, took[len(took)-1])
	if p95 > 50*time.Millisecond {
		t.Errorf("p95 = %s, objetivo < 50 ms (§9.5)", p95)
	}
}
