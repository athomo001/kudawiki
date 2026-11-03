package httpapi

import (
	"context"
	"net/http"
	"os"

	"github.com/athomo001/kudawiki/internal/backup"
	"github.com/athomo001/kudawiki/internal/backups"
	"github.com/go-chi/chi/v5"
)

// Respaldos (SPEC §13.3, F8-B): la configuración del respaldo diario, la lista, "Respaldar
// ahora" y "Borrar todo", solo del superadmin. Respaldar y borrar tardan más que una petición:
// responden 202 y la pantalla vuelve a pedir el estado hasta que terminan.

// backupsService es Administración → Respaldos.
type backupsService interface {
	State(ctx context.Context) (backups.State, error)
	Update(ctx context.Context, actorID string, p backups.Patch) (backups.Settings, error)
	RunNow(ctx context.Context) error
	Reset(ctx context.Context, keep, confirm string) error
	Delete(ctx context.Context, name string) error
	Find(name string) (backup.Entry, error)
	Download(ctx context.Context, name string) (*os.File, backup.Entry, error)
}

func (a api) backupAdminRoutes(r chi.Router) {
	r.With(noStore).Get("/backups", a.getBackups)
	r.Put("/backups/settings", a.putBackupSettings)
	r.Post("/backups", a.runBackup)
	// Borrar un respaldo quita una vuelta atrás: pide reautenticarse, como borrar todo.
	r.With(a.requireReauth).Delete("/backups/{name}", a.deleteBackup)
	// Bajar un respaldo (F8-B.7) también: trae todo el contenido sin cifrar. Un enlace del
	// navegador no puede abrir el diálogo de reautenticación, así que la pantalla pide antes el
	// respaldo con fetch (misma reautenticación; un HEAD no traería el código del error) y recién
	// entonces navega a la descarga.
	r.With(a.requireReauth, noStore).Get("/backups/{name}", a.checkBackupDownload)
	r.With(a.requireReauth, noStore).Get("/backups/{name}/download", a.downloadBackup)
	// Borrar todo pide reautenticarse (contraseña y segundo factor) y la frase escrita.
	r.With(a.requireReauth).Post("/reset", a.resetSite)
}

func (a api) getBackups(w http.ResponseWriter, r *http.Request) {
	s, err := a.Backups.State(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, s)
}

func (a api) putBackupSettings(w http.ResponseWriter, r *http.Request) {
	var p backups.Patch
	if !a.decode(w, r, &p) {
		return
	}
	s, err := a.Backups.Update(r.Context(), currentUser(r).ID, p)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, s)
}

func (a api) runBackup(w http.ResponseWriter, r *http.Request) {
	if err := a.Backups.RunNow(r.Context()); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (a api) deleteBackup(w http.ResponseWriter, r *http.Request) {
	if err := a.Backups.Delete(r.Context(), chi.URLParam(r, "name")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a api) checkBackupDownload(w http.ResponseWriter, r *http.Request) {
	e, err := a.Backups.Find(chi.URLParam(r, "name"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, e)
}

// downloadBackup manda el archivo tal cual; ServeContent atiende Range, así una descarga
// cortada se puede retomar.
func (a api) downloadBackup(w http.ResponseWriter, r *http.Request) {
	f, e, err := a.Backups.Download(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+e.Name+`"`) // el nombre ya es nuestro: sin comillas ni rutas
	http.ServeContent(w, r, e.Name, e.CreatedAt, f)
}

// resetSite deja solo la cuenta de quien lo pide: nunca otra, para que nadie quede afuera de su
// propia instalación.
func (a api) resetSite(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Confirm string `json:"confirm"`
	}
	if !a.decode(w, r, &body) {
		return
	}
	if err := a.Backups.Reset(r.Context(), currentUser(r).ID, body.Confirm); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
