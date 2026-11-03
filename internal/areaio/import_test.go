package areaio

import (
	"errors"
	"fmt"
	"testing"
	"testing/fstest"
)

// Pentest de F8.15: lo que se descomprime tiene un tope total, aunque cada archivo por separado
// sea rechazado por grande. La misma porción de memoria sirve para todos los archivos.
func TestReadBudget(t *testing.T) {
	big := make([]byte, maxPageBytes+1)
	fsys := fstest.MapFS{}
	for i := range maxReadBytes/maxPageBytes + 2 {
		fsys[fmt.Sprintf("p%03d.md", i)] = &fstest.MapFile{Data: big}
	}
	if _, _, err := Read(fsys); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	ok := fstest.MapFS{"a.md": {Data: []byte("# Hola\n\ntexto")}, "a/b.md": {Data: []byte("x")}}
	pages, skipped, err := Read(ok)
	if err != nil || len(pages) != 2 || len(skipped) != 0 {
		t.Fatalf("pages %d, skipped %v, err %v", len(pages), skipped, err)
	}
}
