package workspace

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/database/dbtest"
	"github.com/athomo001/kudawiki/internal/mail"
	"github.com/athomo001/kudawiki/internal/repo"
)

type noMail struct{}

func (noMail) Enabled(context.Context) (bool, error)       { return false, nil }
func (noMail) Enqueue(context.Context, mail.Message) error { return nil }

// §3.5: el área por borrar se purga recién al terminar el plazo de gracia, con su carpeta en el
// disco; un borrado cancelado no se purga. La purga queda en la auditoría.
func TestPurgeAfterGrace(t *testing.T) {
	ctx := context.Background()
	r := repo.New(dbtest.Open(t))
	rec := audit.NewRecorder(r, time.Now, slog.New(slog.DiscardHandler))
	svc := NewService(r, rec, slog.New(slog.DiscardHandler))
	storage := t.TempDir()
	clock := time.Now()
	l := NewLifecycle(r, rec, noMail{}, storage, 30*24*time.Hour, func() time.Time { return clock }, slog.New(slog.DiscardHandler))

	gone, _ := svc.Create(ctx, "compras", "Compras")
	kept, _ := svc.Create(ctx, "ventas", "Ventas")
	for _, w := range []repo.Workspace{gone, kept} {
		dir := filepath.Join(storage, "ws_"+w.ID)
		os.MkdirAll(dir, 0o700)
		os.WriteFile(filepath.Join(dir, "archivo"), []byte("x"), 0o600)
		if _, err := l.RequestDelete(ctx, "", w.ID, w.Slug); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.CancelDelete(ctx, kept.ID); err != nil {
		t.Fatal(err)
	}
	if err := l.PurgeDue(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.WorkspaceByID(ctx, gone.ID); err != nil {
		t.Fatalf("purged before the grace period: %v", err)
	}
	clock = clock.Add(31 * 24 * time.Hour)
	if err := l.PurgeDue(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.WorkspaceByID(ctx, gone.ID); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("not purged: %v", err)
	}
	if _, err := os.Stat(filepath.Join(storage, "ws_"+gone.ID)); !os.IsNotExist(err) {
		t.Errorf("its folder is still there: %v", err)
	}
	if w, err := r.WorkspaceByID(ctx, kept.ID); err != nil || w.DeleteAt != nil {
		t.Errorf("a cancelled delete was purged: %+v %v", w, err)
	}
	if _, err := os.Stat(filepath.Join(storage, "ws_"+kept.ID, "archivo")); err != nil {
		t.Errorf("the kept area lost its files: %v", err)
	}
	rows, err := r.AuditEvents(ctx, repo.AuditFilter{Action: "ws.purge", Limit: 10})
	if err != nil || len(rows) != 1 {
		t.Errorf("purge audit: %d %v", len(rows), err)
	}
}
