package wiki

import (
	"bytes"
	"regexp"
	"sort"
	"strconv"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/ast"
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
	// Solo números: es la línea de origen que pone markSourceLines, no algo que escriba el autor.
	p.AllowAttrs("data-line").Matching(regexp.MustCompile(`^[0-9]{1,7}$`)).Globally()
	return p
}()

// Render convierte Markdown en HTML saneado y devuelve el índice (h2 y h3).
func Render(markdown string, opts Options) (string, []Heading, error) {
	src := []byte(markdown)
	pc := parser.NewContext()
	pc.Set(optionsKey, &opts)
	doc := md.Parser().Parse(text.NewReader(src), parser.WithContext(pc))
	if opts.SourceLines {
		markSourceLines(doc, src)
	}

	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, src, doc); err != nil {
		return "", nil, err
	}
	return policy.Sanitize(buf.String()), opts.toc, nil
}

// markSourceLines pone en cada bloque la línea (desde 1) donde empieza en el Markdown. Lo que
// goldmark no dibuja con atributos (el código resaltado, los diagramas) queda sin marca: la
// vista previa interpola entre los vecinos.
func markSourceLines(doc ast.Node, src []byte) {
	starts := []int{0}
	for i, c := range src {
		if c == '\n' {
			starts = append(starts, i+1)
		}
	}
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || n.Type() != ast.TypeBlock || n.Kind() == ast.KindDocument {
			return ast.WalkContinue, nil
		}
		if off, ok := firstOffset(n); ok {
			line := sort.Search(len(starts), func(i int) bool { return starts[i] > off })
			n.SetAttributeString("data-line", []byte(strconv.Itoa(line)))
		}
		return ast.WalkContinue, nil
	})
}

// firstOffset es el primer byte del Markdown que cubre un nodo: sus líneas o las de su primer
// descendiente que las tenga (una lista no tiene líneas propias; su primer ítem, sí).
func firstOffset(n ast.Node) (int, bool) {
	switch n := n.(type) {
	case *directive:
		return n.start, true
	case *ast.Text:
		return n.Segment.Start, true
	}
	if n.Type() == ast.TypeBlock && n.Lines().Len() > 0 { // Lines() de un nodo en línea entra en pánico
		return n.Lines().At(0).Start, true
	}
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if off, ok := firstOffset(c); ok {
			return off, true
		}
	}
	return 0, false
}
