package wiki

import (
	"bytes"
	"fmt"
	"html"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Extensiones de Markdown de SPEC §4.10. Todo se resuelve en el servidor y la salida pasa por
// bluemonday: no hay HTML, scripts ni CSS que el autor pueda inyectar.

// Child es una subpágina visible para quien lee, para [[children]].
type Child struct{ Slug, Title, Description, Icon string }

// Options es el contexto de un render: la base de las rutas del área (/w/ti), las hijas
// visibles y qué rutas existen para quien lee. Sin base, los [[enlaces]] quedan como texto.
type Options struct {
	Base     string
	Children []Child
	// Exists dice si quien lee ve una página en esa ruta. Una que existe pero no ve se marca
	// como rota igual que una inexistente: marcarla como válida la delataría (SPEC §17).
	Exists func(slug string) bool
	// SourceLines marca cada bloque con la línea del Markdown donde empieza (data-line). Solo lo
	// pide la vista previa del editor, para desplazar juntas sus dos columnas (SPEC §4.2).
	SourceLines bool
	toc         []Heading // lo llena el transformador
}

var optionsKey = parser.NewContextKey()

var (
	kindCallout   = ast.NewNodeKind("Callout")
	kindDirective = ast.NewNodeKind("Directive")
	kindWikiToken = ast.NewNodeKind("WikiToken")
	kindMath      = ast.NewNodeKind("Math")
	kindMermaid   = ast.NewNodeKind("Mermaid")
	kindBlockList = ast.NewNodeKind("BlockList")
)

// ----- Nodos -----

type callout struct {
	ast.BaseBlock
	kind string // note, tip, warning, danger
}

type directive struct {
	ast.BaseBlock
	name, title string
	open        int // directivas hijas abiertas: el ":::" de cierre es de la más interna
	start       int // primer byte de la línea ":::", para data-line (no tiene líneas propias)
}

type wikiToken struct {
	ast.BaseInline
	raw, base string
	broken    bool
}

// target es la ruta de un [[ruta|texto]], o "" si el token no es un enlace.
func (t *wikiToken) target() string {
	target, _, _ := strings.Cut(t.raw, "|")
	// toc y children son palabras reservadas (§4.10), no rutas.
	if target = strings.TrimSpace(target); ValidSlug(target) && target != "toc" && target != "children" {
		return target
	}
	return ""
}

type math struct {
	ast.BaseInline
	tex     string
	display bool
}

type mermaid struct {
	ast.BaseBlock
	source string
}

// blockList reemplaza un párrafo que solo tiene [[toc]] o [[children]]: una lista no puede
// ir dentro de un <p>.
type blockList struct {
	ast.BaseBlock
	class string
	items []listItem
}

// listItem es una línea de [[toc]] o de [[children]]; note es la descripción de la hija (§4.1).
type listItem struct {
	href, text, note string
	sub              bool
}

func (n *callout) Kind() ast.NodeKind   { return kindCallout }
func (n *directive) Kind() ast.NodeKind { return kindDirective }
func (n *wikiToken) Kind() ast.NodeKind { return kindWikiToken }
func (n *math) Kind() ast.NodeKind      { return kindMath }
func (n *mermaid) Kind() ast.NodeKind   { return kindMermaid }
func (n *blockList) Kind() ast.NodeKind { return kindBlockList }

func (n *callout) Dump(src []byte, l int)   { ast.DumpHelper(n, src, l, nil, nil) }
func (n *directive) Dump(src []byte, l int) { ast.DumpHelper(n, src, l, nil, nil) }
func (n *wikiToken) Dump(src []byte, l int) { ast.DumpHelper(n, src, l, nil, nil) }
func (n *math) Dump(src []byte, l int)      { ast.DumpHelper(n, src, l, nil, nil) }
func (n *mermaid) Dump(src []byte, l int)   { ast.DumpHelper(n, src, l, nil, nil) }
func (n *blockList) Dump(src []byte, l int) { ast.DumpHelper(n, src, l, nil, nil) }

// ----- :::tabs, ::: tab Linux, :::steps, :::details Título, y el cierre ::: -----

var (
	directiveOpen  = regexp.MustCompile(`^:::\s*(tabs|tab|steps|details)(?:\s+(.*?))?\s*$`)
	directiveClose = regexp.MustCompile(`^:::\s*$`)
)

type directiveParser struct{}

func (directiveParser) Trigger() []byte { return []byte{':'} }

func (directiveParser) Open(parent ast.Node, reader text.Reader, _ parser.Context) (ast.Node, parser.State) {
	line, seg := reader.PeekLine()
	m := directiveOpen.FindSubmatch(util.TrimRightSpace(line))
	if m == nil {
		return nil, parser.NoChildren
	}
	for p := parent; p != nil; p = p.Parent() {
		if d, ok := p.(*directive); ok {
			d.open++
		}
	}
	advanceLine(reader, line, seg)
	return &directive{name: string(m[1]), title: string(m[2]), start: seg.Start}, parser.HasChildren
}

// Continue recibe la línea primero en la directiva más externa: si una hija sigue abierta,
// el cierre le corresponde a ella y la línea pasa sin consumirse.
func (directiveParser) Continue(node ast.Node, reader text.Reader, _ parser.Context) parser.State {
	d := node.(*directive)
	line, seg := reader.PeekLine()
	if !directiveClose.Match(util.TrimRightSpace(util.TrimLeftSpace(line))) {
		return parser.Continue | parser.HasChildren
	}
	if d.open > 0 {
		d.open--
		return parser.Continue | parser.HasChildren
	}
	advanceLine(reader, line, seg)
	return parser.Close
}

// advanceLine consume la línea sin su salto: el parser de goldmark avanza a la siguiente por
// su cuenta (igual que en sus bloques de código).
func advanceLine(reader text.Reader, line []byte, seg text.Segment) {
	n := seg.Len()
	if len(line) > 0 && line[len(line)-1] == '\n' {
		n--
	}
	reader.Advance(n)
}

func (directiveParser) Close(ast.Node, text.Reader, parser.Context) {} // sin nada que cerrar: los hijos ya son nodos
func (directiveParser) CanInterruptParagraph() bool                 { return true }
func (directiveParser) CanAcceptIndentedLine() bool                 { return false }

// ----- [[toc]], [[children]], [[ruta|texto]] (§4.7) y [[Ctrl+K]] -----

type wikiTokenParser struct{}

func (wikiTokenParser) Trigger() []byte { return []byte{'['} }

func (wikiTokenParser) Parse(_ ast.Node, block text.Reader, _ parser.Context) ast.Node {
	line, _ := block.PeekLine()
	end := bytes.Index(line, []byte("]]"))
	if !bytes.HasPrefix(line, []byte("[[")) || end < 3 {
		return nil
	}
	block.Advance(end + 2)
	return &wikiToken{raw: strings.TrimSpace(string(line[2:end]))}
}

// ----- $…$ y $$…$$: el navegador las dibuja con KaTeX como MathML, sin estilos en línea -----

type mathParser struct{}

func (mathParser) Trigger() []byte { return []byte{'$'} }

// Parse sigue la regla de Pandoc para no confundir precios con fórmulas: tras el $ de apertura
// no va espacio, antes del de cierre tampoco, y al cierre no le sigue un dígito.
func (mathParser) Parse(_ ast.Node, block text.Reader, _ parser.Context) ast.Node {
	line, _ := block.PeekLine()
	delim := 1
	if bytes.HasPrefix(line, []byte("$$")) {
		delim = 2
	}
	body := line[delim:]
	end := bytes.Index(body, line[:delim])
	if end <= 0 || body[0] == ' ' || body[end-1] == ' ' ||
		(len(body) > end+delim && body[end+delim] >= '0' && body[end+delim] <= '9') {
		return nil
	}
	block.Advance(delim + end + delim)
	return &math{tex: string(body[:end]), display: delim == 2}
}

// ----- Transformador: avisos, diagramas, índice y listas sobre el árbol ya parseado -----

var calloutRe = regexp.MustCompile(`^\[!(NOTE|TIP|WARNING|DANGER)\]\s*$`)

type transformer struct{}

func (transformer) Transform(doc *ast.Document, reader text.Reader, pc parser.Context) {
	src := reader.Source()
	opts, _ := pc.Get(optionsKey).(*Options)
	var lists []*blockList
	var replace []func()

	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Heading:
			if id, ok := n.AttributeString("id"); ok && (n.Level == 2 || n.Level == 3) {
				opts.toc = append(opts.toc, Heading{Level: n.Level, ID: string(id.([]byte)), Text: string(n.Lines().Value(src))})
			}
		case *wikiToken:
			n.base = opts.Base
			n.broken = opts.Exists != nil && n.target() != "" && !opts.Exists(n.target())
		case *ast.Blockquote:
			if c := asCallout(n, src); c != nil {
				replace = append(replace, func() { n.Parent().ReplaceChild(n.Parent(), n, c) })
			}
		case *ast.FencedCodeBlock:
			if string(n.Language(src)) == "mermaid" {
				m := &mermaid{source: string(n.Lines().Value(src))}
				replace = append(replace, func() { n.Parent().ReplaceChild(n.Parent(), n, m) })
			}
		case *ast.Paragraph:
			if t, ok := n.FirstChild().(*wikiToken); ok && n.ChildCount() == 1 && (t.raw == "toc" || t.raw == "children") {
				l := &blockList{class: t.raw}
				lists = append(lists, l)
				replace = append(replace, func() { n.Parent().ReplaceChild(n.Parent(), n, l) })
			}
		}
		return ast.WalkContinue, nil
	})
	// Las listas se llenan al final, cuando el índice ya tiene todos los encabezados.
	for _, l := range lists {
		if l.class == "toc" {
			for _, h := range opts.toc {
				l.items = append(l.items, listItem{href: "#" + h.ID, text: h.Text, sub: h.Level == 3})
			}
			continue
		}
		for _, c := range opts.Children {
			text := c.Title
			if c.Icon != "" {
				text = c.Icon + " " + text
			}
			l.items = append(l.items, listItem{href: opts.Base + "/p/" + c.Slug, text: text, note: c.Description})
		}
	}
	for _, f := range replace {
		f()
	}
}

// asCallout convierte una cita en aviso. Con la sintaxis de GitHub la marca va sola en la
// primera línea ("> [!NOTE]"); con la de Wiki.js, la clase va sola en la última ("{.is-info}").
func asCallout(bq *ast.Blockquote, src []byte) ast.Node {
	kind := githubCallout(bq, src)
	if kind == "" {
		kind = wikijsCallout(bq, src)
	}
	if kind == "" {
		return nil
	}
	c := &callout{kind: kind}
	for child := bq.FirstChild(); child != nil; {
		next := child.NextSibling()
		c.AppendChild(c, child)
		child = next
	}
	return c
}

// githubCallout quita la marca "[!NOTE]" de la primera línea y dice qué aviso es.
func githubCallout(bq *ast.Blockquote, src []byte) string {
	p, ok := bq.FirstChild().(*ast.Paragraph)
	if !ok || p.Lines().Len() == 0 {
		return ""
	}
	first := p.Lines().At(0)
	m := calloutRe.FindSubmatch(util.TrimRightSpace(first.Value(src)))
	if m == nil {
		return ""
	}
	for c := p.FirstChild(); c != nil; { // quita los nodos de la línea de la marca
		next := c.NextSibling()
		if t, ok := c.(*ast.Text); ok && t.Segment.Start >= first.Stop {
			break
		}
		p.RemoveChild(p, c)
		c = next
	}
	if p.ChildCount() == 0 {
		bq.RemoveChild(bq, p)
	}
	return strings.ToLower(string(m[1]))
}

// Los cuatro avisos de Wiki.js, por color: el verde de "success" es el de "tip".
var (
	wikijsCalloutRe = regexp.MustCompile(`^\{\.is-(info|success|warning|danger)\}$`)
	wikijsKinds     = map[string]string{"info": "note", "success": "tip", "warning": "warning", "danger": "danger"}
)

// wikijsCallout acepta los avisos escritos para Wiki.js, para que una página pegada desde allá
// se vea igual (§4.10): la clase va sola en la línea siguiente al texto, con o sin ">".
func wikijsCallout(bq *ast.Blockquote, src []byte) string {
	p, ok := bq.LastChild().(*ast.Paragraph)
	if !ok || p.Lines().Len() == 0 {
		return ""
	}
	last := p.Lines().At(p.Lines().Len() - 1)
	m := wikijsCalloutRe.FindSubmatch(bytes.TrimSpace(last.Value(src)))
	if m == nil {
		return ""
	}
	for c := p.LastChild(); c != nil; { // quita los nodos de la línea de la clase
		t, ok := c.(*ast.Text)
		if !ok || t.Segment.Start < last.Start {
			break
		}
		prev := c.PreviousSibling()
		p.RemoveChild(p, c)
		c = prev
	}
	// El salto antes de la clase ya no separa nada.
	if t, ok := p.LastChild().(*ast.Text); ok {
		t.SetSoftLineBreak(false)
		t.SetHardLineBreak(false)
	}
	if p.ChildCount() == 0 {
		bq.RemoveChild(bq, p)
	}
	return wikijsKinds[string(m[1])]
}

// ----- HTML -----

type extRenderer struct{}

func (extRenderer) RegisterFuncs(r renderer.NodeRendererFuncRegisterer) {
	r.Register(kindCallout, func(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		// El título ("Nota", "Atención"…) lo pone la hoja de estilos según el idioma de la
		// interfaz: el HTML no depende de quién lee.
		if entering {
			fmt.Fprintf(w, `<div class="callout callout-%s"`, n.(*callout).kind)
			gmhtml.RenderAttributes(w, n, nil) // solo data-line, en la vista previa
			w.WriteString(">")
		} else {
			w.WriteString("</div>\n")
		}
		return ast.WalkContinue, nil
	})
	r.Register(kindDirective, func(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		d := n.(*directive)
		title := html.EscapeString(d.title)
		open := func(tag string) {
			w.WriteString(tag)
			gmhtml.RenderAttributes(w, n, nil) // solo data-line, en la vista previa
			w.WriteString(">")
		}
		switch {
		case d.name == "tab" && entering:
			open(`<section class="tab"`)
			fmt.Fprintf(w, `<p class="tab-title">%s</p>`, title)
		case d.name == "tab":
			w.WriteString("</section>\n")
		case d.name == "details" && entering:
			open(`<details`)
			fmt.Fprintf(w, `<summary>%s</summary>`, title)
		case d.name == "details":
			w.WriteString("</details>\n")
		case entering: // tabs, steps
			open(fmt.Sprintf(`<div class="%s"`, d.name))
		default:
			w.WriteString("</div>\n")
		}
		return ast.WalkContinue, nil
	})
	r.Register(kindBlockList, func(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		l := n.(*blockList)
		fmt.Fprintf(w, `<ul class="%s">`, l.class)
		for _, it := range l.items {
			sub := ""
			if it.sub {
				sub = ` class="sub"`
			}
			note := ""
			if it.note != "" {
				note = ` <span class="child-note">` + html.EscapeString(it.note) + `</span>`
			}
			fmt.Fprintf(w, `<li%s><a href="%s">%s</a>%s</li>`, sub, html.EscapeString(it.href), html.EscapeString(it.text), note)
		}
		w.WriteString("</ul>\n")
		return ast.WalkSkipChildren, nil
	})
	r.Register(kindWikiToken, func(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			renderWikiToken(w, n.(*wikiToken))
		}
		return ast.WalkSkipChildren, nil
	})
	r.Register(kindMermaid, func(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			fmt.Fprintf(w, `<pre class="mermaid">%s</pre>`+"\n", html.EscapeString(n.(*mermaid).source))
		}
		return ast.WalkSkipChildren, nil
	})
	r.Register(kindMath, func(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			m := n.(*math)
			class := "math-inline"
			if m.display {
				class = "math-display"
			}
			fmt.Fprintf(w, `<span class="%s">%s</span>`, class, html.EscapeString(m.tex))
		}
		return ast.WalkSkipChildren, nil
	})
}

// renderWikiToken decide qué es un [[…]]: una ruta del área es un enlace (nunca a otra área,
// §4.7); con "+" o mayúsculas son teclas; cualquier otra cosa queda como texto.
func renderWikiToken(w util.BufWriter, t *wikiToken) {
	target := t.target()
	switch {
	case target != "" && t.base != "":
		_, label, hasLabel := strings.Cut(t.raw, "|")
		if !hasLabel {
			label = target
		}
		class := "wikilink"
		if t.broken {
			class += " broken"
		}
		fmt.Fprintf(w, `<a class="%s" href="%s/p/%s">%s</a>`, class, html.EscapeString(t.base), target, html.EscapeString(strings.TrimSpace(label)))
	case strings.Contains(t.raw, "+") || t.raw != strings.ToLower(t.raw):
		for i, k := range strings.Split(t.raw, "+") {
			if i > 0 {
				w.WriteString("+")
			}
			fmt.Fprintf(w, "<kbd>%s</kbd>", html.EscapeString(strings.TrimSpace(k)))
		}
	default:
		w.WriteString(html.EscapeString("[[" + t.raw + "]]"))
	}
}

type kudawikiExtensions struct{}

func (kudawikiExtensions) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(
		parser.WithBlockParsers(util.Prioritized(directiveParser{}, 50)),
		// Antes que las notas al pie (101) y que los enlaces (200), que también empiezan con "[".
		parser.WithInlineParsers(util.Prioritized(wikiTokenParser{}, 99), util.Prioritized(mathParser{}, 150)),
		parser.WithASTTransformers(util.Prioritized(transformer{}, 100)),
	)
	m.Renderer().AddOptions(renderer.WithNodeRenderers(util.Prioritized(extRenderer{}, 100)))
}
