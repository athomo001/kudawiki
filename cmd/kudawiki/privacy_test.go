package main

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"log/slog"
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

// §13.10: el ZIP de datos personales trae el perfil, lo que hizo según la auditoría y su espacio
// personal, y nunca un secreto (el hash de la contraseña ni el de la sesión).
func TestWritePersonal(t *testing.T) {
	ctx := context.Background()
	db := dbtest.Open(t)
	store := repo.New(db)
	uid := ulid.Make().String()
	if err := store.CreateUser(ctx, repo.User{ID: uid, Email: "ana@x.cl", FullName: "Ana", PasswordHash: "secreto-hash", IsActive: true, Locale: "es"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.Exec(`INSERT INTO sessions (token_hash, id, user_id, created_at, last_seen_at, ip) VALUES ('token-hash', 's1', ?, ?, ?, '10.0.0.1')`,
		uid, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	ws, err := store.EnsurePersonal(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	rec := audit.NewRecorder(store, time.Now, slog.New(slog.DiscardHandler))
	actx := audit.WithActor(ctx, audit.Actor{ID: uid, Email: "ana@x.cl", Name: "Ana"})
	if err := wiki.NewService(store, rec).Save(actx, repo.Scope{WsID: ws.ID, MaxRank: 3}, uid, 0, repo.PageWrite{Slug: "notas", Title: "Notas", Markdown: "mías"}); err != nil {
		t.Fatal(err)
	}
	u, _ := store.UserByEmail(ctx, "ana@x.cl")
	var buf bytes.Buffer
	if err := writePersonal(ctx, store, media.NewService(store, rec, t.TempDir(), 1<<20), u, &buf); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		all.Write(b)
	}
	for _, want := range []string{"LEEME.txt", "profile.json", "sessions.json", "audit.json", "personal-space.zip"} {
		if !names[want] {
			t.Errorf("missing %s in %v", want, names)
		}
	}
	text := all.String()
	if !strings.Contains(text, "ana@x.cl") || !strings.Contains(text, "10.0.0.1") || !strings.Contains(text, "page.create") {
		t.Errorf("content incomplete")
	}
	if strings.Contains(text, "secreto-hash") || strings.Contains(text, "token-hash") {
		t.Error("a secret leaked into the export")
	}
}
