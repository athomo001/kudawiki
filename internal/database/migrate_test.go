package database

import (
	"context"
	"path/filepath"
	"testing"
)

// La 0008 reconstruye pages con las claves foráneas apagadas. Si se encendieran antes de
// tiempo, su DROP TABLE borraría en cascada todas las revisiones: este test lo detecta.
func TestRebuildKeepsChildRows(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "wiki.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Write.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	ms, err := embedded()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.Write.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if m.version < 8 {
			if err := apply(ctx, conn, m); err != nil {
				t.Fatal(m.name, err)
			}
		}
	}
	conn.Close()
	for _, q := range []string{
		`INSERT INTO users (id, email, full_name, is_active, locale) VALUES ('u', 'u@x.cl', 'U', 1, 'es')`,
		`INSERT INTO workspaces (id, slug, name) VALUES ('w', 'ti', 'TI')`,
		`INSERT INTO pages (id, workspace_id, slug, title, content_markdown, created_by, updated_by) VALUES ('p', 'w', 'red', 'Red', 'x', 'u', 'u')`,
		`INSERT INTO page_revisions (id, page_id, workspace_id, revision_number, kind, title, content_markdown, created_by) VALUES ('r', 'p', 'w', 1, 'created', 'Red', 'x', 'u')`,
		`INSERT INTO page_tags (page_id, workspace_id, tag) VALUES ('p', 'w', 'red')`,
	} {
		if _, err := db.Write.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var revisions, tags, fk int
	db.Read.QueryRow(`SELECT COUNT(*) FROM page_revisions`).Scan(&revisions)
	db.Read.QueryRow(`SELECT COUNT(*) FROM page_tags`).Scan(&tags)
	if revisions != 1 || tags != 1 {
		t.Errorf("rows lost in the rebuild: %d revisions, %d tags", revisions, tags)
	}
	if db.Write.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); fk != 1 {
		t.Error("foreign keys left off after the migration")
	}
	// La cascada vuelve a funcionar contra la tabla nueva.
	if _, err := db.Write.Exec(`DELETE FROM pages WHERE id = 'p'`); err != nil {
		t.Fatal(err)
	}
	if db.Read.QueryRow(`SELECT COUNT(*) FROM page_revisions`).Scan(&revisions); revisions != 0 {
		t.Error("ON DELETE CASCADE does not reach the rebuilt table")
	}
}
