package httpapi

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// watchingShown es cuántas páginas seguidas lista el inicio personal: las últimas cambiadas.
const watchingShown = 50

// homeRoutes es el inicio personal (§15, B5): lo de todas las áreas de quien pregunta. Las
// propuestas las pide la interfaz a cada área, con las reglas de su bandeja (§5.2).
func (a api) homeRoutes(r chi.Router) {
	r.Get("/me/watching", a.watching)
	r.Get("/me/review-queue", a.ownReviews)
	r.Get("/me/read-requests", a.myReadRequests)
	r.Get("/me/quizzes", a.myQuizzes)
}

func (a api) watching(w http.ResponseWriter, r *http.Request) {
	list, err := a.Read.WatchedPages(r.Context(), currentUser(r).ID, watchingShown)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]map[string]any, len(list))
	for i, p := range list {
		out[i] = map[string]any{"area": map[string]string{"slug": p.AreaSlug, "name": p.AreaName}, "slug": p.Slug, "title": p.Title,
			"updated_by": p.UpdatedBy, "updated_at": p.UpdatedAt, "subtree": p.Subtree}
	}
	a.json(w, http.StatusOK, out)
}

func (a api) ownReviews(w http.ResponseWriter, r *http.Request) {
	list, err := a.Read.OwnReviews(r.Context(), currentUser(r).ID, time.Now())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]map[string]any, len(list))
	for i, p := range list {
		out[i] = map[string]any{"area": map[string]string{"slug": p.AreaSlug, "name": p.AreaName}, "slug": p.Slug, "title": p.Title,
			"level": p.MinRank, "verified_at": p.VerifiedAt, "due_at": p.DueAt}
	}
	a.json(w, http.StatusOK, out)
}
