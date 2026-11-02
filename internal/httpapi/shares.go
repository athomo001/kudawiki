package httpapi

import (
	"net/http"
	"strings"

	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/athomo001/kudawiki/internal/wiki"
	"github.com/go-chi/chi/v5"
)

// Artículos compartidos entre áreas (SPEC §4.19). Las rutas del origen (/shares) piden y
// resuelven; las del destino (/shared) leen lo que otras áreas le compartieron. Ninguna es de un
// espacio personal: no comparte ni recibe.
func (a api) shareRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(a.areaOnly)
		r.Get("/shared", a.sharedList)
		r.Get("/shared/{id}", a.sharedArticle)
		r.Get("/shared/{id}/media/{media}", a.sharedMedia)
		r.With(a.requireRank(repo.RoleRank("admin"))).Delete("/shared/{id}", a.removeShared)
		r.Group(func(r chi.Router) {
			r.Use(a.requireRank(repo.RoleRank("editor")))
			r.Get("/share-targets", a.shareTargets)
			r.Post("/shares", a.requestShare)
		})
		r.Group(func(r chi.Router) {
			r.Use(a.requireRank(repo.RoleRank("admin")))
			r.Get("/shares", a.listShares)
			r.Post("/shares/{id}/approve", a.approveShare)
			r.Post("/shares/{id}/reject", a.rejectShare)
			r.Delete("/shares/{id}", a.unshare)
		})
	})
}

func (a api) shareTargets(w http.ResponseWriter, r *http.Request) {
	list, err := a.Shares.Targets(r.Context(), scopeOf(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]map[string]string, len(list))
	for i, t := range list {
		out[i] = map[string]string{"id": t.ID, "name": t.Name}
	}
	a.json(w, http.StatusOK, out)
}

func (a api) requestShare(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Page    string   `json:"page"`
		Targets []string `json:"targets"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	approved, err := a.Shares.Request(r.Context(), scopeOf(r), currentUser(r).ID, strings.TrimSpace(in.Page), in.Targets)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, map[string]bool{"approved": approved})
}

func (a api) listShares(w http.ResponseWriter, r *http.Request) {
	list, err := a.Shares.List(r.Context(), scopeOf(r), "")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]map[string]any, len(list))
	for i, s := range list {
		var reviewed any // null mientras está pendiente
		if !s.ReviewedAt.IsZero() {
			reviewed = s.ReviewedAt
		}
		out[i] = map[string]any{"id": s.ID, "page": map[string]string{"slug": s.PageSlug, "title": s.PageTitle},
			"target": map[string]string{"id": s.TargetID, "name": s.TargetName}, "status": s.Status,
			"requested_by": s.RequestedBy, "reviewer": s.Reviewer, "created_at": s.CreatedAt, "reviewed_at": reviewed}
	}
	a.json(w, http.StatusOK, out)
}

func (a api) approveShare(w http.ResponseWriter, r *http.Request) {
	a.noContent(w, r, a.Shares.Approve(r.Context(), scopeOf(r), currentUser(r).ID, chi.URLParam(r, "id")))
}

func (a api) rejectShare(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Comment string `json:"comment"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	a.noContent(w, r, a.Shares.Reject(r.Context(), scopeOf(r), currentUser(r).ID, chi.URLParam(r, "id"), in.Comment))
}

func (a api) unshare(w http.ResponseWriter, r *http.Request) {
	a.noContent(w, r, a.Shares.Unshare(r.Context(), scopeOf(r), currentUser(r).ID, chi.URLParam(r, "id")))
}

// sharedList es el *Compartido* del área: lo que otras áreas le comparten, por área de origen.
func (a api) sharedList(w http.ResponseWriter, r *http.Request) {
	list, err := a.Shares.Shared(r.Context(), scopeOf(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]map[string]any, len(list))
	for i, s := range list {
		out[i] = map[string]any{"id": s.ID, "title": s.Title, "description": s.Description, "icon": s.Icon,
			"area": map[string]string{"name": s.SourceName}, "updated_at": s.UpdatedAt}
	}
	a.json(w, http.StatusOK, out)
}

// sharedArticle es un artículo compartido, en solo lectura. Sale sin la ruta ni el Markdown de la
// página del origen: el destino no tiene por qué saber dónde está ni llegar a lo que la rodea.
func (a api) sharedArticle(w http.ResponseWriter, r *http.Request) {
	s, err := a.Shares.Article(r.Context(), scopeOf(r), chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	// Las imágenes se piden por el vínculo, nunca al área de origen (§4.19).
	md := strings.ReplaceAll(s.Markdown, "/api/w/"+s.SourceSlug+"/media/", "/api/w/"+chi.URLParam(r, "ws")+"/shared/"+s.ID+"/media/")
	html, toc, err := wiki.Render(md, wiki.Options{LinksAsText: true})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, map[string]any{"id": s.ID, "title": s.Title, "description": s.Description, "icon": s.Icon,
		"area": map[string]string{"name": s.SourceName}, "html": html, "toc": toc, "lang": s.Lang,
		"updated_at": s.UpdatedAt, "updated_by": s.UpdatedBy, "owner": s.Owner})
}

func (a api) sharedMedia(w http.ResponseWriter, r *http.Request) {
	m, f, err := a.Media.OpenShared(r.Context(), scopeOf(r), chi.URLParam(r, "id"), chi.URLParam(r, "media"), r.URL.Query().Get("thumb") == "1")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	defer f.Close()
	serveMedia(w, r, m, f)
}

func (a api) removeShared(w http.ResponseWriter, r *http.Request) {
	a.noContent(w, r, a.Shares.Remove(r.Context(), scopeOf(r), currentUser(r).ID, chi.URLParam(r, "id")))
}
