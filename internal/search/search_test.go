package search

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestParseFilters(t *testing.T) {
	q, err := Parse(`vpn tag:any:red,wifi path:/redes/ autor:diego desde:2026-01-01`)
	if err != nil {
		t.Fatal(err)
	}
	if !q.TagsAny || len(q.Tags) != 2 || q.Path != "redes" || q.Author != "diego" || q.Since.IsZero() || len(q.Words) != 1 {
		t.Errorf("filters: %+v", q)
	}
	if q, _ := Parse("tag:vpn"); q.Match != "" || len(q.Tags) != 1 {
		t.Errorf("filter-only: %+v", q)
	}
}

// Un valor con espacios va entre comillas: autor:"Ana Soto", tag:"mesa de ayuda" (§9.4, filtros).
func TestParseQuotedFilterValue(t *testing.T) {
	q, err := Parse(`vpn autor:"Ana Soto" tag:"mesa de ayuda"`)
	if err != nil {
		t.Fatal(err)
	}
	if q.Author != "Ana Soto" || len(q.Tags) != 1 || q.Tags[0] != "mesa de ayuda" || len(q.Words) != 1 || q.Words[0] != "vpn" {
		t.Errorf("quoted filters: %+v", q)
	}
	// Una comilla sin cerrar abarca hasta el final, igual que en una frase.
	if q, _ := Parse(`autor:"Ana Soto`); q.Author != "Ana Soto" {
		t.Errorf("unclosed quote: %+v", q)
	}
}

// Lo que escribe la persona nunca llega crudo a MATCH: comillas, * y operadores quedan como texto (§9.3).
func TestParseNeverPassesRawSyntax(t *testing.T) {
	for _, in := range []string{`vpn"`, `"vpn`, `vpn*`, `NEAR(a b)`, `a AND`, `{title}:x`, `x" OR "y`} {
		q, err := Parse(in)
		if err != nil && !errors.Is(err, ErrEmptyQuery) {
			t.Errorf("Parse(%q): %v", in, err)
		}
		if strings.Contains(q.Match, "NEAR(") || strings.Contains(q.Match, `{title}`) {
			t.Errorf("Parse(%q) leaked syntax: %s", in, q.Match)
		}
	}
	for _, in := range []string{"", "a", "-vpn", "  "} {
		if _, err := Parse(in); !errors.Is(err, ErrEmptyQuery) {
			t.Errorf("Parse(%q) = %v, want ErrEmptyQuery", in, err)
		}
	}
}

func TestStem(t *testing.T) {
	if got := Stem("Procedimientos de conexión", "es"); got != "proced de conexion" && got != "proced de conexión" {
		t.Errorf("Stem es = %q", got)
	}
}

// Ninguna entrada de la caja arma una consulta que FTS5 rechace (sería un 500): se prueba
// contra una tabla FTS5 real con las mismas columnas que pages_fts.
func FuzzParseRunsInFTS5(f *testing.F) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		f.Fatal(err)
	}
	if _, err := db.Exec(`CREATE VIRTUAL TABLE pages_fts USING fts5(title, tags, headings, description, content_text, code_text,
		title_stem, headings_stem, description_stem, content_stem, tokenize = 'unicode61 remove_diacritics 2', prefix = '2 3')`); err != nil {
		f.Fatal(err)
	}
	for _, s := range []string{"cliente oficial", `"red de invitados" -wifi`, "vpn OR wifi OR", `a" b`, "tag:x y", "NEAR(a b)", "-a -b c"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		q, err := Parse(in)
		if err != nil || q.Match == "" {
			return
		}
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM pages_fts WHERE pages_fts MATCH ?`, q.Match).Scan(&n); err != nil {
			t.Fatalf("Parse(%q) = %q: %v", in, q.Match, err)
		}
	})
}
