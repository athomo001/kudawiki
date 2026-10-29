package database_test

import (
	"context"
	"testing"

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
