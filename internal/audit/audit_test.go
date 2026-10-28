package audit

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/athomo001/kudawiki/internal/database"
	"github.com/athomo001/kudawiki/internal/database/dbtest"
	"github.com/athomo001/kudawiki/internal/repo"
)

func chainOf(t *testing.T, n int) (*database.DB, *repo.Repo) {
	t.Helper()
	db := dbtest.Open(t)
	r := repo.New(db)
	rec := NewRecorder(r, time.Now, slog.New(slog.DiscardHandler))
	ctx := WithActor(context.Background(), Actor{ID: "u1", Email: "a@x.cl", Name: "Ana", IP: "10.0.0.1"})
	for i := range n {
		rec.Record(ctx, Event{Action: PageUpdate, WsID: "w", TargetType: "page", TargetID: "p", Meta: map[string]any{"revision": i + 1}})
	}
	return db, r
}

func exec(t *testing.T, db *database.DB, qs ...string) {
	t.Helper()
	for _, q := range qs {
		if _, err := db.Write.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
}

func TestVerifyAcceptsAnIntactChain(t *testing.T) {
	_, r := chainOf(t, 5)
	rep, err := Verify(context.Background(), r)
	if err != nil || rep.Events != 5 || rep.FirstID != 1 || rep.LastID != 5 {
		t.Fatalf("got %+v, %v", rep, err)
	}
}

// Seudonimizar a alguien reescribe la copia del actor, que está fuera del hash (§11.2, §13.10).
func TestPseudonymizingKeepsTheChain(t *testing.T) {
	db, r := chainOf(t, 3)
	exec(t, db, `UPDATE audit_log SET actor_snapshot_json = '{"name":"persona-1"}'`)
	if _, err := Verify(context.Background(), r); err != nil {
		t.Fatal(err)
	}
}

// Lo que la cadena tiene que detectar, saltándose los triggers como lo haría alguien con
// acceso a la BD.
func TestVerifyDetectsTampering(t *testing.T) {
	for name, tc := range map[string]struct {
		sql  string
		want int64
	}{
		"modified": {`UPDATE audit_log SET meta_json = '{"revision":99}' WHERE id = 3`, 3},
		"removed":  {`DELETE FROM audit_log WHERE id = 3`, 4},
		"inserted": {`INSERT INTO audit_log (id, at, action, prev_hash, hash) SELECT 10, at, action, prev_hash, hash FROM audit_log WHERE id = 2;
		              UPDATE audit_log SET id = id + 100 WHERE id IN (3, 4, 5)`, 10},
		"first": {`UPDATE audit_log SET prev_hash = hash WHERE id = 1`, 1},
	} {
		t.Run(name, func(t *testing.T) {
			db, r := chainOf(t, 5)
			exec(t, db, `DROP TRIGGER audit_no_update`, `DROP TRIGGER audit_retention_floor`, tc.sql)
			_, err := Verify(context.Background(), r)
			var broken BrokenError
			if !errors.As(err, &broken) || broken.ID != tc.want {
				t.Errorf("got %v, want broken at %d", err, tc.want)
			}
		})
	}
}

// La BD misma impide cambiar una fila o borrar una de los últimos 180 días (§11.2).
func TestTriggersProtectTheLog(t *testing.T) {
	db, _ := chainOf(t, 2)
	for _, q := range []string{
		`UPDATE audit_log SET action = 'page.read' WHERE id = 1`,
		`DELETE FROM audit_log WHERE id = 1`,
	} {
		if _, err := db.Write.Exec(q); err == nil {
			t.Errorf("%s: want an error", q)
		}
	}
	// Lo que ya pasó el piso sí se puede borrar (la retención).
	exec(t, db, `DROP TRIGGER audit_no_update`, `UPDATE audit_log SET at = '2020-01-01T00:00:00.000000Z' WHERE id = 1`,
		`DELETE FROM audit_log WHERE id = 1`)
}

func TestSanitizeMetaRedactsSecrets(t *testing.T) {
	got := sanitizeMeta(map[string]any{"revision": 2, "new_password": "x", "api_key": "y", "changed": []string{"smtp.host"}})
	if got["new_password"] != "[redacted]" || got["api_key"] != "[redacted]" || got["revision"] != 2 {
		t.Errorf("got %v", got)
	}
}

// Cada acción de la lista cerrada se muestra traducida en la interfaz, en los dos idiomas.
func TestEveryActionHasATranslation(t *testing.T) {
	for _, lang := range []string{"es", "en"} {
		raw, err := os.ReadFile("../../ui/src/lib/i18n/" + lang + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var keys map[string]string
		if err := json.Unmarshal(raw, &keys); err != nil {
			t.Fatal(err)
		}
		for _, a := range All {
			if keys["audit."+a.String()] == "" {
				t.Errorf("%s.json missing audit.%s", lang, a)
			}
		}
	}
}
