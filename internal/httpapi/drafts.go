package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/athomo001/kudawiki/internal/presence"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/athomo001/kudawiki/internal/wiki"
	"github.com/go-chi/chi/v5"
)

// presenceBeat es cada cuánto el aviso de edición manda un latido y vuelve a mirar los permisos.
// Menos de un minuto: los proxies suelen cortar una conexión callada a los 60 s. Es variable
// para que los tests no esperen.
var presenceBeat = 25 * time.Second

// draftRoutes son el autoguardado y el aviso de edición concurrente (SPEC §4.11). Los usa quien
// puede escribir en el área (requireAuthor): editores y admins, y los lectores si el área les
// deja proponer. La ruta de la página va al final, como en /watch/{path...}.
func (a api) draftRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(a.requireAuthor)
		r.Get("/drafts", a.newPageDrafts)
		r.Get("/drafts/*", a.getDraft)
		r.Put("/drafts/*", a.saveDraft)
		r.Delete("/drafts/*", a.deleteDraft)
		r.Get("/presence/*", a.presence)
	})
}

func draftJSON(d repo.Draft) map[string]any {
	return map[string]any{"slug": d.Slug, "title": d.Title, "markdown": d.Markdown, "base_revision": d.Base,
		"new_page": d.NewPage, "updated_at": d.UpdatedAt}
}

// newPageDrafts son las páginas nuevas que quien pregunta dejó sin publicar, sin el contenido.
func (a api) newPageDrafts(w http.ResponseWriter, r *http.Request) {
	ds, err := a.Drafts.NewPageDrafts(r.Context(), scopeOf(r), currentUser(r).ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]map[string]any, len(ds))
	for i, d := range ds {
		out[i] = map[string]any{"slug": d.Slug, "title": d.Title, "updated_at": d.UpdatedAt}
	}
	a.json(w, http.StatusOK, out)
}

func (a api) getDraft(w http.ResponseWriter, r *http.Request) {
	d, err := a.Drafts.DraftBySlug(r.Context(), scopeOf(r), currentUser(r).ID, chi.URLParam(r, "*"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, draftJSON(d))
}

// saveDraft valida antes de buscar la página: la respuesta no cambia si en esa ruta hay una
// página que quien escribe no ve (§3.3).
func (a api) saveDraft(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title        string `json:"title"`
		Markdown     string `json:"markdown"`
		BaseRevision int    `json:"base_revision"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	slug := chi.URLParam(r, "*")
	if err := wiki.CheckDraft(slug, in.Title, in.Markdown, in.BaseRevision); err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.Drafts.SaveDraft(r.Context(), scopeOf(r), currentUser(r).ID, slug,
		repo.Draft{Title: in.Title, Markdown: in.Markdown, Base: in.BaseRevision}); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a api) deleteDraft(w http.ResponseWriter, r *http.Request) {
	if err := a.Drafts.DeleteDraft(r.Context(), scopeOf(r), currentUser(r).ID, chi.URLParam(r, "*")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// forgetDraft borra el borrador de quien acaba de publicar o proponer en slug (§4.11). Lo
// publicado ya quedó: si esto falla, el borrador caduca solo a los 30 días.
func (a api) forgetDraft(r *http.Request, slug string) {
	if err := a.Drafts.DeleteDraft(r.Context(), scopeOf(r), currentUser(r).ID, slug); err != nil {
		a.Log.Error("delete draft", "err", err)
	}
}

// presence es el aviso de edición concurrente (§4.11): un flujo SSE que manda la lista de
// quienes tienen la página abierta en el editor cada vez que cambia. Solo de páginas que
// existen y que quien pregunta ve; una nueva todavía no es de nadie más.
func (a api) presence(w http.ResponseWriter, r *http.Request) {
	p, ok := a.page(w, r)
	if !ok {
		return
	}
	u := currentUser(r)
	updates, leave, err := a.Presence.Join(p.ID, presence.Editor{ID: u.ID, Name: u.FullName})
	if errors.Is(err, presence.ErrTooMany) {
		a.error(w, r, http.StatusTooManyRequests, err.Error(), nil)
		return
	}
	defer leave()

	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no") // nginx no la junta en su búfer
	w.WriteHeader(http.StatusOK)
	beat := time.NewTicker(presenceBeat)
	defer beat.Stop()
	for {
		// El WriteTimeout del servidor (60 s) cortaría la conexión: cada vuelta corre el plazo.
		_ = rc.SetWriteDeadline(time.Now().Add(2 * presenceBeat))
		select {
		case <-r.Context().Done():
			return
		case <-a.Presence.Done():
			return
		case list := <-updates:
			data, _ := json.Marshal(map[string]any{"editors": list})
			fmt.Fprintf(w, "data: %s\n\n", data)
		case <-beat.C:
			if !a.stillEditing(r, p.ID) {
				return
			}
			fmt.Fprint(w, ": beat\n\n")
		}
		if err := rc.Flush(); err != nil {
			return
		}
	}
}

// stillEditing vuelve a mirar, en cada latido, lo que se miró al conectar: la sesión (sin
// contarla como actividad), la membresía, que pueda escribir y que la página siga a su alcance.
// A quien sacaron del área o le subieron el nivel a la página se le corta: no sigue viendo
// quién la edita (§3.3).
func (a api) stillEditing(r *http.Request, pageID string) bool {
	ctx := r.Context()
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	if u, err := a.Auth.Check(ctx, c.Value); err != nil || u.ID != currentUser(r).ID {
		return false
	}
	m, err := a.Read.MembershipBySlug(ctx, chi.URLParam(r, "ws"), currentUser(r).ID)
	if err != nil {
		return false
	}
	sc := repo.Scope{WsID: m.ID, MaxRank: repo.RoleRank(m.Role)}
	if ok, err := a.mayWrite(ctx, sc); err != nil || !ok {
		return false
	}
	p, err := a.Read.PageBySlug(ctx, sc, chi.URLParam(r, "*"))
	return err == nil && p.ID == pageID
}
