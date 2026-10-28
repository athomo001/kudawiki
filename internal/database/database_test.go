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
