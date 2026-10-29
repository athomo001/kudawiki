package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Retention es cuántos respaldos automáticos se guardan (SPEC §13.3, configurable).
type Retention struct {
	Daily, Weekly, Monthly int
}

// Prune borra los respaldos automáticos que ya no hacen falta: guarda el último de cada uno de
// los Daily días más recientes, de las Weekly semanas y de los Monthly meses. Los manuales y
// los previos a migrar no se tocan: los maneja quien opera. Devuelve lo que borró.
func Prune(dir string, keep Retention) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	type backup struct {
		name string
		at   time.Time
	}
	var all []backup
	prefix := fmt.Sprintf("kudawiki-%s-", Daily)
	for _, e := range entries {
		stamp, ok := strings.CutPrefix(e.Name(), prefix)
		if !ok || e.IsDir() {
			continue
		}
		at, err := time.Parse(timeLayout, strings.TrimSuffix(stamp, ".tar.zst"))
		if err != nil {
			continue // no es un nombre nuestro: no se toca
		}
		all = append(all, backup{e.Name(), at})
	}
	slices.SortFunc(all, func(a, b backup) int { return b.at.Compare(a.at) }) // el más nuevo primero

	kept := map[string]bool{}
	bucket := func(n int, key func(time.Time) string) {
		seen := map[string]bool{}
		for _, b := range all {
			k := key(b.at)
			if seen[k] {
				continue
			}
			if len(seen) == n {
				return
			}
			seen[k] = true
			kept[b.name] = true // el más nuevo de ese día, semana o mes
		}
	}
	bucket(keep.Daily, func(t time.Time) string { return t.Format("2006-01-02") })
	bucket(keep.Weekly, func(t time.Time) string { y, w := t.ISOWeek(); return fmt.Sprintf("%d-%02d", y, w) })
	bucket(keep.Monthly, func(t time.Time) string { return t.Format("2006-01") })

	var removed []string
	for _, b := range all {
		if kept[b.name] {
			continue
		}
		if err := os.Remove(filepath.Join(dir, b.name)); err != nil {
			return removed, err
		}
		removed = append(removed, b.name)
	}
	return removed, nil
}
