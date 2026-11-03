package backup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestList(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 11, 3, 3, 0, 0, 0, time.UTC)
	for _, name := range []string{
		Name(Daily, at), Name(Manual, at.Add(time.Hour)), Name(PreReset, at.Add(2*time.Hour)), Name(PreMigrate, at.Add(-time.Hour)),
		// Lo que no es un respaldo nuestro no se lista.
		"kudawiki-daily-roto.tar.zst", "kudawiki-otro-20261103-030000.tar.zst", "notas.txt",
		Name(Daily, at) + EncSuffix, ".tmp-kudawiki-daily-20261103-030000.tar.zst",
	} {
		os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600)
	}
	os.Mkdir(filepath.Join(dir, "kudawiki-daily-20261104-030000.tar.zst"), 0o700)

	got, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []Kind{PreReset, Manual, Daily, PreMigrate}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i, e := range got {
		if e.Kind != want[i] || e.Size != 1 {
			t.Errorf("%d: %+v, want kind %s", i, e, want[i])
		}
	}
	if !got[2].CreatedAt.Equal(at) {
		t.Errorf("created at %v", got[2].CreatedAt)
	}

	if got, err := List(filepath.Join(dir, "no-existe")); err != nil || got != nil {
		t.Errorf("missing dir: %v %v", got, err)
	}
}

// Una carpeta de respaldos que en realidad es un archivo (mal configurada o pisada) no tumba la
// pantalla: no hay respaldos que listar, como si no existiera. En Linux esto es ENOTDIR.
func TestListNotADirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "backups")
	if err := os.WriteFile(file, []byte("no soy una carpeta"), 0o600); err != nil {
		t.Fatal(err)
	}
	if list, err := List(file); err != nil || len(list) != 0 {
		t.Errorf("List(file) = %v, %v", list, err)
	}
}
