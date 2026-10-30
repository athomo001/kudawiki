package wiki

import (
	"regexp"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

var (
	forbiddenTags = map[string]bool{"script": true, "iframe": true, "object": true, "embed": true, "style": true, "svg": true, "math": true, "form": true}
	badScheme     = regexp.MustCompile(`(?i)^\s*(javascript|vbscript|data):`)
)

// dangerous recorre el HTML de salida como lo haría el navegador y devuelve lo que nunca
// debe llegarle (SPEC §17): tags activos, manejadores on*, estilos en línea y esquemas peligrosos.
func dangerous(out string) string {
	z := html.NewTokenizer(strings.NewReader(out))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return ""
		case html.StartTagToken, html.SelfClosingTagToken:
			tok := z.Token()
			if forbiddenTags[tok.Data] {
				return "<" + tok.Data + ">"
			}
			for _, a := range tok.Attr {
				if strings.HasPrefix(a.Key, "on") || a.Key == "style" ||
					((a.Key == "href" || a.Key == "src") && badScheme.MatchString(a.Val)) {
					return a.Key + "=" + a.Val
				}
			}
		}
	}
}

func render(t *testing.T, src string, opts Options) (string, []Heading) {
	t.Helper()
	out, toc, err := Render(src, opts)
	if err != nil {
		t.Fatal(err)
	}
	return out, toc
}

func TestRenderStripsXSS(t *testing.T) {
	for _, src := range []string{
		`<script>alert(1)</script>`,
		`<img src=x onerror=alert(1)>`,
		`[clic](javascript:alert(1))`,
		`<iframe src="https://evil.example"></iframe>`,
		`<p style="background:url(x)">hola</p>`,
		`<svg onload=alert(1)>`,
		"```html\n<script>alert(1)</script>\n```",
		`<a href="jav&#x09;ascript:alert(1)">x</a>`,
		`![x](data:image/png;base64,AAAA)`,
		"::: tab <img src=x onerror=alert(1)>\nhola\n:::",
		"::: details \"><script>x</script>\nhola\n:::",
		"```mermaid\n</pre><script>alert(1)</script>\n```",
		`$<img src=x onerror=alert(1)>$`,
		`[[<b onmouseover=x>|y]]`,
	} {
		if out, _ := render(t, src, Options{Base: "/w/ti"}); dangerous(out) != "" {
			t.Errorf("Render(%q) leaked %s in: %s", src, dangerous(out), out)
		}
	}
}

func TestRenderFeatures(t *testing.T) {
	out, toc := render(t, "# Titulo\n\n## VPN\n\n- [x] hecho\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n```go\nfunc main() {}\n```\n", Options{})
	for _, want := range []string{`<h2 id="vpn">VPN</h2>`, `type="checkbox"`, "<table>", `class="chroma"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
	if len(toc) != 1 || toc[0].ID != "vpn" || toc[0].Level != 2 {
		t.Errorf("toc = %+v", toc)
	}
}

// Extensiones de SPEC §4.10: cada sintaxis y el HTML que debe salir.
func TestRenderExtensions(t *testing.T) {
	opts := Options{Base: "/w/ti", Children: []Child{{Slug: "redes/vpn", Title: "VPN"}}}
	for src, want := range map[string]string{
		"> [!WARNING]\n> Cuidado con la red.": `<div class="callout callout-warning"><p>Cuidado con la red.</p>`,
		"> cita normal":                       "<blockquote>",
		// Avisos de Wiki.js: la clase en la línea siguiente, con o sin ">"; el tipo sale del color.
		"> Si no está, no existe.\n{.is-info}":                             `<div class="callout callout-note"><p>Si no está, no existe.</p>`,
		"> Clúster sano.\n> {.is-success}":                                 `<div class="callout callout-tip"><p>Clúster sano.</p>`,
		"> uno\n>\n> dos\n{.is-danger}":                                    `<div class="callout callout-danger"><p>uno</p>`,
		"> Otra clase.\n{.is-foo}":                                         "<blockquote>\n<p>Otra clase.\n{.is-foo}</p>",
		"{.is-info}":                                                       "<p>{.is-info}</p>",
		":::tabs\n::: tab Linux\nuno\n:::\n::: tab Windows\ndos\n:::\n:::": `<section class="tab"><p class="tab-title">Linux</p><p>uno</p>`,
		":::steps\n1. a\n2. b\n:::":                                        `<div class="steps"><ol>`,
		":::details Mas info\noculto\n:::":                                 `<details><summary>Mas info</summary><p>oculto</p>`,
		"```mermaid\ngraph TD; A-->B\n```":                                 `<pre class="mermaid">graph TD; A--&gt;B`,
		"Area $a^2$ y precio $5 o $ x $":                                   `<span class="math-inline">a^2</span> y precio $5 o $ x $`,
		"$$E=mc^2$$":                                                       `<span class="math-display">E=mc^2</span>`,
		"Pulsa [[Ctrl+K]].":                                                "<kbd>Ctrl</kbd>+<kbd>K</kbd>",
		"Ver [[redes/vpn|la VPN]].":                                        `<a class="wikilink" href="/w/ti/p/redes/vpn" rel="nofollow">la VPN</a>`,
		"[[children]]":                                                     `<ul class="children"><li><a href="/w/ti/p/redes/vpn" rel="nofollow">VPN</a></li></ul>`,
		"[[toc]]\n\n## Uno\n\n### Dos":                                     `<ul class="toc"><li><a href="#uno" rel="nofollow">Uno</a></li><li class="sub"><a href="#dos" rel="nofollow">Dos</a></li></ul>`,
	} {
		if out, _ := render(t, src, opts); !strings.Contains(out, want) {
			t.Errorf("Render(%q)\n got: %s\nwant: %s", src, out, want)
		}
	}
	// Las pestañas anidadas cierran en orden: Windows queda dentro de tabs y fuera de Linux.
	out, _ := render(t, ":::tabs\n::: tab Linux\nuno\n:::\n::: tab Windows\ndos\n:::\n:::\nfuera", opts)
	if strings.Count(out, `<section class="tab">`) != 2 || !strings.HasSuffix(strings.TrimSpace(out), "</div>\n<p>fuera</p>") {
		t.Errorf("nested directives: %s", out)
	}
}

// El fuzzing del render es parte de SPEC §17: ninguna entrada produce HTML peligroso.
// §4.2: la vista previa marca cada bloque con su línea de origen; el lector, nunca.
func TestRenderSourceLines(t *testing.T) {
	src := "# Título\n\nUn párrafo\nen dos líneas.\n\n- uno\n- dos\n\n> [!NOTE]\n> aviso\n\n" +
		":::details Más\nadentro\n:::\n\n| a | b |\n| --- | --- |\n| 1 | 2 |\n\n```go\nx := 1\n```\n\n## Fin"
	out, _ := render(t, src, Options{SourceLines: true})
	for _, want := range []string{
		`data-line="1">Título</h1>`,
		`<p data-line="3">Un párrafo`,
		`<li data-line="6">uno`,
		`<li data-line="7">dos`,
		`<div class="callout callout-note" data-line="9">`,
		`<details data-line="12">`,
		`<p data-line="13">adentro`,
		`<table data-line="16">`,
		`<tr data-line="18">`,
		`<h2 id="fin" data-line="24">`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in:\n%s", want, out)
		}
	}
	if out, _ := render(t, src, Options{}); strings.Contains(out, "data-line") {
		t.Errorf("the reader must not carry source lines: %s", out)
	}
	// El autor no puede poner su propio data-line: el HTML crudo no pasa.
	if out, _ := render(t, `<p data-line="x" onclick="y">z</p>`, Options{SourceLines: true}); strings.Contains(out, `data-line="x"`) {
		t.Errorf("author data-line leaked: %s", out)
	}
}

func FuzzRender(f *testing.F) {
	for _, s := range []string{"<script>x</script>", "[a](javascript:b)", "<img onerror=x>", "```\n<b>\n```", "**x**",
		":::tabs\n::: tab x\ny\n:::\n:::", "> [!NOTE]\n> x", "> <b>x</b>\n{.is-info}", "$x$ [[a|b]] [[Ctrl+K]]", "```mermaid\nx\n```"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		for _, lines := range []bool{false, true} {
			out, _, err := Render(src, Options{Base: "/w/ti", Children: []Child{{Slug: "a", Title: "<b>"}}, SourceLines: lines})
			if bad := dangerous(out); err == nil && bad != "" {
				t.Fatalf("Render(%q) leaked %s in: %s", src, bad, out)
			}
		}
	})
}
