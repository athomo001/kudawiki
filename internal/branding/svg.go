package branding

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"regexp"
	"strings"
)

// Un SVG abierto directo es un documento que puede ejecutar scripts en el dominio de la wiki
// (lección de Wiki.js 3, SPEC §15.1.3). Se reescribe token por token: se quitan los elementos
// que ejecutan, animan atributos o incrustan otros documentos, los atributos on*, y toda
// referencia que no sea interna (#id) o una imagen rasterizada en data:. Además se sirve con
// una CSP sandbox y nosniff.

var errBadSVG = errors.New("invalid svg")

// droppedElements se quitan con todo su contenido. <set> y <animate> pueden cambiar un href a
// javascript: después de cargar.
var droppedElements = map[string]bool{
	"script": true, "foreignobject": true, "iframe": true, "object": true, "embed": true,
	"set": true, "animate": true, "animatemotion": true, "animatetransform": true, "animatecolor": true,
	"handler": true, "listener": true, "audio": true, "video": true,
}

var (
	cssURL      = regexp.MustCompile(`(?i)url\(\s*['"]?\s*([^'")\s]*)`)
	safeDataURI = regexp.MustCompile(`(?i)^data:image/(png|jpeg|gif|webp)[;,]`)
)

func sanitizeSVG(in []byte) ([]byte, error) {
	d := xml.NewDecoder(bytes.NewReader(in))
	var w svgWriter
	for {
		tok, err := d.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errBadSVG
		}
		if err := w.token(tok); err != nil {
			return nil, err
		}
	}
	if !w.root || len(w.stack) != 0 {
		return nil, errBadSVG
	}
	return w.out.Bytes(), nil
}

type svgWriter struct {
	out   bytes.Buffer
	stack []xml.Name // elementos abiertos que se escriben
	skip  int        // profundidad dentro de un elemento que se quita
	root  bool
}

// token escribe un token. Comentarios, instrucciones de proceso y DOCTYPE (entidades) no pasan.
func (w *svgWriter) token(tok xml.Token) error {
	switch t := tok.(type) {
	case xml.StartElement:
		return w.start(t)
	case xml.EndElement:
		return w.end()
	case xml.CharData:
		w.text(t)
	}
	return nil
}

func (w *svgWriter) start(t xml.StartElement) error {
	name := strings.ToLower(t.Name.Local)
	if w.skip > 0 || droppedElements[name] {
		w.skip++
		return nil
	}
	if (!w.root && name != "svg") || (w.root && len(w.stack) == 0) { // una sola raíz, y es <svg>
		return errBadSVG
	}
	w.root = true
	w.out.WriteString("<" + qname(t.Name))
	for _, a := range t.Attr {
		if keepAttr(a) {
			w.out.WriteString(" " + qname(a.Name) + `="`)
			xml.EscapeText(&w.out, []byte(a.Value))
			w.out.WriteString(`"`)
		}
	}
	w.out.WriteString(">")
	w.stack = append(w.stack, t.Name)
	return nil
}

func (w *svgWriter) end() error {
	if w.skip > 0 {
		w.skip--
		return nil
	}
	if len(w.stack) == 0 {
		return errBadSVG
	}
	w.out.WriteString("</" + qname(w.stack[len(w.stack)-1]) + ">")
	w.stack = w.stack[:len(w.stack)-1]
	return nil
}

// text escribe el texto de los elementos. Fuera de la raíz solo hay espacios, y un <style> que
// trae recursos de afuera se vacía.
func (w *svgWriter) text(t xml.CharData) {
	if w.skip > 0 || len(w.stack) == 0 {
		return
	}
	if strings.EqualFold(w.stack[len(w.stack)-1].Local, "style") && !internalRefs(string(t)) {
		return
	}
	xml.EscapeText(&w.out, t)
}

func qname(n xml.Name) string {
	if n.Space != "" {
		return n.Space + ":" + n.Local
	}
	return n.Local
}

func keepAttr(a xml.Attr) bool {
	name := strings.ToLower(a.Name.Local)
	if strings.HasPrefix(name, "on") {
		return false
	}
	v := strings.TrimSpace(a.Value)
	if name == "href" && a.Name.Space != "xmlns" { // href y xlink:href
		return strings.HasPrefix(v, "#") || safeDataURI.MatchString(v)
	}
	if strings.Contains(strings.ToLower(v), "javascript:") {
		return false
	}
	return internalRefs(v)
}

// internalRefs: todo url(...) apunta a un #id del mismo SVG; nada de @import.
func internalRefs(css string) bool {
	if strings.Contains(strings.ToLower(css), "@import") {
		return false
	}
	for _, m := range cssURL.FindAllStringSubmatch(css, -1) {
		if !strings.HasPrefix(m[1], "#") {
			return false
		}
	}
	return true
}
