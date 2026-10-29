package branding

import (
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

func TestSanitizeSVG(t *testing.T) {
	in := `<?xml version="1.0"?>
<!-- comentario -->
<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 10 10" onload="alert(1)">
  <script>alert(2)</script>
  <defs><linearGradient id="g"><stop offset="0" stop-color="#fff"/></linearGradient></defs>
  <style>.a{fill:url(#g)}</style>
  <style>@import url(https://evil.example/x.css);</style>
  <s:style xmlns:s="http://www.w3.org/2000/svg">.b{background:url(https://evil.example/p.png)}</s:style>
  <rect class="a" width="10" height="10" fill="url(#g)" onclick="alert(3)"/>
  <rect width="1" height="1" style="fill:url(https://evil.example/t)"/>
  <a href="javascript:alert(4)"><path d="M0 0L1 1"/></a>
  <use xlink:href="#g"/>
  <use href="https://evil.example/sprite.svg#x"/>
  <image href="data:image/png;base64,iVBORw0KGgo="/>
  <image href="data:image/svg+xml;base64,PHN2Zz4="/>
  <foreignObject><div xmlns="http://www.w3.org/1999/xhtml">x</div></foreignObject>
  <set attributeName="href" to="javascript:alert(5)"/>
  <animate attributeName="href" values="javascript:alert(6)"/>
  <text x="1" y="1">Hola &amp; chao</text>
</svg>`
	out, err := sanitizeSVG([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, bad := range []string{"script", "alert", "onload", "onclick", "evil.example", "javascript", "foreignObject",
		"<set", "<animate", "@import", "svg+xml", "comentario", "<?xml"} {
		if strings.Contains(s, bad) {
			t.Errorf("output still has %q:\n%s", bad, s)
		}
	}
	for _, good := range []string{`viewBox="0 0 10 10"`, `fill="url(#g)"`, `xlink:href="#g"`, `.a{fill:url(#g)}`,
		`<path d="M0 0L1 1"></path>`, `data:image/png;base64`, `Hola &amp; chao`, `xmlns:xlink=`} {
		if !strings.Contains(s, good) {
			t.Errorf("output lost %q:\n%s", good, s)
		}
	}
	// Lo que sale es XML bien formado.
	d := xml.NewDecoder(bytes.NewReader(out))
	for {
		if _, err := d.Token(); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("output is not well-formed: %v\n%s", err, s)
		}
	}
}

func TestSanitizeSVGRejects(t *testing.T) {
	for name, in := range map[string]string{
		"no es svg":      `<html><body>x</body></html>`,
		"dos raíces":     `<svg></svg><svg></svg>`,
		"entidad":        `<!DOCTYPE svg [<!ENTITY x "y">]><svg>&x;</svg>`,
		"sin cerrar":     `<svg><g>`,
		"vacío":          ``,
		"texto plano":    `hola`,
		"raíz escondida": `<script>alert(1)</script>`,
	} {
		if _, err := sanitizeSVG([]byte(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func FuzzSanitizeSVG(f *testing.F) {
	f.Add(`<svg onload="x"><script>y</script><a href="javascript:z"/></svg>`)
	f.Add(`<svg><style>@import url(http://a)</style><x:set xmlns:x="s" attributeName="href"/></svg>`)
	f.Add(`<svg><g style="fill:url(http://a)"><use xlink:href=" #a"/></g></svg>`)
	f.Fuzz(func(t *testing.T, in string) {
		out, err := sanitizeSVG([]byte(in))
		if err != nil {
			return
		}
		assertSafe(t, out)
	})
}

// assertSafe recorre lo que sale con un parser aparte y busca lo que nunca debe quedar.
func assertSafe(t *testing.T, out []byte) {
	t.Helper()
	d := xml.NewDecoder(bytes.NewReader(out))
	var inStyle bool
	for {
		tok, err := d.RawToken()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatalf("output is not well-formed: %v: %s", err, out)
		}
		switch e := tok.(type) {
		case xml.StartElement:
			name := strings.ToLower(e.Name.Local)
			if droppedElements[name] {
				t.Fatalf("element %s survived: %s", name, out)
			}
			inStyle = name == "style"
			for _, a := range e.Attr {
				an, v := strings.ToLower(a.Name.Local), strings.ToLower(strings.TrimSpace(a.Value))
				switch {
				case strings.HasPrefix(an, "on"),
					strings.Contains(v, "javascript:"),
					an == "href" && a.Name.Space != "xmlns" && !strings.HasPrefix(v, "#") && !safeDataURI.MatchString(v),
					!internalRefs(v):
					t.Fatalf("attribute %s=%q survived: %s", an, a.Value, out)
				}
			}
		case xml.CharData:
			if inStyle && !internalRefs(string(e)) {
				t.Fatalf("external style survived: %s", out)
			}
		case xml.EndElement:
			inStyle = false
		case xml.Comment, xml.ProcInst, xml.Directive:
			t.Fatalf("%T survived: %s", e, out)
		}
	}
}
