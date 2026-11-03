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
// búsqueda y su media en disco; la media huérfana se borra pasado un día, salvo la que usa un
// borrador (el detalle, en TestUnusedMedia); las búsquedas sin resultados duran 90 días y los
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
	if n := count(`SELECT COUNT(*) FROM media_assets WHERE id = '` + orphan.ID + `'`); n != 0 {
		t.Error("orphan media older than a day not deleted")
	}
	if _, err := os.Stat(filepath.Join(dir, "ws_"+ws.ID, orphan.ID)); !os.IsNotExist(err) {
		t.Error("orphan media file still on disk")
	}
	if n := count(`SELECT COUNT(*) FROM media_assets WHERE id = '` + drafted.ID + `' AND deleted_at IS NULL`); n != 1 {
		t.Error("media used by a draft deleted")
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

}

// La media que nadie usa se borra al día (§4.4, decisión del dueño del 2026-11-02): la que nunca
// llegó a una página y la que se sacó de una página cuando ya ninguna revisión guardada la
// muestra. Usarla en el texto o en una revisión de cualquier página del área, en un borrador,
// una propuesta abierta, una plantilla, un anuncio o un cuestionario la mantiene. La que alguien
// borró también dura un día; la de una página en la papelera sigue el plazo de la página.
func TestUnusedMedia(t *testing.T) {
	ctx := context.Background()
	db := dbtest.Open(t)
	store := repo.New(db)
	dir := t.TempDir()
	rec := audit.NewRecorder(store, time.Now, slog.New(slog.DiscardHandler))
	files := media.NewService(store, rec, dir, 1<<20)
	svc := wiki.NewService(store, rec)
	uid := ulid.Make().String()
	if err := store.CreateUser(ctx, repo.User{ID: uid, Email: "e@x.cl", FullName: "E", IsActive: true, Locale: "es"}); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Write.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	// "corta" guarda solo la última revisión: lo que se sacó del texto deja de estar en uso.
	scopes := map[string]repo.Scope{}
	for _, slug := range []string{"ti", "corta"} {
		ws := repo.Workspace{ID: ulid.Make().String(), Slug: slug, Name: slug}
		if err := store.CreateWorkspace(ctx, ws); err != nil {
			t.Fatal(err)
		}
		scopes[slug] = repo.Scope{WsID: ws.ID, MaxRank: 3}
	}
	exec(`UPDATE workspaces SET revisions_keep_last = 1 WHERE slug = 'corta'`)
	ids := map[string]string{}
	upload := func(area, name string) string {
		t.Helper()
		m, err := files.Upload(ctx, scopes[area], uid, name+".txt", strings.NewReader(name))
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = m.ID
		return "![" + name + "](/api/w/" + area + "/media/" + m.ID + ")"
	}
	save := func(area, slug string, base int, md string) {
		t.Helper()
		if err := svc.Save(ctx, scopes[area], uid, base, repo.PageWrite{Slug: slug, Title: slug, Markdown: md}); err != nil {
			t.Fatal(err)
		}
	}
	ti := scopes["ti"].WsID

	upload("ti", "nueva")   // de hace un rato: todavía no
	upload("ti", "suelta")  // nunca se usó
	upload("ti", "borrada") // alguien la borró hace dos días
	upload("ti", "recien")  // alguien la borró hoy
	save("ti", "actual", 0, upload("ti", "actual"))
	save("ti", "historial", 0, upload("ti", "historial"))
	save("ti", "historial", 1, "sin la imagen") // la revisión 1 la sigue mostrando
	save("ti", "papelera", 0, upload("ti", "papelera"))
	if err := svc.Delete(ctx, scopes["ti"], uid, "papelera"); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveDraft(ctx, scopes["ti"], uid, "borrador", repo.Draft{Title: "b", Markdown: upload("ti", "borrador")}); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO proposals (id, workspace_id, page_id, target_slug, base_revision, title, content_markdown, lang, author_id, status)
		  SELECT 'prop', workspace_id, id, slug, 1, 'P', ?, 'es', ?, 'pending' FROM pages WHERE slug = 'actual'`, upload("ti", "propuesta"), uid)
	exec(`INSERT INTO page_templates (id, workspace_id, name, content_markdown, created_by) VALUES ('tpl', ?, 'T', ?, ?)`, ti, upload("ti", "plantilla"), uid)
	exec(`INSERT INTO announcements (id, workspace_id, title, body_markdown, created_by) VALUES ('ann', ?, 'A', ?, ?)`, ti, upload("ti", "anuncio"), uid)
	exec(`INSERT INTO quizzes (id, workspace_id, page_id, title, draft_json, created_by)
		  SELECT 'quiz', workspace_id, id, 'Q', ?, ? FROM pages WHERE slug = 'actual'`, `{"questions":[{"prompt":"`+upload("ti", "cuestionario")+`"}]}`, uid)
	// En "corta", la que se sacó del texto se va; la que otra página sigue mostrando, no.
	save("corta", "a", 0, upload("corta", "sacada")+" "+upload("corta", "compartida"))
	save("corta", "a", 1, "sin imágenes")
	save("corta", "b", 0, "![c](/api/w/corta/media/"+ids["compartida"]+")")

	for name, id := range ids {
		if name != "nueva" {
			exec(`UPDATE media_assets SET created_at = datetime('now', '-2 days') WHERE id = ?`, id)
		}
	}
	exec(`UPDATE media_assets SET deleted_at = datetime('now', '-2 days') WHERE id = ?`, ids["borrada"])
	exec(`UPDATE media_assets SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?`, ids["recien"])
	exec(`UPDATE pages SET deleted_at = datetime('now', '-5 days') WHERE slug = 'papelera'`)

	purge := Purge{Store: store, Files: files, Audit: rec, Retention: 30 * 24 * time.Hour, AuditRetention: 730 * 24 * time.Hour,
		Now: time.Now, Log: slog.New(slog.DiscardHandler)}
	if err := purge.Run(ctx); err != nil {
		t.Fatal(err)
	}
	gone := map[string]bool{"suelta": true, "borrada": true, "sacada": true}
	for name, id := range ids {
		var rows int
		if err := db.Read.QueryRow(`SELECT COUNT(*) FROM media_assets WHERE id = ?`, id).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		area := "ti"
		if name == "sacada" || name == "compartida" {
			area = "corta"
		}
		_, err := os.Stat(filepath.Join(dir, "ws_"+scopes[area].WsID, id))
		switch {
		case gone[name] && (rows != 0 || !os.IsNotExist(err)):
			t.Errorf("%s: unused media not deleted (rows %d, file err %v)", name, rows, err)
		case !gone[name] && (rows != 1 || err != nil):
			t.Errorf("%s: media in use deleted (rows %d, file err %v)", name, rows, err)
		}
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

// §13.10: vencen las sesiones (por inactividad o edad, §8.2), las notificaciones leídas a los 90
// días, lo visto para el aviso de login nuevo al año y los intentos de cuestionario y aperturas
// según quizzes.retention_days. Lo que no venció, o el intento en curso, no se toca.
func TestPurgePersonal(t *testing.T) {
	ctx := context.Background()
	db := dbtest.Open(t)
	store := repo.New(db)
	uid := ulid.Make().String()
	if err := store.CreateUser(ctx, repo.User{ID: uid, Email: "e@x.cl", FullName: "E", IsActive: true, Locale: "es"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	old, recent := now.Add(-800*24*time.Hour), now.Add(-time.Hour)
	for i, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO sessions (token_hash, id, user_id, created_at, last_seen_at) VALUES (?, ?, ?, ?, ?)`, []any{"a", "a", uid, old, old}},
		{`INSERT INTO sessions (token_hash, id, user_id, created_at, last_seen_at) VALUES (?, ?, ?, ?, ?)`, []any{"b", "b", uid, recent, recent}},
		{`INSERT INTO login_history (user_id, ip, browser, last_at) VALUES (?, '1.1.1.1', 'x', ?)`, []any{uid, old}},
		{`INSERT INTO login_history (user_id, ip, browser, last_at) VALUES (?, '2.2.2.2', 'x', ?)`, []any{uid, recent}},
	} {
		if _, err := db.Write.Exec(q.sql, q.args...); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	rec := audit.NewRecorder(store, time.Now, slog.New(slog.DiscardHandler))
	p := Purge{Store: store, Files: media.NewService(store, rec, t.TempDir(), 1<<20), Audit: rec, Retention: 30 * 24 * time.Hour,
		AuditRetention: 730 * 24 * time.Hour, QuizRetention: 730 * 24 * time.Hour, Now: time.Now, Log: slog.New(slog.DiscardHandler)}
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	for table, want := range map[string]int{"sessions": 1, "login_history": 1} {
		var n int
		db.Read.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n)
		if n != want {
			t.Errorf("%s: %d rows left, want %d", table, n, want)
		}
	}
	events, _ := store.AuditEvents(ctx, repo.AuditFilter{Action: "privacy.retention", Limit: 5})
	if len(events) != 1 {
		t.Errorf("retention audit: %+v", events)
	}
}
