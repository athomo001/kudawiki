package wiki

import (
	"bytes"
	"regexp"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// Heading es una entrada del índice lateral del lector (SPEC §15).
type Heading struct {
	Level int    `json:"level"`
	ID    string `json:"id"`
	Text  string `json:"text"`
}

// CommonMark + tablas, listas de tareas, notas al pie (SPEC §4.1) y las extensiones de §4.10.
// El resaltado usa clases y no estilos en línea: la CSP no permite 'unsafe-inline' (§14.5).
var md = goldmark.New(
	goldmark.WithExtensions(
		extension.GFM, extension.Footnote, kudawikiExtensions{},
		highlighting.NewHighlighting(highlighting.WithFormatOptions(chromahtml.WithClasses(true))),
	),
	goldmark.WithParserOptions(parser.WithAutoHeadingID()),
)

// policy es la única puerta entre el Markdown de un usuario y el navegador de otro: el HTML
// crudo, los on*, javascript:, data: y los estilos en línea no pasan (SPEC §4.1, §4.4, §17).
var policy = func() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	safeToken := regexp.MustCompile(`^[a-zA-Z0-9:_ -]+$`)
	p.AllowAttrs("class").Matching(safeToken).OnElements("span", "code", "pre", "div", "li", "ul", "ol", "input", "sup", "a", "section", "p")
	p.AllowAttrs("id").Matching(safeToken).OnElements("h1", "h2", "h3", "h4", "h5", "h6", "li", "sup", "a")
	p.AllowAttrs("type").Matching(regexp.MustCompile(`^checkbox$`)).OnElements("input")
	p.AllowAttrs("checked", "disabled").OnElements("input")
	p.AllowElements("section", "details", "summary", "kbd")
	return p
}()

// Render convierte Markdown en HTML saneado y devuelve el índice (h2 y h3).
func Render(markdown string, opts Options) (string, []Heading, error) {
	src := []byte(markdown)
	pc := parser.NewContext()
	pc.Set(optionsKey, &opts)
	doc := md.Parser().Parse(text.NewReader(src), parser.WithContext(pc))

	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, src, doc); err != nil {
		return "", nil, err
	}
	return policy.Sanitize(buf.String()), opts.toc, nil
}
