package health

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/database"
	"github.com/athomo001/kudawiki/internal/database/dbtest"
	"github.com/athomo001/kudawiki/internal/proposals"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/athomo001/kudawiki/internal/wiki"
	"github.com/athomo001/kudawiki/internal/workspace"
)

type env struct {
	db      *database.DB
	store   *repo.Repo
	storage string
	ti, fin repo.Workspace
}

func setup(t *testing.T) env {
	t.Helper()
	ctx := context.Background()
	db := dbtest.Open(t)
	store := repo.New(db)
	rec := audit.NewRecorder(store, time.Now, slog.New(slog.DiscardHandler))
	if err := store.CreateUser(ctx, repo.User{ID: "u1", Email: "a@x.cl", FullName: "A", IsActive: true, Locale: "es"}); err != nil {
		t.Fatal(err)
	}
	ws := workspace.NewService(store, rec)
	ti, _ := ws.Create(ctx, "ti", "TI")
	fin, _ := ws.Create(ctx, "finanzas", "Finanzas")
	// u1 es admin de las dos: dueño de lo que crea, como en un área real (§4.8).
	for _, w := range []repo.Workspace{ti, fin} {
		if err := store.SetMember(ctx, repo.Scope{WsID: w.ID}, "u1", "admin"); err != nil {
			t.Fatal(err)
		}
	}
	svc := wiki.NewService(store, rec)
	save := func(w repo.Workspace, slug, md string) {
		if err := svc.Save(ctx, repo.Scope{WsID: w.ID, MaxRank: 3}, "u1", 0, repo.PageWrite{Slug: slug, Title: slug, Markdown: md}); err != nil {
			t.Fatal(err)
		}
	}
	// Un área sana: dos páginas que se enlazan entre sí.
	save(ti, "red", "Ver [[red/vpn]].")
	save(ti, "red/vpn", "Volver a [[red]].")
	save(fin, "plan", "Plan.")
	return env{db: db, store: store, storage: t.TempDir(), ti: ti, fin: fin}
}

func (e env) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := e.db.Write.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// media sube una fila y, si body no es nil, su archivo.
func (e env) media(t *testing.T, w repo.Workspace, id string, body []byte, sha string) {
	t.Helper()
	if sha == "" {
		sum := sha256.Sum256(body)
		sha = hex.EncodeToString(sum[:])
	}
	e.exec(t, `INSERT INTO media_assets (id, workspace_id, original_filename, mime_type, file_size_bytes, sha256, uploaded_by)
		VALUES (?, ?, 'x.png', 'image/png', ?, ?, 'u1')`, id, w.ID, len(body), sha)
	if body != nil {
		e.file(t, w, id, body)
	}
}

func (e env) file(t *testing.T, w repo.Workspace, name string, body []byte) {
	t.Helper()
	dir := filepath.Join(e.storage, "ws_"+w.ID)
	os.MkdirAll(dir, 0o700)
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func checks(ps []Problem) map[string]Problem {
	out := map[string]Problem{}
	for _, p := range ps {
		out[p.Check+" "+p.Page+" "+p.Detail] = p
	}
	return out
}

func TestHealthyAreaHasNoErrors(t *testing.T) {
	e := setup(t)
	e.media(t, e.ti, "M1", []byte("ok"), "")
	e.exec(t, `UPDATE media_assets SET page_id = (SELECT id FROM pages WHERE slug = 'red')`)
	ps, err := Check(context.Background(), e.store, Options{StorageDir: e.storage, Areas: []repo.Workspace{e.ti}, Hash: true, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 0 {
		t.Errorf("healthy area: %+v", ps)
	}
}

func TestEveryCheckFindsItsProblem(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	// Enlaces: uno a una ruta inexistente, otro a una página que está en la papelera.
	e.exec(t, `UPDATE pages SET content_markdown = 'x' WHERE slug = 'red'`)
	e.exec(t, `INSERT INTO page_links (workspace_id, from_page_id, to_slug) SELECT workspace_id, id, 'no/existe' FROM pages WHERE slug = 'red'`)
	e.exec(t, `INSERT INTO pages (id, workspace_id, slug, title, content_markdown, created_by, updated_by, deleted_at)
		VALUES ('P_TRASH', ?, 'vieja', 'Vieja', 'x', 'u1', 'u1', CURRENT_TIMESTAMP)`, e.ti.ID)
	e.exec(t, `INSERT INTO page_links (workspace_id, from_page_id, to_slug, to_page_id) SELECT workspace_id, id, 'vieja', 'P_TRASH' FROM pages WHERE slug = 'red/vpn'`)
	// Alias tapado: la ruta vieja la ocupa hoy otra página.
	e.exec(t, `INSERT INTO page_aliases (workspace_id, old_slug, page_id) SELECT workspace_id, 'red', (SELECT id FROM pages WHERE slug = 'red/vpn') FROM pages WHERE slug = 'red'`)
	// Árbol: una hija con menos nivel que su padre, y una ruta inválida.
	e.exec(t, `UPDATE pages SET min_rank = 3 WHERE slug = 'red'`)
	e.exec(t, `INSERT INTO pages (id, workspace_id, slug, title, content_markdown, created_by, updated_by) VALUES ('P_BAD', ?, 'Mal Slug', 'Mal', 'x', 'u1', 'u1')`, e.ti.ID)
	// Media: fila sin archivo, archivo sin fila, hash que no coincide, adjunto sin página viejo.
	e.media(t, e.ti, "M_NOFILE", nil, "abc")
	e.file(t, e.ti, "M_STRAY", []byte("suelto"))
	e.media(t, e.ti, "M_BADSHA", []byte("contenido"), "0000")
	e.media(t, e.ti, "M_OLD", []byte("viejo"), "")
	e.exec(t, `UPDATE media_assets SET created_at = datetime('now', '-30 days') WHERE id = 'M_OLD'`)
	// Búsqueda: una página sin fila en el índice, y una fila del índice sin página.
	e.exec(t, `DELETE FROM pages_fts WHERE rowid = (SELECT search_rowid FROM pages WHERE slug = 'red/vpn')`)
	e.exec(t, `INSERT INTO pages_fts (rowid, title) VALUES (99999, 'purgada')`)

	ps, err := Check(ctx, e.store, Options{StorageDir: e.storage, Areas: []repo.Workspace{e.ti, e.fin}, Global: true, Hash: true, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	got := checks(ps)
	for _, want := range []struct {
		key      string
		severity Severity
	}{
		{"links.broken red no/existe", Error},
		{"links.broken red/vpn vieja", Error},
		{"links.alias_shadowed red/vpn red", Warning},
		{"tree.level_below_parent red/vpn red", Error},
		{"tree.invalid_slug Mal Slug ", Warning},
		{"media.missing_file  M_NOFILE", Error},
		{"media.stray_file  M_STRAY", Warning},
		{"media.sha_mismatch  M_BADSHA", Error},
		{"media.unattached  M_OLD", Warning},
		{"search.page_not_indexed red/vpn ", Warning},
		{"search.orphan_index_rows  1", Error},
	} {
		p, ok := got[want.key]
		if !ok {
			t.Errorf("missing %q in %+v", want.key, ps)
			continue
		}
		if p.Severity != want.severity {
			t.Errorf("%s: severity %s, want %s", want.key, p.Severity, want.severity)
		}
		if want.key != "search.orphan_index_rows  1" && p.Area != "ti" {
			t.Errorf("%s: area %q, want ti", want.key, p.Area)
		}
	}
	// Finanzas está sana: nada suyo aparece.
	for _, p := range ps {
		if p.Area == "finanzas" {
			t.Errorf("problem reported for a healthy area: %+v", p)
		}
	}

	// Sin hash (la pantalla): el SHA-256 no se revisa, pero el tamaño sí.
	ps, _ = Check(ctx, e.store, Options{StorageDir: e.storage, Areas: []repo.Workspace{e.ti}, Now: time.Now()})
	if _, ok := checks(ps)["media.sha_mismatch  M_BADSHA"]; ok {
		t.Error("sha checked without Hash")
	}
	// Sin Global (el admin de un área): las filas del índice sin página no son de ninguna área.
	if _, ok := checks(ps)["search.orphan_index_rows  1"]; ok {
		t.Error("orphan index rows reported to an area")
	}
}

// Una página dentro de una carpeta a la que nadie enlaza está olvidada, salvo que su padre la
// liste con [[children]]: ese índice no se guarda como enlace.
func TestOrphanPages(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	svc := wiki.NewService(e.store, audit.NewRecorder(e.store, time.Now, slog.New(slog.DiscardHandler)))
	sc := repo.Scope{WsID: e.ti.ID, MaxRank: 3}
	for slug, md := range map[string]string{
		"manual":          "Índice:\n\n[[children]]\n",
		"manual/listada":  "Aparece en el índice de su padre.",
		"suelta":          "Primer nivel: se ve desde el inicio del área.",
		"carpeta/perdida": "Nadie la enlaza ni la lista.",
	} {
		if err := svc.Save(ctx, sc, "u1", 0, repo.PageWrite{Slug: slug, Title: slug, Markdown: md}); err != nil {
			t.Fatal(err)
		}
	}
	ps, err := Check(ctx, e.store, Options{StorageDir: e.storage, Areas: []repo.Workspace{e.ti}, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	var orphans []string
	for _, p := range ps {
		if p.Check == "tree.orphan" {
			orphans = append(orphans, p.Page)
		}
	}
	if len(orphans) != 1 || orphans[0] != "carpeta/perdida" {
		t.Errorf("orphans = %v, want only carpeta/perdida", orphans)
	}
}

// Propuestas (§13.7): las que pasaron el plazo de revisión y las que chocan con la versión actual
// de su página. Una que se mezcla sola, aunque la página haya avanzado, no es problema.
func TestProposalChecks(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	rec := audit.NewRecorder(e.store, time.Now, slog.New(slog.DiscardHandler))
	pages := wiki.NewService(e.store, rec)
	props := proposals.NewService(e.store, pages, rec)
	sc := repo.Scope{WsID: e.ti.ID, MaxRank: 3}
	if err := pages.Save(ctx, sc, "u1", 0, repo.PageWrite{Slug: "guia", Title: "Guía", Markdown: "uno\n\ndos\n\ntres\n"}); err != nil {
		t.Fatal(err)
	}
	propose := func(d proposals.Draft) string {
		t.Helper()
		id, err := props.Create(ctx, sc, "u1", d)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	merges := propose(proposals.Draft{Slug: "guia", Title: "Guía", Markdown: "UNO\n\ndos\n\ntres\n", Base: 1})
	clash := propose(proposals.Draft{Slug: "guia", Title: "Guía", Markdown: "uno\n\ndos\n\ntres, propuesta\n", Base: 1})
	waiting := propose(proposals.Draft{Slug: "nueva", Title: "Nueva", Markdown: "Página nueva."})
	e.exec(t, `UPDATE proposals SET created_at = datetime('now', '-4 days') WHERE id = ?`, waiting)
	// La página avanza en la última línea: choca con clash y no con merges.
	if err := pages.Save(ctx, sc, "u1", 1, repo.PageWrite{Slug: "guia", Title: "Guía", Markdown: "uno\n\ndos\n\ntres, publicada\n"}); err != nil {
		t.Fatal(err)
	}

	ps, err := Check(ctx, e.store, Options{StorageDir: e.storage, Areas: []repo.Workspace{e.ti, e.fin}, Now: time.Now(), RemindAfter: 3 * 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	got := checks(ps)
	for _, key := range []string{"proposals.waiting nueva " + waiting, "proposals.conflict guia " + clash} {
		if p, ok := got[key]; !ok || p.Severity != Warning || p.Area != "ti" {
			t.Errorf("missing %q (warning, ti) in %+v", key, ps)
		}
	}
	for _, p := range ps {
		if p.Detail == merges || (p.Check == "proposals.waiting" && p.Detail != waiting) {
			t.Errorf("unexpected %+v", p)
		}
	}
	if len(ps) != 2 {
		t.Errorf("want 2 problems, got %+v", ps)
	}
}

func TestReviewChecks(t *testing.T) {
	ctx := context.Background()
	e := setup(t)
	if err := e.store.CreateUser(ctx, repo.User{ID: "u2", Email: "b@x.cl", FullName: "B", IsActive: true, Locale: "es"}); err != nil {
		t.Fatal(err)
	}
	if err := e.store.CreateUser(ctx, repo.User{ID: "u3", Email: "c@x.cl", FullName: "C", IsActive: true, Locale: "es"}); err != nil {
		t.Fatal(err)
	}
	e.store.SetMember(ctx, repo.Scope{WsID: e.ti.ID}, "u2", "viewer")
	e.store.SetMember(ctx, repo.Scope{WsID: e.ti.ID}, "u3", "editor")
	// red: vencida (el área revisa cada 30 días y se verificó hace 40); red/vpn: su dueño es un
	// lector, que no puede editarla; guia: el dueño está desactivado. En finanzas, sin intervalo,
	// una página de hace años no vence (§19.11).
	e.exec(t, `UPDATE workspaces SET default_review_days = 30 WHERE id = ?`, e.ti.ID)
	e.exec(t, `UPDATE pages SET verified_at = datetime('now', '-40 days') WHERE slug = 'red'`)
	e.exec(t, `UPDATE pages SET owner_id = 'u2' WHERE slug = 'red/vpn'`)
	pages := wiki.NewService(e.store, audit.NewRecorder(e.store, time.Now, slog.New(slog.DiscardHandler)))
	if err := pages.Save(ctx, repo.Scope{WsID: e.ti.ID, MaxRank: 3}, "u3", 0, repo.PageWrite{Slug: "guia", Title: "Guía", Markdown: "x"}); err != nil {
		t.Fatal(err)
	}
	e.exec(t, `UPDATE users SET is_active = 0 WHERE id = 'u3'`)
	e.exec(t, `UPDATE pages SET verified_at = datetime('now', '-9 years') WHERE slug = 'plan'`)

	ps, err := Check(ctx, e.store, Options{StorageDir: e.storage, Areas: []repo.Workspace{e.ti, e.fin}, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	got := checks(ps)
	for _, key := range []string{"review.overdue red ", "review.no_owner red/vpn ", "review.owner_inactive guia "} {
		if p, ok := got[key]; !ok || p.Severity != Warning || p.Area != "ti" {
			t.Errorf("missing %q (warning, ti) in %+v", key, ps)
		}
	}
	if len(ps) != 3 {
		t.Errorf("want 3 problems, got %+v", ps)
	}
	// Verificarla la saca de la salud.
	if err := pages.Verify(ctx, repo.Scope{WsID: e.ti.ID, MaxRank: 3}, "u1", "red"); err != nil {
		t.Fatal(err)
	}
	ps, _ = Check(ctx, e.store, Options{StorageDir: e.storage, Areas: []repo.Workspace{e.ti}, Now: time.Now()})
	if _, ok := checks(ps)["review.overdue red "]; ok {
		t.Errorf("verified page still overdue: %+v", ps)
	}
}
