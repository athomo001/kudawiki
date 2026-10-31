package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/athomo001/kudawiki/internal/wiki"
	"github.com/go-chi/chi/v5"
)

// Rutas de páginas (SPEC §14.3). El slug va al final ({path...}) y las acciones van como
// prefijo (/history/…, /diff/…), para que una página llamada "history" no choque con la ruta.
func (a api) pageRoutes(r chi.Router) {
	r.Get("/tree", a.tree)
	r.Get("/pages/*", a.getPage)
	r.Get("/history/*", a.history)
	r.Get("/revisions/{n}/*", a.revision)
	r.Get("/diff/*", a.diff)
	r.Get("/media/{id}", a.getMedia)
	r.Get("/backlinks/*", a.backlinks)
	r.With(a.areaOnly, a.requireRank(repo.RoleRank("admin"))).Get("/access/*", a.access)
	r.Get("/tags", a.tags)
	r.Get("/tags/{tag}", a.taggedPages)
	// Seguir una página: lo puede hacer cualquiera que la lea (§12.1).
	r.Put("/watch/*", a.watch)
	r.Delete("/watch/*", a.unwatch)
	// Vigencia (§4.8): la verifica su dueño o un admin, aunque el dueño ya no pueda editar en el
	// área (para cualquier otro es el 404 uniforme). La cola: las propias, o todas para un admin.
	r.Post("/verify/*", a.verify)
	r.Get("/review-queue", a.reviewQueue)
	r.Get("/search", a.search)
	r.With(a.requireRank(repo.RoleRank("admin"))).Get("/search-misses", a.searchMisses)
	r.With(a.requireRank(repo.RoleRank("admin"))).Get("/audit", a.areaAudit)
	r.Group(func(r chi.Router) {
		r.Use(a.requireRank(repo.RoleRank("editor")))
		r.Put("/pages/*", a.savePage)
		r.Post("/revert/*", a.revert)
		r.Post("/media", a.uploadMedia)
		// Navegador de archivos y borrar (§4.4): de los editores, cada archivo con el nivel de su página.
		r.Get("/media", a.listMedia)
		r.Delete("/media/{id}", a.deleteMedia)
		r.Patch("/meta/*", a.setMeta)
		r.Post("/move/*", a.move)
		r.Delete("/pages/*", a.deletePage)
		// La papelera: el admin ve toda; un editor, lo que él borró (§4.5, §19.38).
		r.Get("/trash", a.trash)
		r.Post("/trash/{id}/restore", a.restore)
	})
}

// renderOptions arma el contexto del render desde el árbol del Scope: [[children]] lista solo
// las hijas visibles, y un enlace a una página que quien lee no ve sale roto (SPEC §4.10, §17).
func (a api) renderOptions(r *http.Request, slug, markdown string) wiki.Options {
	opts := wiki.Options{Base: "/w/" + chi.URLParam(r, "ws")}
	if !strings.Contains(markdown, "[[") {
		return opts
	}
	refs, err := a.Read.Tree(r.Context(), scopeOf(r))
	if err != nil {
		return opts // sin el árbol, los enlaces no se marcan: el resto de la página igual se lee
	}
	visible := make(map[string]bool, len(refs))
	opts.Exists = func(s string) bool { return visible[s] }
	for _, ref := range refs {
		visible[ref.Slug] = true
		if rest, ok := strings.CutPrefix(ref.Slug, slug+"/"); ok && slug != "" && !strings.Contains(rest, "/") {
			opts.Children = append(opts.Children, wiki.Child{Slug: ref.Slug, Title: ref.Title, Description: ref.Description, Icon: ref.Icon})
		}
	}
	return opts
}

// page busca la página del slug de la ruta dentro del Scope; si no está, 404 uniforme.
func (a api) page(w http.ResponseWriter, r *http.Request) (repo.Page, bool) {
	p, err := a.Read.PageBySlug(r.Context(), scopeOf(r), chi.URLParam(r, "*"))
	if err != nil {
		a.fail(w, r, err)
		return repo.Page{}, false
	}
	return p, true
}

func (a api) tree(w http.ResponseWriter, r *http.Request) {
	refs, err := a.Read.Tree(r.Context(), scopeOf(r))
	a.refs(w, r, refs, err)
}

func (a api) getPage(w http.ResponseWriter, r *http.Request) {
	// Por el servicio y no directo a repo: leer una página N3 queda en la auditoría (§11.1).
	p, err := a.Wiki.Get(r.Context(), scopeOf(r), chi.URLParam(r, "*"))
	if errors.Is(err, repo.ErrNotFound) {
		// Ruta vieja de una página movida: redirige solo si quien pregunta ve la página nueva.
		if slug, err := a.Read.PageByAlias(r.Context(), scopeOf(r), chi.URLParam(r, "*")); err == nil {
			http.Redirect(w, r, "/api/w/"+chi.URLParam(r, "ws")+"/pages/"+slug, http.StatusPermanentRedirect)
			return
		}
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	tags, err := a.Read.PageTags(r.Context(), scopeOf(r), p.ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	html, toc, err := wiki.Render(p.Markdown, a.renderOptions(r, p.Slug, p.Markdown))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	watch, err := a.Notify.WatchOf(r.Context(), scopeOf(r), currentUser(r).ID, p)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	sent, err := a.sentJSON(r, p)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	reads, err := a.Reading.Opened(r.Context(), scopeOf(r), currentUser(r).ID, p)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	// Abrirla marca como leídos sus avisos de cambios (§12.1). Si falla, la página se lee igual.
	if err := a.Notify.PageOpened(r.Context(), currentUser(r).ID, p.ID); err != nil {
		a.Log.Error("mark notifications read", "err", err)
	}
	out := map[string]any{
		"slug": p.Slug, "title": p.Title, "markdown": p.Markdown, "html": html, "toc": toc, "tags": tags, "lang": p.Lang, "level": p.MinRank,
		"revision": p.Revision, "updated_by": p.UpdatedBy, "updated_at": p.UpdatedAt, "description": p.Description, "icon": p.Icon,
		"watch": watch.Mode, "watch_via": watch.Via, "review": reviewJSON(r, p), "reading": myReadsJSON(reads),
	}
	if sent != nil {
		out["sent"] = sent
	}
	a.json(w, http.StatusOK, out)
}

// reviewJSON es la vigencia de la página (§4.8): su dueño, el intervalo (el suyo y el del área),
// la última verificación y, si vence, cuándo. can_review: quien pregunta es el dueño o un admin.
func reviewJSON(r *http.Request, p repo.Page) map[string]any {
	out := map[string]any{"owner_id": p.OwnerID, "owner": p.Owner, "interval_days": p.ReviewDays, "area_interval_days": p.AreaReviewDays,
		"verified_at": p.VerifiedAt, "verified_by": p.VerifiedBy, "due_at": nil, "overdue": false,
		"can_review": p.OwnerID == currentUser(r).ID || scopeOf(r).MaxRank >= 3}
	if due, ok := p.ReviewDue(); ok {
		out["due_at"], out["overdue"] = due, !time.Now().Before(due)
	}
	return out
}

func (a api) verify(w http.ResponseWriter, r *http.Request) {
	if err := a.Wiki.Verify(r.Context(), scopeOf(r), currentUser(r).ID, chi.URLParam(r, "*")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// reviewQueue son las páginas vencidas: todas las del área para su admin, las propias para los
// demás ("Mis páginas por revisar", §4.8).
func (a api) reviewQueue(w http.ResponseWriter, r *http.Request) {
	owner := currentUser(r).ID
	if scopeOf(r).MaxRank >= 3 {
		owner = ""
	}
	items, err := a.Read.ReviewQueue(r.Context(), scopeOf(r), owner, time.Now())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]map[string]any, len(items))
	for i, it := range items {
		out[i] = map[string]any{"slug": it.Slug, "title": it.Title, "level": it.MinRank, "owner": it.Owner,
			"verified_at": it.VerifiedAt, "due_at": it.DueAt}
	}
	a.json(w, http.StatusOK, out)
}

func (a api) backlinks(w http.ResponseWriter, r *http.Request) {
	p, ok := a.page(w, r)
	if !ok {
		return
	}
	refs, err := a.Read.Backlinks(r.Context(), scopeOf(r), p.ID)
	a.refs(w, r, refs, err)
}

func (a api) tags(w http.ResponseWriter, r *http.Request) {
	tags, err := a.Read.Tags(r.Context(), scopeOf(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]map[string]any, len(tags))
	for i, t := range tags {
		out[i] = map[string]any{"tag": t.Tag, "count": t.Count}
	}
	a.json(w, http.StatusOK, out)
}

func (a api) taggedPages(w http.ResponseWriter, r *http.Request) {
	refs, err := a.Read.PagesByTag(r.Context(), scopeOf(r), chi.URLParam(r, "tag"))
	a.refs(w, r, refs, err)
}

// refs responde una lista de páginas {slug, title}.
func (a api) refs(w http.ResponseWriter, r *http.Request, refs []repo.PageRef, err error) {
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]map[string]any, len(refs))
	for i, p := range refs {
		out[i] = map[string]any{"slug": p.Slug, "title": p.Title, "level": p.MinRank, "description": p.Description, "icon": p.Icon}
	}
	a.json(w, http.StatusOK, out)
}

// setMeta cambia los metadatos que vienen en el cuerpo: etiquetas, nivel, descripción, ícono,
// dueño e intervalo de revisión (§4.6, §3.2, §4.1, §4.8). El intervalo 0 es "el del área".
func (a api) setMeta(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Tags        *[]string `json:"tags"`
		Level       *int      `json:"level"`
		Description *string   `json:"description"`
		Icon        *string   `json:"icon"`
		OwnerID     *string   `json:"owner_id"`
		ReviewDays  *int      `json:"review_interval_days"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	slug := chi.URLParam(r, "*")
	if in.Level != nil {
		if err := a.Wiki.SetLevel(r.Context(), scopeOf(r), currentUser(r).ID, slug, *in.Level); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	if in.Tags != nil {
		if err := a.Wiki.SetTags(r.Context(), scopeOf(r), slug, *in.Tags); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	if in.Description != nil || in.Icon != nil {
		if err := a.Wiki.SetInfo(r.Context(), scopeOf(r), slug, in.Description, in.Icon); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	if in.OwnerID != nil || in.ReviewDays != nil {
		if err := a.Wiki.SetReview(r.Context(), scopeOf(r), currentUser(r).ID, slug, in.OwnerID, in.ReviewDays); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// access es "¿quién puede ver esto?" (§3.4): cada miembro con su rol y si lee la página, con
// la misma regla que autoriza (repo.CanRead). ?simulate=N dice quién la leería con otro nivel.
func (a api) access(w http.ResponseWriter, r *http.Request) {
	p, ok := a.page(w, r)
	if !ok {
		return
	}
	simulate, _ := strconv.Atoi(r.URL.Query().Get("simulate"))
	members, err := a.Read.Members(r.Context(), scopeOf(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	people := make([]map[string]any, len(members))
	for i, m := range members {
		entry := map[string]any{"name": m.FullName, "email": m.Email, "role": m.Role, "can_read": repo.CanRead(m.Role, p.MinRank)}
		if simulate >= 1 && simulate <= 3 {
			entry["can_read_simulated"] = repo.CanRead(m.Role, simulate)
		}
		people[i] = entry
	}
	a.json(w, http.StatusOK, map[string]any{"level": p.MinRank, "members": people})
}

func (a api) move(w http.ResponseWriter, r *http.Request) {
	var in struct {
		To string `json:"to"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	if err := a.Wiki.Move(r.Context(), scopeOf(r), currentUser(r).ID, chi.URLParam(r, "*"), strings.TrimSpace(in.To)); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a api) deletePage(w http.ResponseWriter, r *http.Request) {
	if err := a.Wiki.Delete(r.Context(), scopeOf(r), currentUser(r).ID, chi.URLParam(r, "*")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a api) trash(w http.ResponseWriter, r *http.Request) {
	items, err := a.Read.Trash(r.Context(), scopeOf(r), currentUser(r).ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]map[string]any, len(items))
	for i, t := range items {
		out[i] = map[string]any{"id": t.ID, "slug": t.Slug, "title": t.Title, "level": t.MinRank, "pages": t.Pages,
			"deleted_by": t.DeletedBy, "deleted_at": t.DeletedAt, "purge_at": t.DeletedAt.Add(a.TrashRetention)}
	}
	a.json(w, http.StatusOK, out)
}

func (a api) restore(w http.ResponseWriter, r *http.Request) {
	if err := a.Wiki.Restore(r.Context(), scopeOf(r), currentUser(r).ID, chi.URLParam(r, "id")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a api) savePage(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title         string `json:"title"`
		Markdown      string `json:"markdown"`
		Lang          string `json:"lang"`
		BaseRevision  int    `json:"base_revision"` // 0 = crear
		ChangeSummary string `json:"change_summary"`
		ReviewDays    int    `json:"review_interval_days"` // solo al crear: el de la plantilla (§4.8)
	}
	if !a.decode(w, r, &in) {
		return
	}
	err := a.Wiki.Save(r.Context(), scopeOf(r), currentUser(r).ID, in.BaseRevision, repo.PageWrite{
		Slug: chi.URLParam(r, "*"), Title: in.Title, Markdown: in.Markdown, Lang: in.Lang, Summary: in.ChangeSummary, ReviewDays: in.ReviewDays,
	})
	if errors.Is(err, wiki.ErrConflict) {
		a.conflict(w, r, in.Markdown)
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.forgetDraft(r, chi.URLParam(r, "*"))
	w.WriteHeader(http.StatusNoContent)
}

// conflict es el 409 de SPEC §4.1: trae la revisión guardada, quién la guardó y el diff de esa
// versión a la que se quiso guardar, para resolver sin perder lo escrito. La página se lee por
// el servicio y con el mismo Scope: si no se puede leer no hay detalle, y leer una N3 queda en la
// auditoría igual que al abrirla (§11.1).
func (a api) conflict(w http.ResponseWriter, r *http.Request, mine string) {
	body := map[string]any{"code": wiki.ErrConflict.Error(), "message": http.StatusText(http.StatusConflict)}
	if p, err := a.Wiki.Get(r.Context(), scopeOf(r), chi.URLParam(r, "*")); err == nil {
		body["current"] = map[string]any{"revision": p.Revision, "updated_by": p.UpdatedBy, "updated_at": p.UpdatedAt, "diff": wiki.Diff(p.Markdown, mine)}
	}
	a.json(w, http.StatusConflict, map[string]any{"error": body})
}

func (a api) history(w http.ResponseWriter, r *http.Request) {
	p, ok := a.page(w, r)
	if !ok {
		return
	}
	revs, err := a.Read.Revisions(r.Context(), scopeOf(r), p.ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]map[string]any, len(revs))
	for i, v := range revs {
		out[i] = map[string]any{"revision": v.Number, "kind": v.Kind, "title": v.Title,
			"author": v.Author, "approved_by": v.Approver, "summary": v.Summary, "created_at": v.CreatedAt}
	}
	a.json(w, http.StatusOK, out)
}

// revisionAt lee la revisión n de la página de la ruta.
func (a api) revisionAt(w http.ResponseWriter, r *http.Request, p repo.Page, raw string) (repo.Revision, bool) {
	n, err := strconv.Atoi(raw)
	if err != nil {
		a.notFound(w, r)
		return repo.Revision{}, false
	}
	v, err := a.Read.RevisionByNumber(r.Context(), scopeOf(r), p.ID, n)
	if err != nil {
		a.fail(w, r, err)
		return repo.Revision{}, false
	}
	return v, true
}

func (a api) revision(w http.ResponseWriter, r *http.Request) {
	p, ok := a.page(w, r)
	if !ok {
		return
	}
	v, ok := a.revisionAt(w, r, p, chi.URLParam(r, "n"))
	if !ok {
		return
	}
	html, _, err := wiki.Render(v.Markdown, a.renderOptions(r, p.Slug, v.Markdown))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, map[string]any{"revision": v.Number, "title": v.Title, "markdown": v.Markdown, "html": html})
}

func (a api) diff(w http.ResponseWriter, r *http.Request) {
	p, ok := a.page(w, r)
	if !ok {
		return
	}
	from, ok := a.revisionAt(w, r, p, r.URL.Query().Get("from"))
	if !ok {
		return
	}
	to, ok := a.revisionAt(w, r, p, r.URL.Query().Get("to"))
	if !ok {
		return
	}
	a.json(w, http.StatusOK, wiki.Diff(from.Markdown, to.Markdown))
}

func (a api) revert(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision     int `json:"revision"`
		BaseRevision int `json:"base_revision"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	if err := a.Wiki.Revert(r.Context(), scopeOf(r), currentUser(r).ID, chi.URLParam(r, "*"), in.Revision, in.BaseRevision); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// preview usa el mismo render que el lector: la vista previa y la página final son idénticas (SPEC §4.2),
// salvo las marcas data-line, que no se ven.
func (a api) preview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Markdown string `json:"markdown"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	opts := a.renderOptions(r, "", in.Markdown)
	opts.SourceLines = true // para desplazar juntas las dos columnas del editor (§4.2)
	html, toc, err := wiki.Render(in.Markdown, opts)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, map[string]any{"html": html, "toc": toc})
}
