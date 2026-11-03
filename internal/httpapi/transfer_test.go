package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// §4.13: solo el superadmin traslada, después de una vista previa que muestra rutas y niveles
// en el destino, enlaces del origen que quedan rotos y quién pierde y gana acceso. La página y
// su subárbol llegan con su historial y su media (el enlace se reescribe y el archivo se mueve);
// en el origen dan el mismo 404 de siempre, sin alias, y su auditoría no dice a dónde se fueron.
func TestTransfer(t *testing.T) {
	f := newFixture(t)
	w := f.upload("editorTI", "ti", "diagrama.png", pngBytes(t))
	var up struct{ ID, URL string }
	json.Unmarshal(w.Body.Bytes(), &up)
	f.expect(t, "editorTI", "PUT", "/api/w/ti/pages/red", `{"title":"Red","markdown":"![d](`+up.URL+`) [[red/vpn]]","base_revision":0}`, http.StatusNoContent)
	f.expect(t, "editorTI", "PUT", "/api/w/ti/pages/red", `{"title":"Red","markdown":"![d](`+up.URL+`) [[red/vpn]] v2","base_revision":1}`, http.StatusNoContent)
	f.expect(t, "editorTI", "PUT", "/api/w/ti/pages/red/vpn", `{"title":"VPN","markdown":"x","base_revision":0}`, http.StatusNoContent)
	f.expect(t, "editorTI", "PUT", "/api/w/ti/pages/inicio", `{"title":"Inicio","markdown":"Ver [[red/vpn]]","base_revision":0}`, http.StatusNoContent)
	f.expect(t, "adminFin", "PUT", "/api/w/finanzas/pages/plan", `{"title":"Plan","markdown":"x","base_revision":0}`, http.StatusNoContent)
	ti, fin := f.wsID(t, "ti"), f.wsID(t, "finanzas")
	body := func(to string) string {
		return `{"from":"` + ti + `","slug":"red","dest":"` + fin + `","to":"` + to + `","level":0}`
	}

	for _, who := range []string{"adminTI", "adminFin"} {
		if c := f.do(who, "POST", "/api/admin/transfer/preview", body("infra/red")).Code; c != http.StatusNotFound {
			t.Errorf("%s previews a transfer: %d", who, c)
		}
	}
	var p struct {
		Pages []struct {
			Slug    string `json:"slug"`
			NewSlug string `json:"new_slug"`
		} `json:"pages"`
		Taken       []string            `json:"taken"`
		BrokenLinks []map[string]string `json:"broken_links"`
		Lose, Gain  []string
	}
	json.Unmarshal([]byte(f.expect(t, "super", "POST", "/api/admin/transfer/preview", body("infra/red"), http.StatusOK)), &p)
	if len(p.Pages) != 2 || p.Pages[1].NewSlug != "infra/red/vpn" || len(p.Taken) != 0 {
		t.Errorf("preview pages: %+v", p)
	}
	if len(p.BrokenLinks) != 1 || p.BrokenLinks[0]["from"] != "inicio" || len(p.Lose) == 0 || len(p.Gain) == 0 {
		t.Errorf("preview impact: %+v", p)
	}
	if b := f.expect(t, "super", "POST", "/api/admin/transfer/preview", body("plan"), http.StatusOK); !strings.Contains(b, `"taken":["plan"]`) {
		t.Errorf("a taken path: %s", b)
	}
	f.expect(t, "super", "POST", "/api/admin/transfer/preview", `{"from":"`+ti+`","slug":"red","dest":"`+ti+`","to":"x"}`, http.StatusUnprocessableEntity)

	if c := f.do("super", "POST", "/api/admin/transfer", body("infra/red")).Code; c == http.StatusOK {
		t.Fatal("transfer without reauthenticating")
	}
	f.reauth(t, "super")
	f.expect(t, "super", "POST", "/api/admin/transfer", body("plan"), http.StatusConflict)
	f.expect(t, "super", "POST", "/api/admin/transfer", body("infra/red"), http.StatusOK)

	for _, path := range []string{"/api/w/ti/pages/red", "/api/w/ti/pages/red/vpn", up.URL} {
		if c := f.do("adminTI", "GET", path, "").Code; c != http.StatusNotFound {
			t.Errorf("%s still in the source: %d", path, c)
		}
	}
	page := f.expect(t, "adminFin", "GET", "/api/w/finanzas/pages/infra/red", "", http.StatusOK)
	newURL := strings.Replace(up.URL, "/w/ti/", "/w/finanzas/", 1)
	if !strings.Contains(page, newURL) {
		t.Errorf("the media link was not rewritten: %s", page)
	}
	if c := f.do("adminFin", "GET", newURL, "").Code; c != http.StatusOK {
		t.Errorf("the media file did not move: %d", c)
	}
	if h := f.expect(t, "adminFin", "GET", "/api/w/finanzas/history/infra/red", "", http.StatusOK); strings.Count(h, `"revision":`) < 2 {
		t.Errorf("the history did not travel: %s", h)
	}
	out := f.expect(t, "adminTI", "GET", "/api/w/ti/audit?action=page.transfer_out", "", http.StatusOK)
	if !strings.Contains(out, "page.transfer_out") || strings.Contains(out, "finanzas") || strings.Contains(out, "Finanzas") {
		t.Errorf("source audit: %s", out)
	}
	if in := f.expect(t, "adminFin", "GET", "/api/w/finanzas/audit?action=page.transfer_in", "", http.StatusOK); !strings.Contains(in, "infra/red/vpn") {
		t.Errorf("destination audit: %s", in)
	}
}
