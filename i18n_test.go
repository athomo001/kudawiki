package kudawiki

import (
	"encoding/json"
	"maps"
	"os"
	"slices"
	"testing"
)

// Una clave que existe en un idioma y no en el otro rompe el build (SPEC §15).
func TestI18nKeysMatch(t *testing.T) {
	keys := func(lang string) []string {
		raw, err := os.ReadFile("ui/src/lib/i18n/" + lang + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s.json: %v", lang, err)
		}
		return slices.Sorted(maps.Keys(m))
	}
	es, en := keys("es"), keys("en")
	for _, k := range es {
		if !slices.Contains(en, k) {
			t.Errorf("en.json missing %q", k)
		}
	}
	for _, k := range en {
		if !slices.Contains(es, k) {
			t.Errorf("es.json missing %q", k)
		}
	}
}
