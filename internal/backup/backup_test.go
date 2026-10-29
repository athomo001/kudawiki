package backup

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/athomo001/kudawiki/internal/database"
	"github.com/athomo001/kudawiki/internal/database/dbtest"
	"github.com/klauspost/compress/zstd"
)

// fixture: una base migrada con una fila y un storage/ con dos archivos.
func fixture(t *testing.T) (db *database.DB, storage, dir string) {
	t.Helper()
	db = dbtest.Open(t)
	if _, err := db.Write.Exec(`INSERT INTO users (id, email, full_name, is_active, locale) VALUES ('u1', 'ana@empresa.cl', 'Ana', 1, 'es')`); err != nil {
		t.Fatal(err)
	}
	storage = t.TempDir()
	write(t, filepath.Join(storage, "ws1", "a.png"), "imagen")
	write(t, filepath.Join(storage, "ws1", "thumbs", "a.png"), "miniatura")
	return db, storage, t.TempDir()
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCreateAndVerify(t *testing.T) {
	db, storage, dir := fixture(t)
	now := time.Date(2026, 10, 29, 3, 0, 0, 0, time.UTC)
	path, err := Create(context.Background(), db.Read, storage, dir, Daily, now)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "kudawiki-daily-20261029-030000.tar.zst" {
		t.Errorf("name = %s", filepath.Base(path))
	}
	m, err := Verify(path)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range m.Files {
		names = append(names, f.Path)
	}
	slices.Sort(names)
	if strings.Join(names, ",") != "storage/ws1/a.png,storage/ws1/thumbs/a.png,wiki.db" {
		t.Errorf("files = %v", names)
	}
	if m.Kind != Daily || m.Schema != database.Latest() || !m.CreatedAt.Equal(now) {
		t.Errorf("manifest = %+v", m)
	}
	// No quedan temporales a medio escribir.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("backup dir has %d entries, want only the archive", len(entries))
	}

	// Un byte cambiado en el archivo no pasa la verificación.
	raw, _ := os.ReadFile(path)
	raw[len(raw)/2] ^= 0xFF
	bad := filepath.Join(t.TempDir(), "bad.tar.zst")
	os.WriteFile(bad, raw, 0o600)
	if _, err := Verify(bad); err == nil {
		t.Error("a corrupted archive passed Verify")
	}
}

// El respaldo previo a migrar lleva solo la base (§13.9): al restaurarlo, storage/ no se toca.
func TestPreMigrateHasOnlyTheDatabase(t *testing.T) {
	db, _, dir := fixture(t)
	path, err := Create(context.Background(), db.Read, "", dir, PreMigrate, time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.Base(path), "kudawiki-pre-migrate-") {
		t.Errorf("name = %s", filepath.Base(path))
	}
	m, err := Verify(path)
	if err != nil || len(m.Files) != 1 || m.Files[0].Path != "wiki.db" {
		t.Errorf("pre-migrate manifest = %+v, %v", m, err)
	}
}

// Restaurar en una máquina nueva (carpetas vacías) y sobre datos existentes, que se apartan.
func TestRestore(t *testing.T) {
	db, storage, dir := fixture(t)
	path, err := Create(context.Background(), db.Read, storage, dir, Manual, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	target := t.TempDir()
	dbPath, storageDir := filepath.Join(target, "data", "wiki.db"), filepath.Join(target, "storage")
	if _, _, err := Restore(path, dbPath, storageDir, database.Latest()); err != nil {
		t.Fatal(err)
	}
	restored, err := database.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	var name string
	restored.Read.QueryRow(`SELECT full_name FROM users WHERE id = 'u1'`).Scan(&name)
	restored.Close()
	if name != "Ana" {
		t.Errorf("restored user = %q", name)
	}
	if body, _ := os.ReadFile(filepath.Join(storageDir, "ws1", "thumbs", "a.png")); string(body) != "miniatura" {
		t.Errorf("restored file = %q", body)
	}

	// Otra vez sobre lo mismo: lo que había queda aparte, no se borra.
	write(t, filepath.Join(storageDir, "ws1", "nuevo.png"), "posterior al respaldo")
	if _, _, err := Restore(path, dbPath, storageDir, database.Latest()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(storageDir, "ws1", "nuevo.png")); !errors.Is(err, os.ErrNotExist) {
		t.Error("storage was not replaced by the backup")
	}
	kept, _ := filepath.Glob(filepath.Join(target, "before-restore-*", "storage", "ws1", "nuevo.png"))
	if len(kept) != 1 {
		t.Error("the previous storage was not kept aside")
	}
}

// Un respaldo de un binario más nuevo no se restaura con uno más viejo (§13.9).
func TestRestoreRejectsNewerSchema(t *testing.T) {
	db, storage, dir := fixture(t)
	if _, err := db.Write.Exec(`INSERT INTO schema_migrations (version) VALUES (9999)`); err != nil {
		t.Fatal(err)
	}
	path, err := Create(context.Background(), db.Read, storage, dir, Manual, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if _, _, err := Restore(path, filepath.Join(target, "wiki.db"), filepath.Join(target, "storage"), database.Latest()); !errors.Is(err, database.ErrSchemaNewer) {
		t.Errorf("restore of a newer schema: %v", err)
	}
}

// Un archivo armado a mano con una ruta que sale de la carpeta no escribe nada afuera, aunque
// su manifiesto cuadre (zip slip).
func TestRestoreRejectsPathsOutside(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "evil.tar.zst")
	f, _ := os.Create(path)
	zw, _ := zstd.NewWriter(f)
	tw := tar.NewWriter(zw)
	body := []byte("x")
	sum := sha256.Sum256(body)
	m := Manifest{Format: FormatVersion, Kind: Manual, Files: []File{{Path: "storage/../../evil.txt", Size: 1, SHA256: hex.EncodeToString(sum[:])}}}
	tw.WriteHeader(&tar.Header{Name: "storage/../../evil.txt", Mode: 0o600, Size: 1, Typeflag: tar.TypeReg})
	tw.Write(body)
	mb, _ := json.Marshal(m)
	tw.WriteHeader(&tar.Header{Name: manifestName, Mode: 0o600, Size: int64(len(mb)), Typeflag: tar.TypeReg})
	tw.Write(mb)
	tw.Close()
	zw.Close()
	f.Close()

	target := filepath.Join(t.TempDir(), "a", "b")
	if _, _, err := Restore(path, filepath.Join(target, "data", "wiki.db"), filepath.Join(target, "storage"), database.Latest()); err == nil {
		t.Error("restore accepted a path outside the data dir")
	}
	if _, err := os.Stat(filepath.Join(target, "evil.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Error("a file was written outside storage/")
	}
}

// Retención (§13.3): 7 diarios, 4 semanales y 6 mensuales; los manuales y los previos a
// migrar no se tocan.
func TestPrune(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 29, 3, 0, 0, 0, time.UTC)
	for d := 0; d < 400; d++ {
		write(t, filepath.Join(dir, Name(Daily, now.AddDate(0, 0, -d))), "")
	}
	write(t, filepath.Join(dir, Name(Manual, now.AddDate(-2, 0, 0))), "")
	write(t, filepath.Join(dir, Name(PreMigrate, now.AddDate(-2, 0, 0))), "")

	removed, err := Prune(dir, Retention{Daily: 7, Weekly: 4, Monthly: 6})
	if err != nil {
		t.Fatal(err)
	}
	left, _ := os.ReadDir(dir)
	var daily []string
	for _, e := range left {
		if strings.HasPrefix(e.Name(), "kudawiki-daily-") {
			daily = append(daily, e.Name())
		}
	}
	// Los 7 días más recientes, más los de semanas y meses anteriores que no se pisan con ellos.
	if len(daily) < 7 || len(daily) > 7+4+6 || len(removed)+len(daily) != 400 {
		t.Errorf("kept %d daily, removed %d", len(daily), len(removed))
	}
	if !slices.Contains(daily, Name(Daily, now)) || !slices.Contains(daily, Name(Daily, now.AddDate(0, 0, -6))) {
		t.Error("the last 7 days are not all kept")
	}
	if slices.Contains(daily, Name(Daily, now.AddDate(0, -8, 0))) {
		t.Error("a backup older than 6 months was kept")
	}
	if len(left)-len(daily) != 2 {
		t.Error("manual or pre-migrate backups were pruned")
	}
}

// Restaurar con el servidor corriendo rompería la base: InUse lo detecta aunque el servidor
// esté en otro proceso o en otro contenedor sobre el mismo volumen.
func TestInUse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "wiki.db")
	if used, err := InUse(path); err != nil || used {
		t.Errorf("missing db: %v, %v", used, err)
	}
	db, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if used, err := InUse(path); err != nil || !used {
		t.Errorf("open db: in use = %v, %v", used, err)
	}
	db.Close()
	if used, err := InUse(path); err != nil || used {
		t.Errorf("closed db: in use = %v, %v", used, err)
	}
}
