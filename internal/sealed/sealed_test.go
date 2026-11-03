package sealed

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/crypto"
	"github.com/athomo001/kudawiki/internal/database/dbtest"
	"github.com/athomo001/kudawiki/internal/media"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/athomo001/kudawiki/internal/workspace"
	"github.com/oklog/ulid/v2"
)

// Abrir con la llave maestra exige un segundo factor configurado (§6.2). En httpapi los admins
// siempre lo tienen (la fixture lo exige); aquí, un superadmin sin 2FA ni passkeys.
func TestUnlockAsAdminNeedsSecondFactor(t *testing.T) {
	ctx := context.Background()
	store := repo.New(dbtest.Open(t))
	log := slog.New(slog.DiscardHandler)
	ws, err := workspace.NewService(store, audit.NewRecorder(store, time.Now, log), log).Create(ctx, "ti", "TI")
	if err != nil {
		t.Fatal(err)
	}
	user := func(super bool) repo.User {
		u := repo.User{ID: ulid.Make().String(), FullName: "x", PasswordHash: "x", IsGlobalAdmin: super, IsActive: true, Locale: "es"}
		u.Email = u.ID + "@empresa.cl"
		if err := store.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		return u
	}
	author, super := user(false), user(true)
	rec := audit.NewRecorder(store, time.Now, log)
	s := NewService(store, media.NewService(store, rec, t.TempDir(), 1<<20), rec, crypto.MasterKey{Key: bytes.Repeat([]byte{1}, 32), Version: 1}, log, time.Now)
	id, err := s.Create(ctx, repo.Scope{WsID: ws.ID, MaxRank: 3}, author.ID, "a", "legal/contrato", "una clave bien larga", Content{Title: "t", Markdown: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UnlockAsAdmin(ctx, super, "s", id); !errors.Is(err, ErrSecondFactor) {
		t.Fatalf("without a second factor: %v", err)
	}
	super.TOTPEnabled = true
	if err := s.UnlockAsAdmin(ctx, super, "s", id); err != nil {
		t.Fatalf("with TOTP: %v", err)
	}
	if v, err := s.Read(ctx, super, "s", id, 0); err != nil || v.Content.Markdown != "m" || v.CanEdit {
		t.Fatalf("read as admin: %+v %v", v, err)
	}
}
