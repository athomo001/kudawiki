package jobs

import (
	"context"
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
// después; las búsquedas sin resultados duran 90 días. Lo que no venció no se toca.
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
		`UPDATE media_assets SET created_at = datetime('now', '-8 days') WHERE id = '` + orphan.ID + `'`,
		`UPDATE search_misses SET day = date('now', '-91 days')`,
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
	if n := count(`SELECT COUNT(*) FROM search_misses`); n != 0 {
		t.Error("search misses older than 90 days not purged")
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
