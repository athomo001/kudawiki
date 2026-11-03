package wiki

import (
	"bytes"
	"fmt"
	"runtime"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// El render no puede costar más que proporcional al tamaño de la página: cualquier editor
// escribe Markdown, la vista previa renderiza mientras se escribe y el guardado lo vuelve a
// parsear para los enlaces, la búsqueda y los secretos. El pentest de F8.15 (§17) encontró
// entradas de 512 KiB que tomaban minutos de CPU; los topes de este archivo las dejan en
// alrededor de un segundo sin cambiar nada de lo que una persona escribe de verdad.
const (
	// maxNesting es cuántas citas, listas o directivas anidadas se respetan. goldmark recorre
	// todos los bloques abiertos en cada línea: el costo crece con el cuadrado del anidamiento.
	maxNesting = 32
	// maxRun es el largo máximo de una tira sin espacios. Varias reglas de goldmark (el destino
	// de un enlace, por ejemplo) buscan hasta el final de la tira desde cada intento, así que
	// el costo es proporcional al cuadrado de su largo.
	maxRun = 16 << 10
)

// parseSlots acota cuántos parseos corren a la vez. Al núcleo de goldmark le quedan casos que
// crecen con el cuadrado del largo de un párrafo (decenas de miles de definiciones de
// referencia o de "[" en uno solo): una página así de un editor tarda segundos, pero ocupa
// como mucho la mitad de los núcleos y el resto del servidor sigue respondiendo (F8.15).
var parseSlots = make(chan struct{}, max(1, runtime.GOMAXPROCS(0)/2))

// parse es la única forma de parsear Markdown en el paquete: con las entradas acotadas, los ids
// de los títulos en tiempo lineal y un lugar en parseSlots.
func parse(markdown string, opts *Options) (ast.Node, []byte) {
	parseSlots <- struct{}{}
	defer func() { <-parseSlots }()
	src := breakLongRuns(limitNesting([]byte(markdown)))
	pc := parser.NewContext(parser.WithIDs(&headingIDs{used: map[string]bool{}, next: map[string]int{}}))
	pc.Set(optionsKey, opts)
	return md.Parser().Parse(text.NewReader(src), parser.WithContext(pc)), src
}

// headingIDs da a los títulos los mismos ids que goldmark ("a", "a-1", "a-2"…), así los
// anclas que ya existen no cambian. goldmark busca el primer sufijo libre desde 1 en cada
// título repetido; acá cada base recuerda por dónde va.
type headingIDs struct {
	used map[string]bool
	next map[string]int
}

func (h *headingIDs) Generate(value []byte, kind ast.NodeKind) []byte {
	// Un contexto nuevo no tiene ids usados: su generador devuelve la base sin sufijo, con
	// las reglas de goldmark tal cual.
	base := string(parser.NewContext().IDs().Generate(value, kind))
	if !h.used[base] {
		h.used[base] = true
		return []byte(base)
	}
	// Todo sufijo menor que next[base] ya está usado: Put solo agrega, nunca libera.
	for i := max(h.next[base], 1); ; i++ {
		id := fmt.Sprintf("%s-%d", base, i)
		if !h.used[id] {
			h.used[id] = true
			h.next[base] = i + 1
			return []byte(id)
		}
	}
}

func (h *headingIDs) Put(value []byte) { h.used[string(value)] = true }

// breakLongRuns parte con un espacio cada tira sin espacios de más de maxRun bytes. También
// dentro de un bloque de código: detectar los bloques acá sin el parser abriría la puerta a
// uno que este paso crea código y goldmark no. Sin tiras así devuelve src sin copiarlo.
func breakLongRuns(src []byte) []byte {
	var out []byte
	run, last := 0, 0
	for i, c := range src {
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			run = 0
			continue
		}
		if run++; run > maxRun {
			out = append(out, src[last:i]...)
			out = append(out, ' ')
			last, run = i, 1
		}
	}
	if out == nil {
		return src
	}
	return append(out, src[last:]...)
}

// limitNesting deja como texto los marcadores de cita y de lista que pasan de maxNesting en el
// comienzo de una línea: les antepone una barra, así se ven tal cual y no abren otro nivel.
// Sin líneas así devuelve src sin copiarlo.
func limitNesting(src []byte) []byte {
	var out []byte
	start := 0
	for start < len(src) {
		end := bytes.IndexByte(src[start:], '\n')
		if end < 0 {
			end = len(src)
		} else {
			end += start + 1
		}
		line := src[start:end]
		if cut := excessMarker(line); cut >= 0 {
			if out == nil {
				out = append(make([]byte, 0, len(src)+16), src[:start]...)
			}
			out = append(out, line[:cut]...)
			out = append(out, '\\')
			out = append(out, line[cut:]...)
		} else if out != nil {
			out = append(out, line...)
		}
		start = end
	}
	if out == nil {
		return src
	}
	return out
}

// excessMarker devuelve dónde empieza el marcador número maxNesting+1 del comienzo de la línea,
// o -1 si no lo hay. Cuenta citas (">") y viñetas ("-", "*", "+" o "1." / "1)" seguidos de
// espacio), con hasta tres espacios antes de cada uno.
func excessMarker(line []byte) int {
	i, depth := 0, 0
	for {
		for n := 0; n < 3 && i < len(line) && line[i] == ' '; n++ {
			i++
		}
		width := markerWidth(line[i:])
		if width == 0 {
			return -1
		}
		if depth == maxNesting {
			return i
		}
		depth++
		i += width
	}
}

// markerWidth es el largo del marcador de cita o lista al comienzo de b (con su espacio), o 0.
func markerWidth(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	space := func(j int) bool { return j == len(b) || b[j] == ' ' || b[j] == '\t' || b[j] == '\n' || b[j] == '\r' }
	switch c := b[0]; {
	case c == '>':
		if len(b) > 1 && (b[1] == ' ' || b[1] == '\t') {
			return 2
		}
		return 1
	case c == '-' || c == '*' || c == '+':
		if space(1) && len(b) > 1 {
			return 2
		}
	case c >= '0' && c <= '9':
		j := 1
		for j < len(b) && j < 9 && b[j] >= '0' && b[j] <= '9' {
			j++
		}
		if j < len(b) && (b[j] == '.' || b[j] == ')') && space(j+1) && j+1 < len(b) {
			return j + 2
		}
	}
	return 0
}
