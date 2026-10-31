package httpapi

import (
	"errors"
	"mime"
	"net/http"
	"strings"

	"github.com/athomo001/kudawiki/internal/media"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/go-chi/chi/v5"
)

func (a api) getMedia(w http.ResponseWriter, r *http.Request) {
	m, f, err := a.Media.Open(r.Context(), scopeOf(r), chi.URLParam(r, "id"), r.URL.Query().Get("thumb") == "1")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	defer f.Close()
	disposition := "attachment" // todo lo que no es imagen se descarga, no se abre en la wiki (SPEC §4.4)
	if strings.HasPrefix(m.MimeType, "image/") {
		disposition = "inline"
	}
	h := w.Header()
	h.Set("Content-Type", m.MimeType)
	h.Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": m.Filename}))
	h.Set("Cache-Control", "private, max-age=3600")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox") // abierto directo, no ejecuta nada
	http.ServeContent(w, r, "", m.CreatedAt, f)
}

// uploadMedia lee el multipart en streaming: el límite de tamaño lo aplica media.Service
// mientras escribe, sin cargar el archivo en memoria.
func (a api) uploadMedia(w http.ResponseWriter, r *http.Request) {
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
		m, err := a.Media.Upload(r.Context(), scopeOf(r), currentUser(r).ID, part.FileName(), part)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		a.json(w, http.StatusCreated, map[string]string{
			"id": m.ID, "name": m.Filename, "mime": m.MimeType,
			"url": "/api/w/" + chi.URLParam(r, "ws") + "/media/" + m.ID,
		})
		return
	}
}

func (a api) mediaURL(r *http.Request, id string) string {
	return "/api/w/" + chi.URLParam(r, "ws") + "/media/" + id
}

// listMedia es el navegador de archivos (§4.4). La página de cada archivo es la que lo tiene; si
// quien pregunta lo ve, también la ve a ella (MediaList aplica el nivel de esa página).
func (a api) listMedia(w http.ResponseWriter, r *http.Request) {
	items, err := a.Media.List(r.Context(), scopeOf(r), r.URL.Query().Get("q"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]map[string]any, len(items))
	for i, m := range items {
		var page any // null: todavía no la usa ninguna página
		if m.PageSlug != "" {
			page = map[string]string{"slug": m.PageSlug, "title": m.PageTitle}
		}
		out[i] = map[string]any{"id": m.ID, "name": m.Filename, "mime": m.MimeType, "size": m.Size, "thumb": m.HasThumbnail,
			"uploaded_by": m.Uploader, "created_at": m.CreatedAt, "url": a.mediaURL(r, m.ID), "page": page}
	}
	a.json(w, http.StatusOK, out)
}

// deleteMedia: si una página que quien borra ve todavía usa el archivo, 409 con esas páginas.
func (a api) deleteMedia(w http.ResponseWriter, r *http.Request) {
	pages, err := a.Media.Delete(r.Context(), scopeOf(r), chi.URLParam(r, "id"))
	if errors.Is(err, media.ErrInUse) {
		a.json(w, http.StatusConflict, map[string]any{"error": map[string]any{
			"code": err.Error(), "message": http.StatusText(http.StatusConflict), "pages": refsJSON(pages)}})
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func refsJSON(refs []repo.PageRef) []map[string]string {
	out := make([]map[string]string, len(refs))
	for i, p := range refs {
		out[i] = map[string]string{"slug": p.Slug, "title": p.Title}
	}
	return out
}
