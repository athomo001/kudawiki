package httpapi

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/athomo001/kudawiki/internal/search"
	"github.com/athomo001/kudawiki/internal/wiki"
)

const (
	pageSize      = 20
	maxCandidates = 200 // SPEC §9.5: más allá no se pagina
	searchTimeout = 3 * time.Second
)

type snippetPart struct {
	Text string `json:"text"`
	Hit  bool   `json:"hit,omitempty"`
}

// split convierte el fragmento con marcas en partes de texto: la interfaz las pinta como texto
// y resalta las coincidencias sin {@html}.
func split(snippet string) []snippetPart {
	// Nunca null: una búsqueda solo con filtros (tag:, estado:) no tiene fragmento, y la interfaz
	// pregunta por su largo.
	parts := []snippetPart{}
	for snippet != "" {
		before, rest, found := strings.Cut(snippet, repo.HitStart)
		if before != "" {
			parts = append(parts, snippetPart{Text: before})
		}
		if !found {
			break
		}
		hit, after, _ := strings.Cut(rest, repo.HitEnd)
		parts = append(parts, snippetPart{Text: hit, Hit: true})
		snippet = after
	}
	return parts
}

func (a api) search(w http.ResponseWriter, r *http.Request) {
	// Tope por búsqueda: una consulta lenta se corta en vez de ocupar el servidor (SPEC §9.5).
	ctx, cancel := context.WithTimeout(r.Context(), searchTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	q, err := search.Parse(r.URL.Query().Get("q"))
	if errors.Is(err, search.ErrEmptyQuery) {
		a.json(w, http.StatusOK, map[string]any{"results": []any{}})
		return
	}
	q.ByDate = r.URL.Query().Get("sort") == "date"
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	page = max(page, 1)
	offset := (page - 1) * pageSize
	if offset >= maxCandidates {
		a.json(w, http.StatusOK, map[string]any{"results": []any{}})
		return
	}
	// Los candidatos (hasta 200, §9.5) dan la página que se muestra y los conteos de los filtros.
	cands, err := a.Read.Search(r.Context(), scopeOf(r), q, maxCandidates)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	hits := cands[min(offset, len(cands)):min(offset+pageSize, len(cands))]
	if err := a.Read.Snippets(r.Context(), scopeOf(r), q, hits); err != nil {
		a.fail(w, r, err)
		return
	}
	out := map[string]any{"more": len(cands) > offset+pageSize}
	results := make([]map[string]any, 0, len(hits))
	for _, h := range hits {
		item := map[string]any{"slug": h.Slug, "title": h.Title, "description": h.Description, "icon": h.Icon, "snippet": split(h.Snippet)}
		// La sección donde está la coincidencia, para mostrarla y saltar a ella (§9.4).
		if sec, ok := wiki.SectionAt(h.Markdown, h.Snippet); ok {
			item["section"] = map[string]string{"id": sec.ID, "text": sec.Text}
		}
		results = append(results, item)
	}
	out["results"] = results

	if page == 1 {
		facets, err := a.facets(r, q, cands)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		out["facets"] = facets
	}
	// Pocos resultados: sugerencia por título; ninguno: se registra para el admin (SPEC §9.1, §9.6).
	if page == 1 && len(cands) < 3 && len(q.Words) > 0 {
		if s, err := a.Read.SuggestTitle(r.Context(), scopeOf(r), q.Words); err == nil && s != "" && !slices.ContainsFunc(cands, func(h repo.SearchHit) bool { return h.Title == s }) {
			out["suggestion"] = s // solo si no está ya entre los resultados
		}
	}
	if page == 1 && len(cands) == 0 && len(q.Words) > 0 {
		if err := a.Read.RecordMiss(r.Context(), scopeOf(r), strings.Join(q.Words, " ")); err != nil {
			a.Log.Warn("record search miss", "err", err)
		}
	}
	a.json(w, http.StatusOK, out)
}

// facets cuenta los filtros de la página de resultados sobre los candidatos (SPEC §9.4).
func (a api) facets(r *http.Request, q repo.SearchQuery, cands []repo.SearchHit) (search.Facets, error) {
	ids := make([]string, len(cands))
	for i, c := range cands {
		ids[i] = c.ID
	}
	tags, authors, err := a.Read.SearchMeta(r.Context(), scopeOf(r), ids)
	if err != nil {
		return search.Facets{}, err
	}
	overdue, err := a.Read.OverduePages(r.Context(), scopeOf(r), ids)
	if err != nil {
		return search.Facets{}, err
	}
	list := make([]search.Candidate, len(cands))
	for i, c := range cands {
		list[i] = search.Candidate{Slug: c.Slug, UpdatedAt: c.UpdatedAt, Tags: tags[c.ID], Authors: authors[c.ID], Overdue: overdue[c.ID]}
	}
	return search.CountFacets(list, q.Path, time.Now()), nil
}

func (a api) searchMisses(w http.ResponseWriter, r *http.Request) {
	misses, err := a.Read.Misses(r.Context(), scopeOf(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]map[string]any, len(misses))
	for i, m := range misses {
		out[i] = map[string]any{"query": m.Query, "count": m.Count}
	}
	a.json(w, http.StatusOK, out)
}
