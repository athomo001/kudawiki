package backup

import (
	"errors"
	"os"
	"slices"
	"strings"
	"syscall"
	"time"
)

// Entry es un respaldo de la carpeta, tal como lo dice su nombre (Name): leer el manifiesto
// obliga a recorrer el archivo entero, y la lista del panel no lo necesita.
type Entry struct {
	Name      string    `json:"name"`
	Kind      Kind      `json:"kind"`
	CreatedAt time.Time `json:"created_at"`
	Size      int64     `json:"size"`
}

// List son los respaldos de dir, el más nuevo primero. Lo que no tiene un nombre nuestro no se
// lista: puede ser un temporal a medio escribir o algo que dejó quien opera.
func List(dir string) ([]Entry, error) {
	entries, err := os.ReadDir(dir)
	// Una carpeta que es un archivo tampoco tiene respaldos: la pantalla tiene que seguir en pie
	// para mostrar por qué falló el último (en Windows ya llega como ErrNotExist).
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, e := range entries {
		rest, ok := strings.CutPrefix(e.Name(), "kudawiki-")
		if !ok || e.IsDir() || !strings.HasSuffix(rest, ".tar.zst") {
			continue
		}
		rest = strings.TrimSuffix(rest, ".tar.zst")
		for _, k := range []Kind{Daily, Manual, PreMigrate, PreRotate, PreReset} {
			stamp, ok := strings.CutPrefix(rest, string(k)+"-")
			if !ok {
				continue
			}
			at, err := time.Parse(timeLayout, stamp)
			if err != nil {
				break
			}
			info, err := e.Info()
			if err != nil {
				return nil, err
			}
			out = append(out, Entry{Name: e.Name(), Kind: k, CreatedAt: at, Size: info.Size()})
			break
		}
	}
	slices.SortFunc(out, func(a, b Entry) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return out, nil
}
