package httpapi

import (
	"mime"
	"net/http"
	"strings"

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
