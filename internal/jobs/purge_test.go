package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/database/dbtest"
	"github.com/athomo001/kudawiki/internal/media"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/athomo001/kudawiki/internal/wiki"
	"github.com/oklog/ulid/v2"
)

// La purga borra de verdad (§4.5): la página vencida con sus revisiones, su fila en la
// búsqueda y su media en disco; la media huérfana pasa a la papelera a los 7 días y se purga
// después, salvo la que usa un borrador; las búsquedas sin resultados duran 90 días y los
// borradores, 30 sin tocarlos. Lo que no venció no se toca.
func TestPurge(t *testing.T) {
	ctx := context.Background()
	db := dbtest.Open(t)
	store := repo.New(db)
	dir := t.TempDir()
	rec := audit.NewRecorder(store, time.Now, slog.New(slog.DiscardHandler))
	files := media.NewService(store, rec, dir, 1<<20)
	uid := ulid.Make().String()
	if err := store.CreateUser(ctx, repo.User{ID: uid, Email: "e@x.cl", FullName: "E", IsActive: true, Locale: "es"}); err != nil {
		t.Fatal(err)
	}
	ws := repo.Workspace{ID: ulid.Make().String(), Slug: "ti", Name: "TI"}
	if err := store.CreateWorkspace(ctx, ws); err != nil {
		t.Fatal(err)
	}
	sc := repo.Scope{WsID: ws.ID, MaxRank: 3}
	used, err := files.Upload(ctx, sc, uid, "a.txt", strings.NewReader("hola"))
	if err != nil {
		t.Fatal(err)
	}
	orphan, err := files.Upload(ctx, sc, uid, "b.txt", strings.NewReader("chao"))
	if err != nil {
		t.Fatal(err)
	}
	// La imagen pegada en un borrador que todavía no se publica no es huérfana (§4.11).
	drafted, err := files.Upload(ctx, sc, uid, "c.txt", strings.NewReader("borrador"))
	if err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"abandonado", "en-curso"} {
		if err := store.SaveDraft(ctx, sc, uid, slug, repo.Draft{Title: slug, Markdown: "![c](/api/w/ti/media/" + drafted.ID + ")"}); err != nil {
			t.Fatal(err)
		}
	}
	svc := wiki.NewService(store, rec)
	for _, slug := range []string{"vieja", "reciente"} {
		md := "x"
		if slug == "vieja" {
			md = "![a](/api/w/ti/media/" + used.ID + ")"
		}
		if err := svc.Save(ctx, sc, uid, 0, repo.PageWrite{Slug: slug, Title: slug, Markdown: md}); err != nil {
			t.Fatal(err)
		}
		if err := svc.Delete(ctx, sc, uid, slug); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.RecordMiss(ctx, sc, "antigua"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`UPDATE pages SET deleted_at = datetime('now', '-31 days') WHERE slug = 'vieja'`,
		`UPDATE media_assets SET created_at = datetime('now', '-8 days') WHERE id IN ('` + orphan.ID + `', '` + drafted.ID + `')`,
		// Borradores (§4.11): el que nadie toca hace 31 días se va; el de hoy se queda.
		`UPDATE page_drafts SET updated_at = datetime('now', '-31 days') WHERE target_slug = 'abandonado'`,
		`UPDATE search_misses SET day = date('now', '-91 days')`,
		// Visitas (§4.17): una de hace 91 días se va y la de hoy se queda.
		`INSERT INTO page_views (page_id, workspace_id, day) SELECT id, workspace_id, date('now', '-91 days') FROM pages WHERE slug = 'reciente'`,
		`INSERT INTO page_views (page_id, workspace_id, day) SELECT id, workspace_id, date('now') FROM pages WHERE slug = 'reciente'`,
		// Correos (§12): el entregado hace 31 días se va; el de ayer y el pendiente se quedan.
		`INSERT INTO email_outbox (id, template, next_attempt_at, sent_at) VALUES ('viejo', 'invite', datetime('now', '-31 days'), datetime('now', '-31 days'))`,
		`INSERT INTO email_outbox (id, template, next_attempt_at, failed_at) VALUES ('ayer', 'invite', datetime('now', '-1 days'), datetime('now', '-1 days'))`,
		`INSERT INTO email_outbox (id, template, payload_enc, key_version, next_attempt_at) VALUES ('pendiente', 'invite', x'00', 1, datetime('now', '-40 days'))`,
	} {
		if _, err := db.Write.Exec(q); err != nil {
			t.Fatal(err)
		}
	}

	purge := Purge{Store: store, Files: files, Audit: rec, Retention: 30 * 24 * time.Hour, AuditRetention: 730 * 24 * time.Hour,
		Now: time.Now, Log: slog.New(slog.DiscardHandler)}
	if err := purge.Run(ctx); err != nil {
		t.Fatal(err)
	}
	count := func(q string) (n int) {
		t.Helper()
		if err := db.Read.QueryRow(q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(`SELECT COUNT(*) FROM pages WHERE slug = 'vieja'`); n != 0 {
		t.Error("expired page not purged")
	}
	if n := count(`SELECT COUNT(*) FROM pages WHERE slug = 'reciente'`); n != 1 {
		t.Error("page purged before its time")
	}
	if n := count(`SELECT COUNT(*) FROM page_revisions r JOIN pages p ON p.id = r.page_id`); n != count(`SELECT COUNT(*) FROM page_revisions`) {
		t.Error("revisions of the purged page left behind")
	}
	if n := count(`SELECT COUNT(*) FROM pages_fts`); n != 1 {
		t.Errorf("search index has %d rows, want 1", n)
	}
	if _, err := os.Stat(filepath.Join(dir, "ws_"+ws.ID, used.ID)); !os.IsNotExist(err) {
		t.Error("media file of the purged page is still on disk")
	}
	if n := count(`SELECT COUNT(*) FROM media_assets WHERE id = '` + orphan.ID + `' AND deleted_at IS NOT NULL`); n != 1 {
		t.Error("orphan media older than 7 days not moved to the trash")
	}
	if n := count(`SELECT COUNT(*) FROM media_assets WHERE id = '` + drafted.ID + `' AND deleted_at IS NULL`); n != 1 {
		t.Error("media used by a draft moved to the trash")
	}
	if n := count(`SELECT COUNT(*) FROM page_drafts WHERE target_slug = 'en-curso'`); n != 1 || count(`SELECT COUNT(*) FROM page_drafts`) != 1 {
		t.Error("drafts: want only the one untouched for 30 days purged")
	}
	if n := count(`SELECT COUNT(*) FROM search_misses`); n != 0 {
		t.Error("search misses older than 90 days not purged")
	}
	if n := count(`SELECT COUNT(*) FROM page_views WHERE day = date('now')`); n != 1 || count(`SELECT COUNT(*) FROM page_views`) != 1 {
		t.Error("page views: want only today's left after the purge")
	}
	if n := count(`SELECT COUNT(*) FROM email_outbox WHERE id IN ('ayer', 'pendiente')`); n != 2 || count(`SELECT COUNT(*) FROM email_outbox`) != 2 {
		t.Error("email outbox: want only the old delivered email purged")
	}
	// Lo hizo el sistema: sin actor y con meta.system (§11.2).
	if n := count(`SELECT COUNT(*) FROM audit_log WHERE action = 'page.purge' AND actor_id IS NULL AND meta_json = '{"system":"daily_purge"}'`); n != 1 {
		t.Errorf("%d page.purge events by the system, want 1", n)
	}

	// Pasado el plazo, la huérfana que está en la papelera también se va del disco.
	purge.Now = func() time.Time { return time.Now().Add(31 * 24 * time.Hour) }
	if err := purge.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ws_"+ws.ID, orphan.ID)); !os.IsNotExist(err) {
		t.Error("orphan media file not purged after the retention period")
	}
}

// La retención del historial (§4.3): con "las últimas 2" quedan las dos más nuevas de cada
// página, más la base de una propuesta abierta; con "30 días", las de ese plazo y la actual.
// Un área sin retención no pierde nada. Queda en la auditoría, una vez por área.
func TestRevisionRetention(t *testing.T) {
	ctx := context.Background()
	db := dbtest.Open(t)
	store := repo.New(db)
	rec := audit.NewRecorder(store, time.Now, slog.New(slog.DiscardHandler))
	uid := ulid.Make().String()
	if err := store.CreateUser(ctx, repo.User{ID: uid, Email: "e@x.cl", FullName: "E", IsActive: true, Locale: "es"}); err != nil {
		t.Fatal(err)
	}
	svc := wiki.NewService(store, rec)
	scopes := map[string]repo.Scope{}
	for _, slug := range []string{"ultimas", "dias", "todas"} {
		ws := repo.Workspace{ID: ulid.Make().String(), Slug: slug, Name: slug}
		if err := store.CreateWorkspace(ctx, ws); err != nil {
			t.Fatal(err)
		}
		sc := repo.Scope{WsID: ws.ID, MaxRank: 3}
		scopes[slug] = sc
		for n := range 5 {
			if err := svc.Save(ctx, sc, uid, n, repo.PageWrite{Slug: "p", Title: "P", Markdown: strings.Repeat("x", n+1)}); err != nil {
				t.Fatal(err)
			}
		}
	}
	revisions := func(slug string) (out []int) {
		t.Helper()
		rows, err := db.Read.Query(`SELECT r.revision_number FROM page_revisions r JOIN workspaces w ON w.id = r.workspace_id
			WHERE w.slug = ? ORDER BY r.revision_number`, slug)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var n int
			rows.Scan(&n)
			out = append(out, n)
		}
		return out
	}
	page := func(slug string) (id string) {
		db.Read.QueryRow(`SELECT p.id FROM pages p JOIN workspaces w ON w.id = p.workspace_id WHERE w.slug = ?`, slug).Scan(&id)
		return id
	}
	for _, q := range []string{
		`UPDATE workspaces SET revisions_keep_last = 2 WHERE slug = 'ultimas'`,
		`UPDATE workspaces SET revisions_keep_days = 30 WHERE slug = 'dias'`,
		// Una propuesta pendiente escrita sobre la revisión 2: sin ella no se puede mezclar.
		`INSERT INTO proposals (id, workspace_id, page_id, target_slug, base_revision, title, content_markdown, lang, author_id, status)
		 VALUES ('prop', '` + scopes["ultimas"].WsID + `', '` + page("ultimas") + `', 'p', 2, 'P', 'y', 'es', '` + uid + `', 'pending')`,
		`UPDATE page_revisions SET created_at = datetime('now', '-40 days') WHERE workspace_id IN ('` + scopes["dias"].WsID + `', '` + scopes["todas"].WsID + `')`,
		`UPDATE page_revisions SET created_at = datetime('now', '-1 days') WHERE workspace_id = '` + scopes["dias"].WsID + `' AND revision_number = 4`,
	} {
		if _, err := db.Write.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	purge := Purge{Store: store, Files: media.NewService(store, rec, t.TempDir(), 1<<20), Audit: rec, Retention: 30 * 24 * time.Hour,
		AuditRetention: 730 * 24 * time.Hour, Now: time.Now, Log: slog.New(slog.DiscardHandler)}
	if err := purge.Run(ctx); err != nil {
		t.Fatal(err)
	}
	for slug, want := range map[string]string{"ultimas": "[2 4 5]", "dias": "[4 5]", "todas": "[1 2 3 4 5]"} {
		if got := fmt.Sprint(revisions(slug)); got != want {
			t.Errorf("%s: revisions %s, want %s", slug, got, want)
		}
	}
	var events int
	db.Read.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'revision.purge' AND meta_json LIKE '%"count":%'`).Scan(&events)
	if events != 2 {
		t.Errorf("%d revision.purge events, want 2 (one per area)", events)
	}
}
