package httpapi

import (
	"errors"
	"net/http"

	"github.com/athomo001/kudawiki/internal/personal"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/go-chi/chi/v5"
)

// personalRoutes es lo propio del espacio personal (§7): publicar una página en un área.
func (a api) personalRoutes(r chi.Router) {
	r.With(a.personalOnly).Post("/publish/*", a.publish)
}

// personalOnly: la ruta existe solo en el espacio personal de quien pregunta.
func (a api) personalOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, ok := personalOf(r); !ok || p.auditing {
			a.notFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// publish lleva una página del espacio personal a un área donde quien publica es editor o admin.
// Un área donde no lo es da el mismo 404 que una que no existe (§3.3).
func (a api) publish(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Area string `json:"area"`
		To   string `json:"to"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	m, err := a.Read.MembershipBySlug(r.Context(), in.Area, currentUser(r).ID)
	if err == nil && m.Kind != "area" {
		err = repo.ErrNotFound
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	from := personal.Space{Scope: scopeOf(r), Slug: chi.URLParam(r, "ws")}
	area := personal.Space{Scope: repo.Scope{WsID: m.ID, MaxRank: repo.RoleRank(m.Role)}, Slug: m.Slug}
	res, err := a.Personal.Publish(r.Context(), currentUser(r).ID, from, area, chi.URLParam(r, "*"), in.To)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if res.Proposal != "" {
		a.json(w, http.StatusAccepted, map[string]string{"proposal": res.Proposal})
		return
	}
	a.json(w, http.StatusOK, map[string]string{"slug": in.To})
}

// sentJSON es en qué quedó lo que se mandó desde una página del espacio personal (§7): enviado
// (pendiente), publicado, devuelto o retirado.
func (a api) sentJSON(r *http.Request, p repo.Page) ([]map[string]any, error) {
	if _, ok := personalOf(r); !ok {
		return nil, nil
	}
	list, err := a.Read.SentProposals(r.Context(), p.OwnerID, p.ID)
	if err != nil && !errors.Is(err, repo.ErrNotFound) {
		return nil, err
	}
	out := make([]map[string]any, len(list))
	for i, s := range list {
		out[i] = map[string]any{"id": s.ID, "area": map[string]string{"slug": s.AreaSlug, "name": s.AreaName}, "slug": s.Slug,
			"status": s.Status, "comment": s.Comment, "created_at": s.CreatedAt}
	}
	return out, nil
}
