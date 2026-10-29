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

// Buscar los padres de una página corre en cada guardado. El único índice por ruta es parcial
// (sin las páginas de la papelera, 0008): si la consulta no lo dice, SQLite recorre el área
// entera y guardar se vuelve más lento cuanto más grande es el área. Ninguna parte de la
// consulta puede buscar solo por área.
func TestAncestorLookupUsesSlugIndex(t *testing.T) {
	db := dbtest.Open(t)
	query, args := ancestorRankSQL(Scope{WsID: "w", MaxRank: 3}, "a/b/c")
	rows, err := db.Read.Query(`EXPLAIN QUERY PLAN `+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	joined := strings.Join(plan, "\n")
	if !strings.Contains(joined, "pages_ws_slug") || strings.Contains(joined, "(workspace_id=?)") || strings.Contains(joined, "SCAN pages") {
		t.Errorf("ancestor lookup must not walk the whole area, got:\n%s", joined)
	}
}

// Contar etiquetas y autores de los candidatos (§9.4) tiene que empezar por la lista de
// candidatos, no por las páginas del área: con 50 000 páginas era la diferencia entre 0,3 ms y
// más de un segundo por búsqueda.
func TestSearchMetaStartsWithCandidates(t *testing.T) {
	db := dbtest.Open(t)
	tags, authors, args := searchMetaSQL(Scope{WsID: "w", MaxRank: 3}, []string{"a", "b", "c"})
	for name, q := range map[string]string{"tags": tags, "authors": authors} {
		rows, err := db.Read.Query(`EXPLAIN QUERY PLAN `+q, args...)
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
		joined := strings.Join(plan, "\n")
		if strings.Contains(joined, "pages_ws_rank") || strings.Contains(joined, "SCAN p") {
			t.Errorf("%s must start with the candidates, got:\n%s", name, joined)
		}
	}
}
