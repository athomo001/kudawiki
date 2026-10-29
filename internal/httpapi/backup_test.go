package httpapi

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/backup"
	"github.com/athomo001/kudawiki/internal/database"
	"github.com/athomo001/kudawiki/internal/repo"
)

// Criterio de salida de F3 (FASES.md): un respaldo restaurado en otra máquina tiene el mismo
// contenido y su cadena de auditoría pasa la verificación (§13.3, §11.1).
func TestRestoredBackupPassesAuditVerify(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	// Actividad real: crea una página, la edita, cambia su nivel. Todo queda en la auditoría.
	f.do("editorTI", "PUT", "/api/w/ti/pages/manual", `{"title":"Manual","markdown":"uno","base_revision":0}`)
	f.do("editorTI", "PUT", "/api/w/ti/pages/manual", `{"title":"Manual","markdown":"dos","base_revision":1}`)
	f.do("adminTI", "PATCH", "/api/w/ti/meta/manual", `{"level":2}`)
	before, err := audit.Verify(ctx, repo.New(f.db))
	if err != nil || before.Events == 0 {
		t.Fatalf("source chain: %+v, %v", before, err)
	}

	path, err := backup.Create(ctx, f.db.Read, t.TempDir(), t.TempDir(), backup.Manual, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// "Otra máquina": carpetas vacías.
	other := t.TempDir()
	dbPath := filepath.Join(other, "data", "wiki.db")
	if _, _, err := backup.Restore(path, dbPath, filepath.Join(other, "storage"), database.Latest()); err != nil {
		t.Fatal(err)
	}
	restored, err := database.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err := restored.Ready(ctx); err != nil {
		t.Fatalf("restored db not ready: %v", err)
	}
	after, err := audit.Verify(ctx, repo.New(restored))
	if err != nil || after != before {
		t.Errorf("restored chain: %+v, %v; want %+v", after, err, before)
	}
	var markdown string
	restored.Read.QueryRow(`SELECT content_markdown FROM pages WHERE slug = 'manual'`).Scan(&markdown)
	if markdown != "dos" {
		t.Errorf("restored page = %q", markdown)
	}
}
