package wikijs

import (
	"strings"
	"testing"
	"testing/fstest"
)

// §13.8: carpeta de Wiki.js con cabecera YAML (.md) y de comentario (.html), mapeo por prefijo,
// pestañas {.tabset}, idioma en los enlaces, páginas sin publicar a nivel 3 y lo que no mapea.
func TestReadFolderAndBuild(t *testing.T) {
	fsys := fstest.MapFS{
		"ti/redes.md":       {Data: []byte("---\ntitle: Redes\npublished: true\ntags: red, vpn\neditor: markdown\n---\n\n# Tabs {.tabset}\n## Linux\nuno\n## Windows\ndos\n\nVer [vpn](/es/ti/redes/vpn) {.links-list}")},
		"ti/redes/vpn.html": {Data: []byte("<!--\ntitle: VPN\npublished: false\n-->\n<h2>Uso</h2><p>Hola <strong>mundo</strong></p><ul><li>a</li><li>b</li></ul>")},
		"otro/x.md":         {Data: []byte("x")},
		".git/config":       {Data: []byte("x")},
	}
	pages, bad, err := ReadFolder(fsys)
	if err != nil || len(bad) != 0 || len(pages) != 3 {
		t.Fatalf("read: %v %v %d", err, bad, len(pages))
	}
	for i := range pages {
		pages[i].Locale = "es"
	}
	m, err := ParseMapping([]byte("mappings:\n  - from: /ti/**\n    to: ti\n"))
	if err != nil {
		t.Fatal(err)
	}
	plan := Build(pages, m)
	ti := plan.Areas["ti"]
	if len(ti) != 2 || ti[0].Slug != "redes" || ti[1].Slug != "redes/vpn" || len(plan.Skipped) != 1 || plan.Skipped[0].Reason != "unmapped" {
		t.Fatalf("plan: %+v skipped %+v", ti, plan.Skipped)
	}
	if md := ti[0].Markdown; !strings.Contains(md, ":::tabs\n::: tab Linux\nuno\n:::\n::: tab Windows\ndos") || !strings.Contains(md, "](/ti/redes/vpn)") {
		t.Errorf("markdown:\n%s", md)
	}
	if ti[1].Level != 3 || !strings.Contains(ti[1].Markdown, "## Uso\n\nHola **mundo**\n\n- a\n- b") || len(ti[0].Tags) != 2 {
		t.Errorf("html page: %+v", ti[1])
	}
	reasons := map[string]bool{}
	for _, w := range plan.Warnings {
		reasons[w.Reason+":"+w.Detail] = true
	}
	for _, want := range []string{"html_converted:", "unpublished:", "unsupported_class:links-list"} {
		if !reasons[want] {
			t.Errorf("missing warning %s in %v", want, plan.Warnings)
		}
	}
	if _, err := ParseMapping([]byte("mappings:\n  - from: ti/*/x\n    to: ti\n")); err == nil {
		t.Error("a pattern that is not a prefix was accepted")
	}
}
