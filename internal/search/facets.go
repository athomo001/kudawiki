package search

import (
	"cmp"
	"slices"
	"strings"
	"time"
)

// Facet es un valor de un filtro de la página de resultados y cuántos candidatos lo tienen (SPEC §9.4).
type Facet struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// DateFacet cuenta los candidatos modificados en los últimos Days días.
type DateFacet struct {
	Days  int `json:"days"`
	Count int `json:"count"`
}

type Facets struct {
	Tags    []Facet     `json:"tags"`
	Folders []Facet     `json:"folders"`
	Authors []Facet     `json:"authors"`
	Dates   []DateFacet `json:"dates"`
}

// Candidate es lo que se cuenta de cada página candidata. Las páginas ya vienen filtradas por
// el Scope de quien busca: los conteos nunca incluyen lo que no puede ver (§17).
type Candidate struct {
	Slug      string
	UpdatedAt time.Time
	Tags      []string
	Authors   []string
}

const maxFacetValues = 10

var dateWindows = []int{7, 30, 365}

// CountFacets cuenta etiquetas, carpetas, autores y fechas sobre los candidatos. Las carpetas
// son las del nivel siguiente a path (sin path, las de primer nivel), para ir bajando por el
// árbol. La vigencia llega con F5.
func CountFacets(cands []Candidate, path string, now time.Time) Facets {
	tags, folders, authors := map[string]int{}, map[string]int{}, map[string]int{}
	dates := make([]DateFacet, len(dateWindows))
	for i, d := range dateWindows {
		dates[i].Days = d
	}
	for _, c := range cands {
		for _, t := range c.Tags {
			tags[t]++
		}
		for _, a := range c.Authors {
			authors[a]++
		}
		if folder, ok := folderOf(c.Slug, path); ok {
			folders[folder]++
		}
		for i, d := range dateWindows {
			if !c.UpdatedAt.Before(now.AddDate(0, 0, -d)) {
				dates[i].Count++
			}
		}
	}
	return Facets{Tags: top(tags), Folders: top(folders), Authors: top(authors), Dates: dates}
}

// folderOf es la carpeta del nivel siguiente a path en la que está slug, si está en una.
func folderOf(slug, path string) (string, bool) {
	rel := slug
	if path != "" {
		var ok bool
		if rel, ok = strings.CutPrefix(slug, path+"/"); !ok {
			return "", false
		}
	}
	seg, _, ok := strings.Cut(rel, "/")
	if !ok {
		return "", false // la página está en este nivel, no dentro de una carpeta
	}
	if path != "" {
		seg = path + "/" + seg
	}
	return seg, true
}

// top deja los valores más frecuentes; a igual conteo, en orden alfabético.
func top(m map[string]int) []Facet {
	out := make([]Facet, 0, len(m))
	for v, n := range m {
		out = append(out, Facet{Value: v, Count: n})
	}
	slices.SortFunc(out, func(a, b Facet) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(a.Value, b.Value))
	})
	return out[:min(len(out), maxFacetValues)]
}
