package httpapi

import (
	"net/http"
	"time"

	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/athomo001/kudawiki/internal/wiki"
	"github.com/go-chi/chi/v5"
)

// Inicio del área (SPEC §4.17): anuncios, actividad y más vistas. La portada es la página
// "portada" del área y se lee con /pages/portada, como cualquier otra.

const (
	announcementsShown = 10 // los últimos; los anteriores siguen en la base hasta que se borran
	statsListed        = 5
)

func (a api) areaHomeRoutes(r chi.Router) {
	r.Get("/announcements", a.listAnnouncements)
	r.Get("/stats", a.areaStats)
	r.Post("/views/*", a.recordView)
	r.Group(func(r chi.Router) {
		r.Use(a.areaOnly, a.requireRank(repo.RoleRank("editor")))
		r.Post("/announcements", a.createAnnouncement)
		r.Delete("/announcements/{id}", a.deleteAnnouncement)
	})
}

// listAnnouncements rinde cada anuncio para quien lo lee: un [[enlace]] a una página que no ve
// sale roto, igual que en una página (§17).
func (a api) listAnnouncements(w http.ResponseWriter, r *http.Request) {
	list, err := a.Read.Announcements(r.Context(), scopeOf(r), announcementsShown)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	me := currentUser(r).ID
	out := make([]map[string]any, len(list))
	for i, an := range list {
		html, _, err := wiki.Render(an.Markdown, a.renderOptions(r, "", an.Markdown))
		if err != nil {
			a.fail(w, r, err)
			return
		}
		out[i] = map[string]any{"id": an.ID, "title": an.Title, "html": html, "author": an.Author, "created_at": an.CreatedAt,
			"can_delete": wiki.CanDeleteAnnouncement(scopeOf(r), me, an.AuthorID)}
	}
	a.json(w, http.StatusOK, out)
}

func (a api) createAnnouncement(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	id, err := a.Wiki.Announce(r.Context(), scopeOf(r), currentUser(r).ID, in.Title, in.Body)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusCreated, map[string]string{"id": id})
}

func (a api) deleteAnnouncement(w http.ResponseWriter, r *http.Request) {
	if err := a.Wiki.DeleteAnnouncement(r.Context(), scopeOf(r), currentUser(r).ID, chi.URLParam(r, "id")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// areaStats es la actividad del área con el nivel de quien mira (§4.17).
func (a api) areaStats(w http.ResponseWriter, r *http.Request) {
	st, err := a.Read.AreaStats(r.Context(), scopeOf(r), statsListed)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	type item struct {
		Slug      string     `json:"slug"`
		Title     string     `json:"title"`
		Icon      string     `json:"icon"`
		UpdatedAt *time.Time `json:"updated_at,omitempty"`
		Views     int        `json:"views,omitempty"`
	}
	recent := make([]item, len(st.Recent))
	for i, p := range st.Recent {
		recent[i] = item{Slug: p.Slug, Title: p.Title, Icon: p.Icon, UpdatedAt: &p.UpdatedAt}
	}
	top := make([]item, len(st.TopViewed))
	for i, p := range st.TopViewed {
		top[i] = item{Slug: p.Slug, Title: p.Title, Icon: p.Icon, Views: p.Views}
	}
	a.json(w, http.StatusOK, map[string]any{"pages": st.Pages, "changes_30d": st.Changes, "new_30d": st.New, "recent": recent, "top_viewed": top})
}

// recordView la llama el lector al abrir una página; el editor no. Cuenta visitas, no personas.
func (a api) recordView(w http.ResponseWriter, r *http.Request) {
	if err := a.Read.RecordView(r.Context(), scopeOf(r), chi.URLParam(r, "*")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
