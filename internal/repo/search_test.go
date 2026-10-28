package repo

import (
	"strings"
	"testing"

	"github.com/athomo001/kudawiki/internal/database/dbtest"
)

// La búsqueda tiene que recorrer primero el índice FTS5. Si el plan empieza por pages, la
// búsqueda FTS5 se ejecuta una vez por página y con 50 000 páginas tarda minutos (§9.5).
func TestSearchPlanStartsWithFTS(t *testing.T) {
	db := dbtest.Open(t)
	// Igual con el orden por relevancia que por fecha (§9.4).
	for _, byDate := range []bool{false, true} {
		query, args := searchSQL(Scope{WsID: "w", MaxRank: 3},
			SearchQuery{Match: `"x"`, Path: "a", Tags: []string{"t"}, Author: "y", ByDate: byDate}, 200)
		rows, err := db.Read.Query(`EXPLAIN QUERY PLAN `+query, args...)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		if len(plan) == 0 || !strings.HasPrefix(plan[0], "SCAN f VIRTUAL TABLE") {
			t.Errorf("byDate=%v: search plan must start with the FTS5 index, got:\n%s", byDate, strings.Join(plan, "\n"))
		}
	}
}
