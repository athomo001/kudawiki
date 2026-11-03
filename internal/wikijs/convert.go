// Package wikijs importa una instalación de Wiki.js 2.x (SPEC §13.8): desde la carpeta de su
// almacenamiento en disco o Git, o directo desde su base de datos PostgreSQL o MySQL. Se lee el
// formato, no se usa código de Wiki.js (AGPL, §19.9): todo esto está escrito desde cero.
package wikijs

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// headingRe es un encabezado ATX con sus atributos al final: "## Título {.tabset}".
var headingRe = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*(\{[^}]*\})?\s*$`)

// fenceRe abre o cierra un bloque de código; dentro de uno nada se convierte.
var fenceRe = regexp.MustCompile("^\\s*(```|~~~)")

// ConvertTabsets traduce las pestañas de Wiki.js (un encabezado con {.tabset} y, debajo, un
// encabezado de un nivel más por pestaña) a la directiva de Kudawiki (§4.10):
//
//	:::tabs
//	::: tab Linux
//	…
//	:::
//	:::
//
// El encabezado {.tabset} no se ve en Wiki.js, así que tampoco queda aquí. El grupo termina en
// el siguiente encabezado del mismo nivel o más alto.
func ConvertTabsets(md string) string {
	lines := strings.Split(md, "\n")
	var out []string
	inFence, level, open := false, 0, false
	closeTab := func() {
		if open {
			out = append(out, ":::")
			open = false
		}
	}
	closeGroup := func() {
		if level > 0 {
			closeTab()
			out = append(out, ":::")
			level = 0
		}
	}
	for _, line := range lines {
		if fenceRe.MatchString(line) {
			inFence = !inFence
		}
		m := headingRe.FindStringSubmatch(line)
		if inFence || m == nil {
			out = append(out, line)
			continue
		}
		n := len(m[1])
		switch {
		case strings.Contains(m[3], ".tabset"):
			closeGroup()
			out = append(out, ":::tabs")
			level = n
		case level > 0 && n == level+1:
			closeTab()
			out = append(out, "::: tab "+m[2])
			open = true
		case level > 0 && n <= level:
			closeGroup()
			out = append(out, line)
		default:
			out = append(out, line)
		}
	}
	closeGroup()
	return strings.Join(out, "\n")
}

// classRe encuentra los atributos de clase de Markdown: {.is-info}, {.links-list}.
var classRe = regexp.MustCompile(`\{\.([a-z0-9-]+)[^}\n]*\}`)

// readClasses son las clases de Wiki.js que Kudawiki muestra igual (§4.10).
var readClasses = []string{"is-info", "is-warning", "is-danger", "is-success", "tabset"}

// UnsupportedClasses son las clases que Kudawiki no muestra ({.links-list}, {.grid-list},
// {.align-right}…): el contenido queda, pero sin ese formato. Van al informe como conversiones
// dudosas.
func UnsupportedClasses(md string) []string {
	var out []string
	for _, m := range classRe.FindAllStringSubmatch(md, -1) {
		if !slices.Contains(readClasses, m[1]) && !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	return out
}

// HTMLToMarkdown convierte una página del editor visual de Wiki.js. Cubre lo que ese editor
// produce (encabezados, párrafos, énfasis, enlaces, imágenes, listas, tablas, código, citas);
// lo demás queda como texto, por eso el informe siempre avisa que pudo perderse formato.
func HTMLToMarkdown(src string) (string, error) {
	nodes, err := html.ParseFragment(strings.NewReader(src), &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body})
	if err != nil {
		return "", err
	}
	var c converter
	for _, n := range nodes {
		c.block(n, "")
	}
	out := regexp.MustCompile(`\n{3,}`).ReplaceAllString(c.b.String(), "\n\n")
	return strings.TrimSpace(out) + "\n", nil
}

type converter struct{ b strings.Builder }

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// block escribe un nodo de bloque; prefix es lo que va al comienzo de cada línea (citas, listas).
func (c *converter) block(n *html.Node, prefix string) {
	if n.Type == html.TextNode {
		if t := strings.TrimSpace(collapse(n.Data)); t != "" {
			c.b.WriteString(prefix + t + "\n\n")
		}
		return
	}
	if n.Type != html.ElementNode {
		return
	}
	switch n.DataAtom {
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		c.b.WriteString(prefix + strings.Repeat("#", int(n.Data[1]-'0')) + " " + c.inline(n) + "\n\n")
	case atom.P:
		if t := c.inline(n); t != "" {
			c.b.WriteString(prefix + t + "\n\n")
		}
	case atom.Hr:
		c.b.WriteString(prefix + "---\n\n")
	case atom.Pre:
		lang := ""
		if code := firstChild(n, atom.Code); code != nil {
			lang = strings.TrimPrefix(attr(code, "class"), "language-")
		}
		body := strings.TrimRight(text(n), "\n")
		c.b.WriteString(prefix + "```" + lang + "\n")
		for _, l := range strings.Split(body, "\n") {
			c.b.WriteString(prefix + l + "\n")
		}
		c.b.WriteString(prefix + "```\n\n")
	case atom.Blockquote:
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			c.block(ch, prefix+"> ")
		}
		// Los avisos de Wiki.js son citas con clase: {.is-info} los mantiene como aviso (§4.10).
		if cls := attr(n, "class"); strings.HasPrefix(cls, "is-") {
			c.b.WriteString(prefix + "{." + strings.Fields(cls)[0] + "}\n\n")
		}
	case atom.Ul, atom.Ol:
		i := 1
		for li := n.FirstChild; li != nil; li = li.NextSibling {
			if li.DataAtom != atom.Li {
				continue
			}
			mark := "- "
			if n.DataAtom == atom.Ol {
				mark = fmt.Sprintf("%d. ", i)
				i++
			}
			c.listItem(li, prefix, mark)
		}
		c.b.WriteString("\n")
	case atom.Table:
		c.table(n, prefix)
	case atom.Script, atom.Style, atom.Head:
	case atom.Img, atom.A, atom.Strong, atom.B, atom.Em, atom.I, atom.Code, atom.Span, atom.S, atom.Del, atom.Br:
		if t := strings.TrimSpace(c.node(n)); t != "" {
			c.b.WriteString(prefix + t + "\n\n")
		}
	default: // div, section, figure…: su contenido
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			c.block(ch, prefix)
		}
	}
}

func (c *converter) listItem(li *html.Node, prefix, mark string) {
	var inl strings.Builder
	var nested []*html.Node
	for ch := li.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.DataAtom == atom.Ul || ch.DataAtom == atom.Ol {
			nested = append(nested, ch)
			continue
		}
		if ch.DataAtom == atom.P {
			inl.WriteString(c.inline(ch) + " ")
			continue
		}
		inl.WriteString(c.node(ch))
	}
	c.b.WriteString(prefix + mark + strings.TrimSpace(inl.String()) + "\n")
	for _, l := range nested {
		var sub converter
		sub.block(l, strings.Repeat(" ", len(mark)))
		c.b.WriteString(strings.TrimRight(sub.b.String(), "\n") + "\n")
	}
}

func (c *converter) table(t *html.Node, prefix string) {
	var rows [][]string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.DataAtom == atom.Tr {
			var row []string
			for cell := n.FirstChild; cell != nil; cell = cell.NextSibling {
				if cell.DataAtom == atom.Td || cell.DataAtom == atom.Th {
					row = append(row, strings.ReplaceAll(c.inline(cell), "|", `\|`))
				}
			}
			rows = append(rows, row)
			return
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(t)
	if len(rows) == 0 {
		return
	}
	width := 0
	for _, r := range rows {
		width = max(width, len(r))
	}
	line := func(r []string) string {
		for len(r) < width {
			r = append(r, "")
		}
		return prefix + "| " + strings.Join(r, " | ") + " |\n"
	}
	c.b.WriteString(line(rows[0]))
	c.b.WriteString(prefix + "|" + strings.Repeat(" --- |", width) + "\n")
	for _, r := range rows[1:] {
		c.b.WriteString(line(r))
	}
	c.b.WriteString("\n")
}

// inline convierte el contenido de un nodo a Markdown de una línea.
func (c *converter) inline(n *html.Node) string {
	var b strings.Builder
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		b.WriteString(c.node(ch))
	}
	return strings.TrimSpace(b.String())
}

// node convierte un nodo en línea.
func (c *converter) node(ch *html.Node) string {
	switch {
	case ch.Type == html.TextNode:
		return collapse(ch.Data)
	case ch.Type != html.ElementNode:
		return ""
	case ch.DataAtom == atom.Br:
		return "  \n"
	case ch.DataAtom == atom.Strong || ch.DataAtom == atom.B:
		return wrap("**", c.inline(ch))
	case ch.DataAtom == atom.Em || ch.DataAtom == atom.I:
		return wrap("*", c.inline(ch))
	case ch.DataAtom == atom.S || ch.DataAtom == atom.Del:
		return wrap("~~", c.inline(ch))
	case ch.DataAtom == atom.Code:
		return "`" + text(ch) + "`"
	case ch.DataAtom == atom.A:
		return "[" + c.inline(ch) + "](" + attr(ch, "href") + ")"
	case ch.DataAtom == atom.Img:
		return "![" + attr(ch, "alt") + "](" + attr(ch, "src") + ")"
	case ch.DataAtom == atom.Script || ch.DataAtom == atom.Style:
		return ""
	}
	return c.inline(ch)
}

func wrap(mark, s string) string {
	if strings.TrimSpace(s) == "" {
		return s
	}
	return mark + s + mark
}

var spaceRe = regexp.MustCompile(`\s+`)

func collapse(s string) string { return spaceRe.ReplaceAllString(s, " ") }

func text(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		b.WriteString(text(ch))
	}
	return b.String()
}

func firstChild(n *html.Node, a atom.Atom) *html.Node {
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.DataAtom == a {
			return ch
		}
	}
	return nil
}
