package httpapi

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/athomo001/kudawiki/internal/repo"
)

// Plantillas (SPEC §4.9). Las usa quien crea páginas (editor+); las administra el admin del área.
// Las globales son del superadmin, con los mismos handlers sobre repo.GlobalScope.
func (a api) templateRoutes(r chi.Router) {
	r.With(a.requireRank(repo.RoleRank("editor"))).Get("/templates", a.listTemplates)
	r.With(a.requireRank(repo.RoleRank("editor"))).Get("/templates/{id}/use", a.useTemplate)
	r.Group(func(r chi.Router) {
		r.Use(a.requireRank(repo.RoleRank("admin")))
		a.templateAdminRoutes(r)
	})
}

func (a api) templateAdminRoutes(r chi.Router) {
	r.Post("/templates", a.saveTemplate)
	r.Get("/templates/{id}", a.getTemplate)
	r.Put("/templates/{id}", a.saveTemplate)
	r.Delete("/templates/{id}", a.deleteTemplate)
}

// globalTemplates: el panel del superadmin trabaja sobre las plantillas sin área.
func (a api) globalTemplates(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, withScope(r, repo.GlobalScope))
	})
}

func templateJSON(t repo.Template) map[string]any {
	tags := t.Tags
	if tags == nil {
		tags = []string{}
	}
	return map[string]any{"id": t.ID, "name": t.Name, "description": t.Description, "title": t.Title,
		"markdown": t.Markdown, "level": t.MinRank, "tags": tags, "review_interval_days": t.ReviewDays, "updated_at": t.UpdatedAt}
}

// listTemplates no manda el contenido: para elegir bastan el nombre y la descripción.
func (a api) listTemplates(w http.ResponseWriter, r *http.Request) {
	list, err := a.Read.Templates(r.Context(), scopeOf(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]map[string]any, len(list))
	for i, t := range list {
		out[i] = templateJSON(t)
		delete(out[i], "markdown")
	}
	a.json(w, http.StatusOK, out)
}

func (a api) getTemplate(w http.ResponseWriter, r *http.Request) {
	t, err := a.Read.TemplateByID(r.Context(), scopeOf(r), chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, templateJSON(t))
}

func (a api) saveTemplate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Title       string   `json:"title"`
		Markdown    string   `json:"markdown"`
		Level       int      `json:"level"`
		Tags        []string `json:"tags"`
		ReviewDays  int      `json:"review_interval_days"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	t := repo.Template{ID: chi.URLParam(r, "id"), Name: in.Name, Description: in.Description, Title: in.Title,
		Markdown: in.Markdown, MinRank: in.Level, Tags: in.Tags, ReviewDays: in.ReviewDays}
	id, err := a.Wiki.SaveTemplate(r.Context(), scopeOf(r), currentUser(r).ID, t)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if t.ID == "" {
		a.json(w, http.StatusCreated, map[string]any{"id": id})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a api) deleteTemplate(w http.ResponseWriter, r *http.Request) {
	if err := a.Wiki.DeleteTemplate(r.Context(), scopeOf(r), chi.URLParam(r, "id")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// useTemplate llena la plantilla para el editor de una página nueva. today es la fecha de quien
// la usa: el servidor puede estar en otra zona horaria y {{fecha}} saldría con el día siguiente.
func (a api) useTemplate(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	if d, err := time.Parse(time.DateOnly, r.URL.Query().Get("today")); err == nil {
		now = d
	}
	u := currentUser(r)
	t, err := a.Wiki.UseTemplate(r.Context(), scopeOf(r), chi.URLParam(r, "id"), r.URL.Query().Get("title"), u.FullName, now)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := templateJSON(t)
	delete(out, "updated_at")
	a.json(w, http.StatusOK, out)
}
