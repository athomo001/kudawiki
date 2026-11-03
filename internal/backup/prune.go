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

// KeepPreMigrate es cuántos respaldos previos a migrar se guardan (§13.3, F8-B.6, decisión del
// dueño del 2026-11-03): alcanza para volver tres actualizaciones atrás sin llenar el disco.
const KeepPreMigrate = 3

// Prune borra los respaldos diarios que ya no hacen falta: guarda el último de cada uno de
// los Daily días más recientes, de las Weekly semanas y de los Monthly meses. Los manuales no
// se tocan: los hizo alguien a propósito. Los previos a migrar tienen su propia regla
// (KeepLatest). Devuelve lo que borró.
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

// KeepLatest borra los respaldos de tipo kind salvo los n más nuevos. Devuelve lo que borró.
func KeepLatest(dir string, kind Kind, n int) ([]string, error) {
	list, err := List(dir) // el más nuevo primero
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, e := range list {
		if e.Kind != kind {
			continue
		}
		if n > 0 {
			n--
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name)); err != nil {
			return removed, err
		}
		removed = append(removed, e.Name)
	}
	return removed, nil
}
