package wiki

import (
	"errors"
	"slices"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"
)

// ErrMergeConflict: la página y la propuesta cambiaron las mismas líneas, o líneas vecinas,
// desde la revisión base (§5.2).
var ErrMergeConflict = errors.New("needs_rebase")

// lineEdit reemplaza las líneas base[start:end] por lines (start == end es una inserción).
type lineEdit struct {
	start, end int
	lines      []string
	side       int
}

// Merge3 mezcla por líneas lo que cambiaron ours y theirs desde base (§5.2). Es conservador
// como git: dos cambios que se tocan, aunque sea en líneas contiguas, son conflicto, porque
// juntarlos a ciegas puede dejar un párrafo o una tabla sin sentido. El aprobador o el autor
// lo resuelve a mano.
func Merge3(base, ours, theirs string) (string, error) {
	if ours == theirs || theirs == base {
		return ours, nil
	}
	if ours == base {
		return theirs, nil
	}
	lines := splitLines(base)
	edits := append(lineEdits(base, ours, 0), lineEdits(base, theirs, 1)...)
	slices.SortStableFunc(edits, func(a, b lineEdit) int {
		if a.start != b.start {
			return a.start - b.start
		}
		return a.end - b.end
	})

	var out strings.Builder
	pos := 0
	for i := 0; i < len(edits); {
		// Un grupo son los cambios que se superponen o se tocan.
		start, end, j := edits[i].start, edits[i].end, i+1
		for j < len(edits) && edits[j].start <= end {
			end = max(end, edits[j].end)
			j++
		}
		region, err := resolve(lines, start, end, edits[i:j])
		if err != nil {
			return "", err
		}
		out.WriteString(strings.Join(lines[pos:start], ""))
		out.WriteString(region)
		pos, i = end, j
	}
	out.WriteString(strings.Join(lines[pos:], ""))
	return out.String(), nil
}

// resolve decide un grupo: si solo un lado cambió, gana ese; si los dos, solo si llegaron al
// mismo texto.
func resolve(lines []string, start, end int, group []lineEdit) (string, error) {
	var bySide [2][]lineEdit
	for _, e := range group {
		bySide[e.side] = append(bySide[e.side], e)
	}
	ours, theirs := apply(lines, start, end, bySide[0]), apply(lines, start, end, bySide[1])
	switch {
	case len(bySide[1]) == 0, ours == theirs:
		return ours, nil
	case len(bySide[0]) == 0:
		return theirs, nil
	}
	return "", ErrMergeConflict
}

// apply arma las líneas base[start:end] con los cambios de un solo lado aplicados.
func apply(lines []string, start, end int, edits []lineEdit) string {
	var b strings.Builder
	pos := start
	for _, e := range edits {
		b.WriteString(strings.Join(lines[pos:e.start], ""))
		b.WriteString(strings.Join(e.lines, ""))
		pos = e.end
	}
	b.WriteString(strings.Join(lines[pos:end], ""))
	return b.String()
}

// lineEdits convierte el diff por líneas de base a other en reemplazos sobre base.
func lineEdits(base, other string, side int) []lineEdit {
	dmp := diffmatchpatch.New()
	a, b, table := dmp.DiffLinesToRunes(base, other)
	var out []lineEdit
	var cur *lineEdit
	pos := 0
	flush := func() {
		if cur != nil {
			out = append(out, *cur)
			cur = nil
		}
	}
	for _, d := range dmp.DiffMainRunes(a, b, false) {
		runes := []rune(d.Text)
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			flush()
			pos += len(runes)
		case diffmatchpatch.DiffDelete:
			if cur == nil {
				cur = &lineEdit{start: pos, end: pos, side: side}
			}
			pos += len(runes)
			cur.end = pos
		case diffmatchpatch.DiffInsert:
			if cur == nil {
				cur = &lineEdit{start: pos, end: pos, side: side}
			}
			for _, r := range runes {
				cur.lines = append(cur.lines, table[r])
			}
		}
	}
	flush()
	return out
}

// splitLines corta igual que DiffLinesToRunes: cada línea con su salto, la última sin él si el
// texto no termina en salto. Así las posiciones de los cambios calzan con estas líneas.
func splitLines(s string) []string {
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
