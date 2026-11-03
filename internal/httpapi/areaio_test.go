package httpapi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/athomo001/kudawiki/internal/repo"
)

// §13.8: el ZIP de un área trae un .md por página con su front matter y la media, con enlaces
// relativos; nunca un sellado. Importarlo en otra área crea las mismas páginas (con su nivel,
// etiquetas y media nueva), no pisa las que existen, y el dry-run no crea nada.
func TestAreaExportImport(t *testing.T) {
	f := newFixture(t)
	w := f.upload("editorTI", "ti", "diagrama.png", pngBytes(t))
	var up struct{ ID, URL string }
	json.Unmarshal(w.Body.Bytes(), &up)
	f.expect(t, "editorTI", "PUT", "/api/w/ti/pages/red", `{"title":"Red","markdown":"![d](`+up.URL+`) [[red/vpn]]","base_revision":0}`, http.StatusNoContent)
	f.expect(t, "editorTI", "PUT", "/api/w/ti/pages/red/vpn", `{"title":"VPN","markdown":"secreto","base_revision":0}`, http.StatusNoContent)
	f.expect(t, "adminTI", "PATCH", "/api/w/ti/meta/red/vpn", `{"level":3}`, http.StatusNoContent)
	f.expect(t, "editorTI", "PATCH", "/api/w/ti/meta/red", `{"tags":["redes"]}`, http.StatusNoContent)
	f.expect(t, "adminFin", "PUT", "/api/w/finanzas/pages/red", `{"title":"Ya existe","markdown":"x","base_revision":0}`, http.StatusNoContent)

	for _, who := range []string{"editorTI", "adminFin", "outsider"} {
		if c := f.do(who, "GET", "/api/w/ti/export", "").Code; c != http.StatusNotFound {
			t.Errorf("%s exports TI: %d", who, c)
		}
	}
	res := f.do("adminTI", "GET", "/api/w/ti/export", "")
	if res.Code != http.StatusOK || res.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("export: %d %v", res.Code, res.Header())
	}
	data := res.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, zf := range zr.File {
		rc, _ := zf.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		files[zf.Name] = string(b)
	}
	red, vpn := files["pages/red.md"], files["pages/red/vpn.md"]
	if !strings.HasPrefix(red, "---\ntitle: Red\n") || !strings.Contains(red, "tags:\n    - redes") || !strings.Contains(red, "](../media/"+up.ID+"-diagrama.png)") {
		t.Errorf("red.md:\n%s", red)
	}
	if !strings.Contains(vpn, "level: 3") || files["manifest.json"] == "" {
		t.Errorf("vpn.md:\n%s\nfiles: %v", vpn, len(files))
	}
	if _, ok := files["media/"+up.ID+"-diagrama.png"]; !ok {
		t.Errorf("no media in the ZIP: %v", files)
	}

	imp := func(q string) map[string]any {
		t.Helper()
		w := f.uploadTo("adminFin", "/api/w/finanzas/import"+q, "ti.zip", data)
		if w.Code != http.StatusOK {
			t.Fatalf("import%s: %d %s", q, w.Code, w.Body)
		}
		var rep map[string]any
		json.Unmarshal(w.Body.Bytes(), &rep)
		return rep
	}
	if rep := imp("?dry_run=1"); len(rep["pages"].([]any)) != 1 || rep["media"].(float64) != 0 {
		// red existe en Finanzas: solo red/vpn se crearía, y no usa media.
		t.Errorf("dry run: %v", rep)
	}
	if c := f.do("adminFin", "GET", "/api/w/finanzas/pages/red/vpn", "").Code; c != http.StatusNotFound {
		t.Errorf("the dry run created a page: %d", c)
	}
	if c := f.uploadTo("editorTI", "/api/w/ti/import", "ti.zip", data).Code; c != http.StatusNotFound {
		t.Errorf("an editor imports: %d", c)
	}
	rep := imp("")
	if len(rep["pages"].([]any)) != 1 || len(rep["skipped"].([]any)) != 1 {
		t.Errorf("import: %v", rep)
	}
	page := f.expect(t, "adminFin", "GET", "/api/w/finanzas/pages/red/vpn", "", http.StatusOK)
	if !strings.Contains(page, `"level":3`) && !strings.Contains(page, `"min_rank":3`) {
		t.Errorf("the level did not come: %s", page)
	}
	if b := f.expect(t, "adminFin", "GET", "/api/w/finanzas/pages/red", "", http.StatusOK); !strings.Contains(b, "Ya existe") {
		t.Errorf("an existing page was overwritten: %s", b)
	}
	if a := f.expect(t, "adminFin", "GET", "/api/w/finanzas/audit?action=ws.import", "", http.StatusOK); !strings.Contains(a, "ws.import") {
		t.Errorf("import audit: %s", a)
	}

	// Un ZIP con su media en otra área: la imagen se sube a Finanzas y el enlace apunta a ella.
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	md, _ := zw.Create("docs/guia.md")
	io.WriteString(md, "# Guía\n\n![x](img/a.png) ver [otra](otra.md)")
	o, _ := zw.Create("docs/otra.md")
	io.WriteString(o, "otra")
	img, _ := zw.Create("docs/img/a.png")
	img.Write(pngBytes(t))
	zw.Close()
	if w := f.uploadTo("adminFin", "/api/w/finanzas/import", "docs.zip", zbuf.Bytes()); w.Code != http.StatusOK {
		t.Fatalf("plain zip: %d %s", w.Code, w.Body)
	}
	g := f.expect(t, "adminFin", "GET", "/api/w/finanzas/pages/docs/guia", "", http.StatusOK)
	if !strings.Contains(g, `"title":"Guía"`) || !strings.Contains(g, "/api/w/finanzas/media/") || !strings.Contains(g, "[[docs/otra|otra]]") {
		t.Errorf("plain zip page: %s", g)
	}
}

// Exportar todas mis áreas (F8-B): un ZIP con el de cada área donde es admin, y nada de las otras.
func TestExportMyAreas(t *testing.T) {
	f := newFixture(t)
	f.expect(t, "editorTI", "PUT", "/api/w/ti/pages/red", `{"title":"Red","markdown":"de TI","base_revision":0}`, http.StatusNoContent)
	f.expect(t, "adminFin", "PUT", "/api/w/finanzas/pages/caja", `{"title":"Caja","markdown":"de finanzas","base_revision":0}`, http.StatusNoContent)

	// Sin ninguna área como admin: el 404 de siempre, también para el superadmin, que no es miembro.
	for _, who := range []string{"editorTI", "viewerTI", "outsider", "super"} {
		if c := f.do(who, "GET", "/api/me/areas/export", "").Code; c != http.StatusNotFound {
			t.Errorf("%s: %d", who, c)
		}
	}
	if c := f.do("", "GET", "/api/me/areas/export", "").Code; c != http.StatusUnauthorized {
		t.Errorf("anonymous: %d", c)
	}

	inner := func(who string) map[string]map[string]bool {
		t.Helper()
		res := f.do(who, "GET", "/api/me/areas/export", "")
		if res.Code != http.StatusOK || res.Header().Get("Content-Type") != "application/zip" || res.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d %v", who, res.Code, res.Header())
		}
		data := res.Body.Bytes()
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]map[string]bool{}
		for _, zf := range zr.File {
			rc, _ := zf.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			az, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatalf("%s is not a ZIP: %v", zf.Name, err)
			}
			out[zf.Name] = map[string]bool{}
			for _, x := range az.File {
				out[zf.Name][x.Name] = true
			}
		}
		return out
	}
	if got := inner("adminTI"); len(got) != 1 || !got["ti.zip"]["pages/red.md"] {
		t.Errorf("adminTI: %v", got)
	}
	// Admin de las dos: las dos, cada una con lo suyo.
	if err := repo.New(f.db).SetMember(t.Context(), repo.Scope{WsID: f.wsID(t, "finanzas")}, f.users["adminTI"], "admin"); err != nil {
		t.Fatal(err)
	}
	got := inner("adminTI")
	if len(got) != 2 || !got["ti.zip"]["pages/red.md"] || got["ti.zip"]["pages/caja.md"] || !got["finanzas.zip"]["pages/caja.md"] {
		t.Errorf("adminTI in two areas: %v", got)
	}
}
