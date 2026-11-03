package httpapi

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"slices"
	"time"

	"github.com/athomo001/kudawiki/internal/areaio"
	"github.com/athomo001/kudawiki/internal/repo"
)

// areaIOService es exportar e importar un área como Markdown (§13.8).
type areaIOService interface {
	Export(ctx context.Context, sc repo.Scope, area repo.Workspace, w io.Writer) error
	Import(ctx context.Context, t areaio.Target, zr *zip.Reader, dryRun bool) (areaio.Report, error)
}

// maxImportBytes es el ZIP más grande que se recibe: un área con años de archivos cabe; un
// abuso que llene el disco, no.
const maxImportBytes = 1 << 30

// exportArea descarga el ZIP. Lo arma con el rango de quien exporta: el admin ve todo el área,
// pero si otro rol llegara aquí, tampoco saldría lo que no puede leer (§3.2).
func (a api) exportArea(w http.ResponseWriter, r *http.Request) {
	sc := scopeOf(r)
	ws, err := a.Read.WorkspaceByID(r.Context(), sc.WsID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="kudawiki-`+ws.Slug+`.zip"`)
	w.Header().Set("Cache-Control", "no-store")
	if err := a.AreaIO.Export(r.Context(), sc, ws, w); err != nil {
		// La respuesta ya empezó: queda en el log y el ZIP llega cortado (no abre).
		a.Log.Error("export area", "ws", sc.WsID, "err", err)
	}
}

// exportMyAreas es "Exportar todas mis áreas" (F8-B): un ZIP con el ZIP de cada área donde quien
// pide es admin, el mismo que bajaría de a una. Sin ninguna, el 404 de cualquier ruta del área. El
// superadmin no tiene atajo: su respaldo es el completo (decisión del dueño del 2026-11-03).
func (a api) exportMyAreas(w http.ResponseWriter, r *http.Request) {
	ms, err := a.Read.Memberships(r.Context(), currentUser(r).ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	ms = slices.DeleteFunc(ms, func(m repo.Membership) bool { return m.Role != "admin" })
	if len(ms) == 0 {
		a.notFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="kudawiki-mis-areas.zip"`)
	w.Header().Set("Cache-Control", "no-store")
	zw := zip.NewWriter(w)
	for _, m := range ms {
		// Cada ZIP ya viene comprimido: se guarda tal cual.
		f, err := zw.CreateHeader(&zip.FileHeader{Name: m.Slug + ".zip", Method: zip.Store, Modified: time.Now()})
		if err == nil {
			err = a.AreaIO.Export(r.Context(), repo.Scope{WsID: m.ID, MaxRank: repo.RoleRank(m.Role)}, m.Workspace, f)
		}
		if err != nil {
			// Como en exportArea: la respuesta ya empezó y el ZIP llega cortado.
			a.Log.Error("export my areas", "ws", m.ID, "err", err)
			return
		}
	}
	if err := zw.Close(); err != nil {
		a.Log.Error("export my areas", "err", err)
	}
}

// importArea recibe el ZIP como el campo "file". Con ?dry_run=1 no crea nada: devuelve el informe
// de lo que haría, que la interfaz muestra antes de confirmar.
func (a api) importArea(w http.ResponseWriter, r *http.Request) {
	mr, err := r.MultipartReader()
	if err != nil {
		a.error(w, r, http.StatusBadRequest, "invalid_request", nil)
		return
	}
	var part io.Reader
	for {
		p, err := mr.NextPart()
		if err != nil {
			a.error(w, r, http.StatusBadRequest, "invalid_request", nil)
			return
		}
		if p.FormName() == "file" {
			part = p
			break
		}
	}
	// zip necesita leer en cualquier orden: el ZIP va a un archivo temporal junto al almacenamiento
	// (en Docker /tmp es memoria) y se borra al terminar.
	tmp, err := os.CreateTemp(a.StorageDir, ".import-*.zip")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	n, err := io.Copy(tmp, io.LimitReader(part, maxImportBytes+1))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if n > maxImportBytes {
		a.error(w, r, http.StatusRequestEntityTooLarge, "file_too_large", nil)
		return
	}
	zr, err := zip.NewReader(tmp, n)
	if err != nil {
		a.error(w, r, http.StatusUnprocessableEntity, "invalid_zip", nil)
		return
	}
	// El slug vigente, no el de la URL: puede ser uno viejo (B17) y las URL de media lo llevan.
	ws, err := a.Read.WorkspaceByID(r.Context(), scopeOf(r).WsID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	t := areaio.Target{Scope: scopeOf(r), Area: ws.Slug, ActorID: currentUser(r).ID}
	rep, err := a.AreaIO.Import(r.Context(), t, zr, r.URL.Query().Get("dry_run") == "1")
	if errors.Is(err, areaio.ErrTooManyFiles) {
		a.error(w, r, http.StatusUnprocessableEntity, "import_too_many_files", nil)
		return
	}
	if errors.Is(err, areaio.ErrTooLarge) {
		a.error(w, r, http.StatusRequestEntityTooLarge, "file_too_large", nil)
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, rep)
}
