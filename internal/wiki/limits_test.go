package wiki

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/parser"
)

// Pentest de F8.15 (§17): ninguna página del tamaño máximo cuesta minutos de CPU al
// renderizarla, al sacarle los enlaces o al indexarla. Quedan afuera los casos del núcleo de
// goldmark que siguen creciendo con el cuadrado de un párrafo gigante ("[a](" sin fin, miles de
// "[a]: b" o de "> [!NOTE]" seguidos): tardan segundos y los contiene parseSlots.
func TestPathologicalMarkdown(t *testing.T) {
	n := maxMarkdown - 10
	for name, md := range map[string]string{
		"quotes":       strings.Repeat(">", n),
		"quotes-space": strings.Repeat("> ", n/2),
		"lists":        strings.Repeat("- ", n/2),
		"olists":       strings.Repeat("1. ", n/3),
		"mixed":        strings.Repeat("> - 1. ", n/7),
		"directives":   strings.Repeat(":::tabs\n", n/8),
		"heading-ids":  strings.Repeat("# a\n", n/4),
		"brackets":     strings.Repeat("[", n),
		"backticks":    strings.Repeat("`a", n/2),
		"emph":         strings.Repeat("*a", n/2),
		"wikilinks":    strings.Repeat("[[a", n/3),
		"html-nest":    strings.Repeat("<div>", n/5),
	} {
		done := make(chan time.Duration, 1)
		go func() {
			start := time.Now()
			if _, _, err := Render(md, Options{Base: "/w/ti"}); err != nil {
				t.Error(name, err)
			}
			Links(md)
			IndexDoc("t", "", md, "es")
			done <- time.Since(start)
		}()
		// Holgado para una máquina de CI lenta: sin los topes, varios pasaban del minuto.
		select {
		case d := <-done:
			t.Logf("%-12s %v", name, d.Round(time.Millisecond))
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: more than 10s", name)
		}
	}
}

// Dentro de los topes el Markdown no cambia; pasado el tope, lo que sobra se ve como texto.
func TestLimitsKeepNormalMarkdown(t *testing.T) {
	ok := strings.Repeat("> ", maxNesting) + "hola\n\n" + strings.Repeat("- ", maxNesting) + "x\n" + strings.Repeat("a", maxRun) + "\n"
	if got := breakLongRuns(limitNesting([]byte(ok))); string(got) != ok {
		t.Error("changed markdown within the limits")
	}
	html, _, _ := Render(strings.Repeat(">", maxNesting+3)+" x", Options{})
	if strings.Count(html, "<blockquote>") != maxNesting || !strings.Contains(html, "&gt;&gt;&gt; x") {
		t.Errorf("the excess nesting is not literal text: %d blockquotes", strings.Count(html, "<blockquote>"))
	}
	long := strings.Repeat("a", 2*maxRun+5)
	if got := string(breakLongRuns([]byte(long))); got != long[:maxRun]+" "+long[maxRun:2*maxRun]+" "+long[2*maxRun:] {
		t.Errorf("long run split as %d bytes", len(got))
	}
	html, _, _ = Render(strings.Repeat(":::tabs\n", maxNesting+1), Options{})
	if c := strings.Count(html, `<div class="tabs">`); c != maxNesting {
		t.Errorf("%d nested directives, want %d", c, maxNesting)
	}
}

// Los ids de los títulos son los de goldmark, también con repetidos y con choques entre un
// título "a-1" escrito a mano y el sufijo generado: los anclas que ya existen no cambian.
func TestHeadingIDsMatchGoldmark(t *testing.T) {
	src := []byte("# A\n# a\n## a-1\n# a\n# Título ñ\n# \n# \n# a\n### a-2\n# a\n")
	var want bytes.Buffer
	plain := goldmark.New(goldmark.WithParserOptions(parser.WithAutoHeadingID()))
	if err := plain.Convert(src, &want); err != nil {
		t.Fatal(err)
	}
	got, _, _ := Render(string(src), Options{})
	ids := func(s string) []string {
		var out []string
		for _, part := range strings.Split(s, `id="`)[1:] {
			out = append(out, part[:strings.IndexByte(part, '"')])
		}
		return out
	}
	if w, g := ids(want.String()), ids(got); strings.Join(w, " ") != strings.Join(g, " ") {
		t.Errorf("ids %v, goldmark %v", g, w)
	}
}
