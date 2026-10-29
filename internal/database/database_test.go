package database_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/athomo001/kudawiki/internal/database"
	"github.com/athomo001/kudawiki/internal/database/dbtest"
)

// Migrar dos veces no debe hacer nada la segunda vez, y Ready lo confirma.
func TestMigrateIsIdempotent(t *testing.T) {
	db := dbtest.Open(t)
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Ready(ctx); err != nil {
		t.Fatal(err)
	}
}

// Las lecturas van por mmap (SPEC §13.4): con la caché por omisión de SQLite (~2 MB por
// conexión), buscar en un área de 50 000 páginas releía el índice en cada consulta.
func TestReadPoolUsesMmap(t *testing.T) {
	db := dbtest.Open(t)
	var size int64
	if err := db.Read.QueryRow(`PRAGMA mmap_size`).Scan(&size); err != nil {
		t.Fatal(err)
	}
	if size < 1<<30 {
		t.Errorf("read pool mmap_size = %d, want at least 1 GiB", size)
	}
}

// §13.9: migrate --dry-run dice qué se aplicaría sin tocar nada, y un binario viejo se niega a
// arrancar contra una base con un esquema más nuevo que el suyo (se vuelve atrás restaurando).
func TestPendingAndNewerSchema(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(filepath.Join(t.TempDir(), "wiki.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	pending, err := db.Pending(ctx)
	if err != nil || len(pending) == 0 || pending[0] != "0001_users_sessions.sql" {
		t.Fatalf("pending on an empty db = %v, %v", pending, err)
	}
	// Mirar no aplica nada.
	if again, _ := db.Pending(ctx); len(again) != len(pending) {
		t.Errorf("Pending changed the database: %d then %d", len(pending), len(again))
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if pending, err := db.Pending(ctx); err != nil || len(pending) != 0 {
		t.Errorf("pending after migrate = %v, %v", pending, err)
	}

	if _, err := db.Write.Exec(`INSERT INTO schema_migrations (version) VALUES (9999)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pending(ctx); !errors.Is(err, database.ErrSchemaNewer) {
		t.Errorf("Pending with a newer schema: %v, want ErrSchemaNewer", err)
	}
	if err := db.Migrate(ctx); !errors.Is(err, database.ErrSchemaNewer) {
		t.Errorf("Migrate with a newer schema: %v, want ErrSchemaNewer", err)
	}
	if err := db.Ready(ctx); !errors.Is(err, database.ErrSchemaNewer) {
		t.Errorf("Ready with a newer schema: %v, want ErrSchemaNewer", err)
	}
}
