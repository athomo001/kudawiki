// Package dbtest abre una BD real en un directorio temporal: el dominio se prueba
// contra SQLite, sin mocks de repo (SPEC §16.2). Solo lo importan los tests.
package dbtest

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/athomo001/kudawiki/internal/database"
)

func Open(t testing.TB) *database.DB {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "wiki.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db
}
