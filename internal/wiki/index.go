package wiki

import (
	"strings"

	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/athomo001/kudawiki/internal/search"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// IndexDoc arma lo que se indexa de una página (SPEC §9.1): texto plano sin sintaxis ni URLs,
// la descripción y los encabezados aparte (pesan más) y el código aparte (pesa menos), cada uno
// con su raíz. La descripción la pide cada camino que reescribe el índice (guardar, mover,
// reconstruir): si alguno no la pasara, se perdería de la búsqueda.
func IndexDoc(title, description, markdown, lang string) repo.IndexDoc {
	x := extract(markdown)
	d := repo.IndexDoc{
		Lang: lang, Title: title, Description: description,
		Headings: x.headings, Content: x.content, Code: x.code,
	}
	d.TitleStem, d.DescriptionStem = search.Stem(d.Title, lang), search.Stem(d.Description, lang)
	d.HeadingsStem, d.ContentStem = search.Stem(d.Headings, lang), search.Stem(d.Content, lang)
	return d
}

// SectionAt dice bajo qué encabezado cae la coincidencia de un fragmento del buscador (SPEC
// §9.4). El fragmento (snippet() de FTS5) es un trozo del texto indexado, así que se ubica en
// ese mismo texto, que sale del mismo recorrido que anota dónde empieza cada encabezado. Se
// compara sin contar los espacios repetidos. Si la coincidencia está antes del primer
// encabezado, no hay sección.
func SectionAt(markdown, snippet string) (Heading, bool) {
	s := strings.TrimPrefix(snippet, "…")
	hit := strings.Index(s, repo.HitStart)
	if hit < 0 {
		return Heading{}, false
	}
	unmark := strings.NewReplacer(repo.HitStart, "", repo.HitEnd, "")
	plain, _ := collapse(strings.TrimSuffix(strings.TrimSpace(unmark.Replace(s)), "…"))
	before, _ := collapse(s[:hit])
	x := extract(markdown)
	content, offsets := collapse(x.content)
	at := strings.Index(content, strings.TrimSpace(plain))
	if at < 0 {
		return Heading{}, false
	}
	at = offsets[min(at+len(strings.TrimLeft(before, " ")), len(offsets)-1)]
	var h Heading
	found := false
	for _, m := range x.sections {
		if m.at > at {
			break
		}
		h, found = m.heading, true
	}
	return h, found
}

// collapse deja un solo espacio donde hay varios, y dice dónde estaba cada byte en el original.
func collapse(s string) (string, []int) {
	var b strings.Builder
	offsets := make([]int, 0, len(s))
	space := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		isSpace := c == ' ' || c == '\n' || c == '\t'
		if isSpace && space {
			continue
		}
		space = isSpace
		if isSpace {
			c = ' '
		}
		b.WriteByte(c)
		offsets = append(offsets, i)
	}
	return b.String(), offsets
}

type sectionMark struct {
	at      int // posición en content donde empieza la sección
	heading Heading
}

type extracted struct {
	content, headings, code string
	sections                []sectionMark
}

// extract recorre la página una vez: el texto de cada parte y dónde empieza cada sección.
// checkSecrets busca los textos ocultos que quedarían a la vista. Se mira en el árbol del
// parser: dentro de código no es texto oculto.
//   - Un título de sección con un [[secret:…]], o dentro de un :::secret: el título se indexa y
//     arma el ancla de la URL.
//   - Un "[[secret:" que el parser no reconoció (sin "]]" en la misma línea, o cruzando
//     párrafos) queda como texto común: se publicaría la contraseña tal cual.
func checkSecrets(markdown string) error {
	src := []byte(markdown)
	pc := parser.NewContext()
	pc.Set(optionsKey, &Options{})
	doc := md.Parser().Parse(text.NewReader(src), parser.WithContext(pc))
	var found error
	inSecret := 0
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		switch n := n.(type) {
		case *directive:
			if n.name == "secret" && entering {
				inSecret++
			} else if n.name == "secret" {
				inSecret--
			}
		case *ast.Heading:
			if entering && inSecret > 0 {
				found = ErrSecretInHeading
			}
		case *wikiToken:
			if _, secret := n.secret(); entering && secret && n.Parent() != nil && n.Parent().Kind() == ast.KindHeading {
				found = ErrSecretInHeading
			}
		case *ast.CodeSpan:
			return ast.WalkSkipChildren, nil
		}
		if entering && found == nil && n.Type() == ast.TypeBlock && strings.Contains(inlineText(n, src), "[["+secretPrefix) {
			found = ErrSecretUnclosed
		}
		if found != nil {
			return ast.WalkStop, nil
		}
		return ast.WalkContinue, nil
	})
	return found
}

// inlineText es el texto común de los hijos directos de un bloque, seguido, sin el de código:
// el parser parte "[[secret:" en varios nodos de texto al no reconocerlo.
func inlineText(block ast.Node, src []byte) string {
	var b strings.Builder
	for c := block.FirstChild(); c != nil; c = c.NextSibling() {
		if t, ok := c.(*ast.Text); ok {
			b.Write(t.Segment.Value(src))
		}
	}
	return b.String()
}

func extract(markdown string) extracted {
	src := []byte(markdown)
	pc := parser.NewContext()
	pc.Set(optionsKey, &Options{})
	doc := md.Parser().Parse(text.NewReader(src), parser.WithContext(pc))

	var content, headings, code strings.Builder
	var sections []sectionMark
	add := func(b *strings.Builder, s string) {
		b.WriteString(s)
		b.WriteByte(' ')
	}
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Heading:
			text := string(n.Lines().Value(src))
			// El id lo pone el mismo parser que usa el render: es el ancla del encabezado en la página.
			if id, ok := n.AttributeString("id"); ok {
				sections = append(sections, sectionMark{at: content.Len(), heading: Heading{Level: n.Level, ID: string(id.([]byte)), Text: text}})
			}
			add(&headings, text)
			return ast.WalkSkipChildren, nil
		case *ast.FencedCodeBlock, *ast.CodeBlock:
			add(&code, string(n.Lines().Value(src)))
			return ast.WalkSkipChildren, nil
		case *ast.CodeSpan:
			for c := n.FirstChild(); c != nil; c = c.NextSibling() {
				if t, ok := c.(*ast.Text); ok {
					add(&code, string(t.Segment.Value(src)))
				}
			}
			return ast.WalkSkipChildren, nil
		case *mermaid:
			add(&code, n.source)
		case *directive:
			if n.name == "secret" {
				return ast.WalkSkipChildren, nil
			}
		case *wikiToken:
			// Un texto oculto no se busca ni aparece en los fragmentos de resultados (§4.10).
			if _, secret := n.secret(); !secret {
				add(&content, strings.ReplaceAll(n.raw, "|", " "))
			}
		case *ast.Text:
			add(&content, string(n.Segment.Value(src)))
		}
		return ast.WalkContinue, nil
	})
	return extracted{content: content.String(), headings: headings.String(), code: code.String(), sections: sections}
}
