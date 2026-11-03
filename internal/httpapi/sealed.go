package httpapi

import (
	"bytes"
	"errors"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/athomo001/kudawiki/internal/sealed"
	"github.com/athomo001/kudawiki/internal/wiki"
	"github.com/go-chi/chi/v5"
)

// Documentos sellados (SPEC §6, §14). En el área: listar los que quien pide ve (para su árbol) y
// sellar uno nuevo. Fuera del área, por id: quien lo abre puede no ser miembro (el superadmin,
// y desde F7.2 las personas con acceso), así que la regla de quién lo ve está en repo, no en el
// Scope (ADR-0011). Nada de esto se guarda en la cache HTTP (§6).
func (a api) sealedAreaRoutes(r chi.Router) {
	r.Get("/sealed", a.sealedInArea)
	r.With(a.requireRank(repo.RoleRank("editor"))).Post("/sealed", a.createSealed)
}

func (a api) sealedRoutes(r chi.Router) {
	r.Use(noStore)
	r.Get("/{id}", a.sealedDoc)
	r.Post("/{id}/unlock", a.unlockSealed)
	r.Put("/{id}", a.saveSealed)
	r.Get("/{id}/history", a.sealedHistory)
	r.Delete("/{id}", a.deleteSealed)
	r.Get("/{id}/grants", a.sealedGrants)
	r.Post("/{id}/grants", a.grantSealed)
	r.Delete("/{id}/grants/{user_id}", a.revokeSealed)
	r.Post("/{id}/declassify", a.declassifySealed)
	r.Post("/{id}/password-reset", a.requestSealedReset)
	r.Post("/{id}/password-reset/approve", a.approveSealedReset)
	r.Post("/{id}/password-reset/complete", a.completeSealedReset)
	r.Post("/{id}/files", a.uploadSealedFile)
	r.Get("/{id}/files/{file_id}", a.sealedFile)
}

// vaultRoutes son de cada persona (§14.4): su bóveda y sus sellados de todas las áreas.
func (a api) vaultRoutes(r chi.Router) {
	r.With(noStore).Get("/me/sealed", a.mySealed)
	r.Post("/me/vault", a.setVault)
}

// noStore: ni el navegador ni un proxy guardan lo que sale de un sellado (§6, §17).
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func sessionOf(r *http.Request) string {
	c, _ := r.Cookie(sessionCookie) // requireSession ya comprobó que está
	return sealed.Session(c.Value)
}

func (a api) sealedInArea(w http.ResponseWriter, r *http.Request) {
	list, err := a.Sealed.InArea(r.Context(), scopeOf(r), currentUser(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]map[string]string, len(list))
	for i, s := range list {
		out[i] = map[string]string{"id": s.ID, "slug": s.Slug}
	}
	w.Header().Set("Cache-Control", "no-store")
	a.json(w, http.StatusOK, out)
}

func (a api) createSealed(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Slug     string `json:"slug"`
		Title    string `json:"title"`
		Markdown string `json:"markdown"`
		Password string `json:"password"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	id, err := a.Sealed.Create(r.Context(), scopeOf(r), currentUser(r).ID, sessionOf(r), strings.TrimSpace(in.Slug), in.Password,
		sealed.Content{Title: in.Title, Markdown: in.Markdown})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusCreated, map[string]string{"id": id})
}

// sealedDoc es el documento: cerrado, solo lo que hace falta para pedir que se abra; abierto,
// la revisión vigente descifrada y dibujada.
func (a api) sealedDoc(w http.ResponseWriter, r *http.Request) {
	u, id := currentUser(r), chi.URLParam(r, "id")
	d, open, err := a.Sealed.Doc(r.Context(), u, sessionOf(r), id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := map[string]any{"id": d.ID, "slug": d.Slug, "area": map[string]any{"slug": d.WsSlug, "name": d.WsName, "personal": d.Personal},
		"author": d.Author, "is_author": d.IsAuthor, "locked": !open, "needs_password": d.IsAuthor && !d.HasPassword,
		"can_admin_open": d.IsAreaAdmin || u.IsGlobalAdmin, "can_delete": d.IsAuthor || d.IsAreaAdmin || u.IsGlobalAdmin,
		"grant": d.Grant, "can_grant": d.IsAuthor || u.IsGlobalAdmin, "has_vault": u.HasVault,
		"can_declassify": u.IsGlobalAdmin, "has_password": d.HasPassword}
	// En qué va el restablecimiento de la contraseña (§6.3): solo para el autor y el superadmin.
	if d.IsAuthor || u.IsGlobalAdmin {
		out["reset"] = a.Sealed.ResetState(d.ResetRequested, d.ResetExpires)
	}
	if !open {
		a.json(w, http.StatusOK, out)
		return
	}
	rev, _ := strconv.Atoi(r.URL.Query().Get("revision"))
	v, err := a.Sealed.Read(r.Context(), u, sessionOf(r), id, rev)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	// Quien no es del área (el superadmin) no tiene a dónde ir con los [[enlaces]]: van como texto.
	opts := wiki.Options{LinksAsText: true}
	if d.IsAuthor || d.IsAreaAdmin {
		opts = wiki.Options{Base: "/w/" + d.WsSlug}
	}
	html, toc, err := wiki.Render(v.Content.Markdown, opts)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out["title"], out["markdown"], out["html"], out["toc"] = v.Content.Title, v.Content.Markdown, html, toc
	out["revision"], out["current_revision"], out["can_edit"] = v.Revision.Number, d.Revision, v.CanEdit
	out["updated_by"], out["updated_at"] = v.Revision.CreatedBy, v.Revision.CreatedAt
	a.json(w, http.StatusOK, out)
}

// unlockSealed abre el documento en esta sesión: con su contraseña, o como admin con la llave
// maestra, lo que pide la sesión reautenticada (§6.2, §8.2). Un sellado recibido en una baja no
// tiene contraseña: la primera vez su autor la pone, también con la sesión reautenticada (§8.4).
func (a api) unlockSealed(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
		VaultKey string `json:"vault_key"`
		AsAdmin  bool   `json:"as_admin"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	u, id, session := currentUser(r), chi.URLParam(r, "id"), sessionOf(r)
	// Primero, que exista para quien pide (si no, el 404 de siempre): a nadie se le pide
	// reautenticarse para algo que no puede hacer.
	d, _, err := a.Sealed.Doc(r.Context(), u, session, id)
	if err == nil && in.AsAdmin && !d.IsAreaAdmin && !u.IsGlobalAdmin {
		err = sealed.ErrInvalid
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	switch {
	case in.AsAdmin:
		if !a.reauthenticated(w, r) {
			return
		}
		err = a.Sealed.UnlockAsAdmin(r.Context(), u, session, id)
	case in.VaultKey != "":
		err = a.Sealed.UnlockVault(r.Context(), u, session, id, in.VaultKey)
	case d.IsAuthor && !d.HasPassword:
		if !a.reauthenticated(w, r) {
			return
		}
		err = a.Sealed.SetPassword(r.Context(), u, session, id, in.Password)
	default:
		err = a.Sealed.UnlockPassword(r.Context(), u, session, id, in.Password)
	}
	var limited sealed.RateLimitedError
	if errors.As(err, &limited) {
		w.Header().Set("Retry-After", strconv.Itoa(int(limited.RetryAfter.Seconds())+1))
		a.error(w, r, http.StatusTooManyRequests, "rate_limited", nil)
		return
	}
	a.noContent(w, r, err)
}

func (a api) saveSealed(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title        string `json:"title"`
		Markdown     string `json:"markdown"`
		BaseRevision int    `json:"base_revision"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	n, err := a.Sealed.Save(r.Context(), currentUser(r), sessionOf(r), chi.URLParam(r, "id"), in.BaseRevision,
		sealed.Content{Title: in.Title, Markdown: in.Markdown})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, map[string]int{"revision": n})
}

func (a api) sealedHistory(w http.ResponseWriter, r *http.Request) {
	list, err := a.Sealed.History(r.Context(), currentUser(r), sessionOf(r), chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]map[string]any, len(list))
	for i, rev := range list {
		out[i] = map[string]any{"revision": rev.Number, "author": rev.CreatedBy, "created_at": rev.CreatedAt}
	}
	a.json(w, http.StatusOK, out)
}

func (a api) deleteSealed(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Confirm string `json:"confirm"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	a.noContent(w, r, a.Sealed.Delete(r.Context(), currentUser(r), chi.URLParam(r, "id"), in.Confirm))
}

// sealedGrants son los accesos dados: solo para el autor y el superadmin (§19.1).
func (a api) sealedGrants(w http.ResponseWriter, r *http.Request) {
	list, err := a.Sealed.Grants(r.Context(), currentUser(r), chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]map[string]any, len(list))
	for i, g := range list {
		out[i] = map[string]any{"user_id": g.UserID, "full_name": g.Name, "email": g.Email, "permission": g.Permission, "created_at": g.CreatedAt}
	}
	a.json(w, http.StatusOK, out)
}

func (a api) grantSealed(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email      string `json:"email"`
		Permission string `json:"permission"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	a.noContent(w, r, a.Sealed.Grant(r.Context(), currentUser(r), sessionOf(r), chi.URLParam(r, "id"), in.Email, in.Permission))
}

func (a api) revokeSealed(w http.ResponseWriter, r *http.Request) {
	a.noContent(w, r, a.Sealed.Revoke(r.Context(), currentUser(r), chi.URLParam(r, "id"), chi.URLParam(r, "user_id")))
}

func (a api) mySealed(w http.ResponseWriter, r *http.Request) {
	list, err := a.Sealed.Mine(r.Context(), currentUser(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]map[string]any, len(list))
	for i, m := range list {
		out[i] = map[string]any{"id": m.ID, "slug": m.Slug, "area": map[string]any{"slug": m.WsSlug, "name": m.WsName, "personal": m.Personal},
			"author": m.Author, "access": m.Access, "updated_at": m.UpdatedAt}
	}
	a.json(w, http.StatusOK, out)
}

// setVault crea o reemplaza la clave de bóveda, con la sesión reautenticada: con una sesión
// robada se podría poner una propia antes que la persona, o borrarle lo compartido (§6.3, §8.2).
func (a api) setVault(w http.ResponseWriter, r *http.Request) {
	var in struct {
		VaultKey string `json:"vault_key"`
	}
	if !a.decode(w, r, &in) || !a.reauthenticated(w, r) {
		return
	}
	lost, err := a.Sealed.SetVault(r.Context(), currentUser(r), in.VaultKey)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, map[string]int64{"lost": lost})
}

// sealedFor es el sellado si quien pide lo ve (si no, el 404 de siempre) y allowed lo deja hacer
// la acción (si no, 422). Va antes de pedir la reautenticación: a nadie se le pide la contraseña
// para algo que igual no puede hacer.
func (a api) sealedFor(w http.ResponseWriter, r *http.Request, allowed func(repo.SealedDoc, repo.User) bool) bool {
	u := currentUser(r)
	d, _, err := a.Sealed.Doc(r.Context(), u, sessionOf(r), chi.URLParam(r, "id"))
	if err == nil && !allowed(d, u) {
		err = sealed.ErrInvalid
	}
	if err != nil {
		a.fail(w, r, err)
		return false
	}
	return true
}

func superadmin(_ repo.SealedDoc, u repo.User) bool { return u.IsGlobalAdmin }
func author(d repo.SealedDoc, _ repo.User) bool     { return d.IsAuthor }

// declassifySealed lo convierte en una página del área (§6.1): superadmin y sesión reautenticada.
// Responde a dónde quedó la página.
func (a api) declassifySealed(w http.ResponseWriter, r *http.Request) {
	if !a.sealedFor(w, r, superadmin) || !a.reauthenticated(w, r) {
		return
	}
	d, err := a.Sealed.Declassify(r.Context(), currentUser(r), chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, map[string]string{"area": d.WsSlug, "slug": d.Slug})
}

func (a api) requestSealedReset(w http.ResponseWriter, r *http.Request) {
	a.noContent(w, r, a.Sealed.RequestReset(r.Context(), currentUser(r), chi.URLParam(r, "id")))
}

// approveSealedReset es del superadmin, con la sesión reautenticada (§6.3, paso 2).
func (a api) approveSealedReset(w http.ResponseWriter, r *http.Request) {
	if !a.sealedFor(w, r, superadmin) || !a.reauthenticated(w, r) {
		return
	}
	a.noContent(w, r, a.Sealed.ApproveReset(r.Context(), currentUser(r), chi.URLParam(r, "id")))
}

// completeSealedReset es la contraseña nueva del autor. La sesión reautenticada prueba que es él:
// por eso no hay un token en el correo (§6.3, pasos 3 a 5).
func (a api) completeSealedReset(w http.ResponseWriter, r *http.Request) {
	var in struct {
		NewPassword string `json:"new_password"`
	}
	if !a.decode(w, r, &in) || !a.sealedFor(w, r, author) || !a.reauthenticated(w, r) {
		return
	}
	a.noContent(w, r, a.Sealed.CompleteReset(r.Context(), currentUser(r), sessionOf(r), chi.URLParam(r, "id"), in.NewPassword))
}

// allSealed es la lista del superadmin (adminRoutes exige el rol): no recorre las áreas, así que
// sin ella no llegaría a los pedidos de restablecimiento ni a lo que hay que desclasificar (§6.1).
func (a api) allSealed(w http.ResponseWriter, r *http.Request) {
	list, err := a.Sealed.All(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]map[string]any, len(list))
	for i, s := range list {
		out[i] = map[string]any{"id": s.ID, "slug": s.Slug, "area": map[string]any{"slug": s.WsSlug, "name": s.WsName, "personal": s.Personal},
			"author": s.Author, "updated_at": s.UpdatedAt, "reset": a.Sealed.ResetState(s.ResetRequested, s.ResetExpires)}
	}
	a.json(w, http.StatusOK, out)
}

// uploadSealedFile adjunta un archivo a un sellado abierto con permiso de edición (F7.6). Responde
// la dirección para el Markdown, que solo sirve con el documento abierto.
func (a api) uploadSealedFile(w http.ResponseWriter, r *http.Request) {
	mr, err := r.MultipartReader()
	if err != nil {
		a.error(w, r, http.StatusBadRequest, "invalid_request", nil)
		return
	}
	for {
		part, err := mr.NextPart()
		if err != nil { // io.EOF incluido: no vino el campo "file"
			a.error(w, r, http.StatusBadRequest, "invalid_request", nil)
			return
		}
		if part.FormName() != "file" {
			continue
		}
		id := chi.URLParam(r, "id")
		f, err := a.Sealed.AddFile(r.Context(), currentUser(r), sessionOf(r), id, part.FileName(), part)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		a.json(w, http.StatusCreated, map[string]string{"id": f.ID, "name": f.Name, "mime": f.Mime, "url": sealed.FileURL(id, f.ID)})
		return
	}
}

// sealedFile entrega un archivo descifrado, con las reglas de la media del área (las imágenes se
// ven, lo demás se descarga, nada se ejecuta) y sin cache: sealedRoutes pone no-store (§6).
func (a api) sealedFile(w http.ResponseWriter, r *http.Request) {
	f, data, err := a.Sealed.OpenFile(r.Context(), currentUser(r), sessionOf(r), chi.URLParam(r, "id"), chi.URLParam(r, "file_id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	disposition := "attachment"
	if strings.HasPrefix(f.Mime, "image/") {
		disposition = "inline"
	}
	h := w.Header()
	h.Set("Content-Type", f.Mime)
	h.Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": f.Name}))
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	http.ServeContent(w, r, "", f.CreatedAt, bytes.NewReader(data))
}
