package jobs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/backup"
	"github.com/athomo001/kudawiki/internal/database/dbtest"
	"github.com/athomo001/kudawiki/internal/repo"
)

type memRemote struct {
	objects map[string][]byte
	corrupt bool
}

func (m *memRemote) Put(_ context.Context, name string, body io.Reader, size int64) error {
	b, err := io.ReadAll(body)
	if int64(len(b)) != size {
		return errors.New("size mismatch")
	}
	m.objects[name] = b
	return err
}

func (m *memRemote) Get(_ context.Context, name string) (io.ReadCloser, error) {
	b, ok := m.objects[name]
	if !ok {
		return nil, errors.New("NoSuchKey")
	}
	if m.corrupt {
		b = bytes.Clone(b)
		b[len(b)/2] ^= 1
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (m *memRemote) Key(name string) string { return "wiki/" + name }

// §13.3, §21.2.4: el respaldo diario sube una copia cifrada (no se ve el contenido), y la
// primera vez, y después una vez por semana, la baja y prueba restaurarla; el resultado queda
// en job_state para el panel. Si la copia remota está dañada, la prueba falla, queda el error y
// se avisa a los superadmins.
func TestBackupUploadAndVerify(t *testing.T) {
	ctx := context.Background()
	db := dbtest.Open(t)
	store := repo.New(db)
	rec := audit.NewRecorder(store, time.Now, slog.New(slog.DiscardHandler))
	storage, dir := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(storage, "marca.txt"), []byte("kwsecretomarca"), 0o600)
	rec.Record(ctx, audit.Event{Action: audit.BackupRun, System: "test"})
	clock := time.Date(2026, 11, 2, 3, 0, 0, 0, time.UTC)
	remote := &memRemote{objects: map[string][]byte{}}
	var notices []string
	b := Backup{DB: db.Read, StorageDir: storage, Dir: dir, Keep: backup.Retention{Daily: 7}, Audit: rec, State: store,
		Remote: remote, Key: bytes.Repeat([]byte{9}, backup.KeySize), Now: func() time.Time { return clock },
		Notify: func(_ context.Context, step, detail string) { notices = append(notices, step+": "+detail) }, Log: slog.New(slog.DiscardHandler)}

	if err := b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(remote.objects) != 1 {
		t.Fatalf("uploaded %d objects", len(remote.objects))
	}
	for name, obj := range remote.objects {
		if !strings.HasSuffix(name, ".tar.zst.enc") || bytes.Contains(obj, []byte("kwsecretomarca")) {
			t.Errorf("the offsite copy %s is not encrypted", name)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.enc")); len(left) != 0 {
		t.Errorf("the local encrypted copy was left behind: %v", left)
	}
	v, ok, _ := LastRun(ctx, store, BackupVerifyJob)
	if !ok || !v.OK || v.Source != "remote" || v.Events < 1 {
		t.Errorf("verify state: %+v", v)
	}
	if u, ok, _ := LastRun(ctx, store, BackupUploadJob); !ok || !u.OK || u.Bytes == 0 {
		t.Errorf("upload state: %+v", u)
	}

	// Al día siguiente no toca verificar; a la semana sí, y la copia remota viene dañada.
	clock = clock.Add(24 * time.Hour)
	remote.corrupt = true
	if err := b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(notices) != 0 {
		t.Fatalf("verified again before a week: %v", notices)
	}
	clock = clock.Add(VerifyEvery)
	b.Run(ctx)
	v, _, _ = LastRun(ctx, store, BackupVerifyJob)
	if v.OK || v.Error == "" || len(notices) != 1 || !strings.HasPrefix(notices[0], BackupVerifyJob+": ") {
		t.Errorf("a damaged offsite copy: state %+v, notices %v", v, notices)
	}
}

// La prueba de un respaldo local (sin destino externo) detecta una base dañada.
func TestCheckBackupDetectsDamage(t *testing.T) {
	ctx := context.Background()
	db := dbtest.Open(t)
	dir := t.TempDir()
	path, err := backup.Create(ctx, db.Read, "", dir, backup.Manual, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CheckBackup(ctx, path, nil, dir); err != nil {
		t.Fatalf("a good backup: %v", err)
	}
	raw, _ := os.ReadFile(path)
	raw[len(raw)/2] ^= 0xff
	bad := filepath.Join(dir, "bad.tar.zst")
	os.WriteFile(bad, raw, 0o600)
	if _, err := CheckBackup(ctx, bad, nil, dir); err == nil {
		t.Error("a damaged backup passed")
	}
	if _, err := CheckBackup(ctx, path+".enc", nil, dir); err == nil {
		t.Error("an .enc without a key passed")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".verify-*")); len(left) != 0 {
		t.Errorf("temporary folders left: %v", left)
	}
}
