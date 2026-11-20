package httpapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/athomo001/kudawiki/internal/presence"
)

// QA-09: todo código de error que la API puede responder tiene su texto en la interfaz
// (error.<código>); si no, la persona lee "Algo falló en el servidor" aunque el motivo sea otro.
// Basta con es.json: TestI18nKeysMatch ya exige las mismas claves en en.json.
func TestErrorCodesTranslated(t *testing.T) {
	raw, err := os.ReadFile("../../ui/src/lib/i18n/es.json")
	if err != nil {
		t.Fatal(err)
	}
	var es map[string]string
	if err := json.Unmarshal(raw, &es); err != nil {
		t.Fatal(err)
	}
	codes := map[string]string{presence.ErrTooMany.Error(): "presence.ErrTooMany"}
	for e := range domainErrors {
		codes[e.Error()] = "domainErrors"
	}
	// Los códigos escritos a mano en los handlers: a.error(w, r, http.StatusX, "código", …) y los
	// cuerpos {"code": "…"}.
	literal := regexp.MustCompile(`http\.Status\w+, "([a-z_]+)"|"code":\s*"([a-z_]+)"`)
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range literal.FindAllStringSubmatch(string(src), -1) {
			codes[m[1]+m[2]] = f
		}
	}
	for code, from := range codes {
		if _, ok := es["error."+code]; !ok {
			t.Errorf("error code %q (%s) has no error.%s in the UI", code, from, code)
		}
	}
}
