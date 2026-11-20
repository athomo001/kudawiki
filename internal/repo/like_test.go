package repo

import (
	"testing"

	"github.com/athomo001/kudawiki/internal/database/dbtest"
)

// QA-15: en los filtros autor: y ruta:, % y _ de lo que escribe la persona son texto, no comodines.
// Se prueba con el LIKE … ESCAPE de SQLite, el mismo que arma searchSQL.
func TestLikeEscape(t *testing.T) {
	db := dbtest.Open(t)
	for _, c := range []struct {
		value, typed string
		want         bool
	}{
		{"Ana Pérez", "%", false}, // antes: todos
		{"100% Ana", "%", true},   // un % de verdad sigue encontrándose
		{"axb/c", "a_b", false},   // antes: _ valía por cualquier letra
		{"a_b/c", "a_b", true},
		{`a\b/c`, `a\b`, true}, // la barra misma también es texto
		{"redes/vpn", "redes", true},
	} {
		var got bool
		pattern := "%" + likeEscape(c.typed) + "%"
		if err := db.Read.QueryRow(`SELECT ? LIKE ? ESCAPE '\'`, c.value, pattern).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("%q LIKE %q: %v, want %v", c.value, pattern, got, c.want)
		}
	}
}
