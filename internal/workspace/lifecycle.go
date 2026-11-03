package workspace

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/mail"
	"github.com/athomo001/kudawiki/internal/repo"
)

var (
	// ErrInvalidStatus: un estado que no es active, archived ni deactivated.
	ErrInvalidStatus = errors.New("invalid_status")
	// ErrConfirmSlug: para borrar un área hay que escribir su slug (§3.5).
	ErrConfirmSlug = errors.New("confirm_slug")
	// ErrDeletePending: el área tiene el borrado pedido; primero hay que cancelarlo.
	ErrDeletePending = errors.New("delete_pending")
)

type lifecycleStore interface {
	WorkspaceByID(ctx context.Context, id string) (repo.Workspace, error)
	SetWorkspaceStatus(ctx context.Context, id, status string) error
	RequestWorkspaceDelete(ctx context.Context, id, by string, at time.Time) error
	CancelWorkspaceDelete(ctx context.Context, id string) error
	DueWorkspaceDeletes(ctx context.Context, now time.Time) ([]repo.Workspace, error)
	PurgeWorkspace(ctx context.Context, id string, now time.Time) (int, []repo.MediaFile, error)
	RenameWorkspaceSlug(ctx context.Context, id, slug string) (string, error)
	ForeignSealedHolders(ctx context.Context, wsID string) ([]repo.User, error)
}

type lifecycleMailer interface {
	Enabled(ctx context.Context) (bool, error)
	Enqueue(ctx context.Context, m mail.Message) error
}

// Lifecycle es el ciclo de vida de un área (SPEC §3.5): archivar, desactivar, borrar con un
// plazo de gracia y renombrar su slug (B17). Todo es del superadmin; las rutas lo exigen, y el
// borrado además la reautenticación.
type Lifecycle struct {
	store      lifecycleStore
	audit      *audit.Recorder
	mail       lifecycleMailer
	storageDir string
	grace      time.Duration
	now        func() time.Time
	log        *slog.Logger
}

func NewLifecycle(s lifecycleStore, rec *audit.Recorder, m lifecycleMailer, storageDir string, grace time.Duration, now func() time.Time, log *slog.Logger) *Lifecycle {
	return &Lifecycle{store: s, audit: rec, mail: m, storageDir: storageDir, grace: grace, now: now, log: log}
}

var statusActions = map[string]audit.Action{
	"active:archived":      audit.WsArchive,
	"archived:active":      audit.WsUnarchive,
	"active:deactivated":   audit.WsDeactivate,
	"archived:deactivated": audit.WsDeactivate,
	"deactivated:active":   audit.WsReactivate,
	"deactivated:archived": audit.WsReactivate,
}

// SetStatus archiva, desactiva o vuelve a activar el área. Es reversible: no borra nada.
func (l *Lifecycle) SetStatus(ctx context.Context, id, status string) error {
	w, err := l.store.WorkspaceByID(ctx, id)
	if err != nil || w.Kind != "area" {
		return repo.ErrNotFound
	}
	if w.DeleteAt != nil {
		return ErrDeletePending
	}
	if w.Status == status {
		return nil
	}
	action, ok := statusActions[w.Status+":"+status]
	if !ok {
		return ErrInvalidStatus
	}
	if err := l.store.SetWorkspaceStatus(ctx, id, status); err != nil {
		return err
	}
	l.audit.Record(ctx, audit.Event{Action: action, WsID: id, TargetType: "workspace", TargetID: id,
		Meta: map[string]any{"from": w.Status, "to": status}})
	return nil
}

// RequestDelete pide borrar el área: desde ya nadie la ve y se purga al terminar el plazo de
// gracia. Hay que escribir el slug, para que no se borre la equivocada. Quienes tienen acceso a
// uno de sus sellados sin ser del área reciben un correo: lo pierden sin haberlo visto venir.
func (l *Lifecycle) RequestDelete(ctx context.Context, actorID, id, confirm string) (time.Time, error) {
	w, err := l.store.WorkspaceByID(ctx, id)
	if err != nil || w.Kind != "area" {
		return time.Time{}, repo.ErrNotFound
	}
	if strings.TrimSpace(confirm) != w.Slug {
		return time.Time{}, ErrConfirmSlug
	}
	if w.DeleteAt != nil {
		return *w.DeleteAt, ErrDeletePending
	}
	at := l.now().Add(l.grace)
	if err := l.store.RequestWorkspaceDelete(ctx, id, actorID, at); err != nil {
		return time.Time{}, err
	}
	l.audit.Record(ctx, audit.Event{Action: audit.WsDeleteRequest, WsID: id, TargetType: "workspace", TargetID: id,
		Meta: map[string]any{"slug": w.Slug, "purge_at": at.UTC().Format(time.DateOnly)}})
	l.warnSealedHolders(ctx, w, at)
	return at, nil
}

func (l *Lifecycle) warnSealedHolders(ctx context.Context, w repo.Workspace, at time.Time) {
	if on, err := l.mail.Enabled(ctx); err != nil || !on {
		return
	}
	users, err := l.store.ForeignSealedHolders(ctx, w.ID)
	if err != nil {
		// El borrado ya quedó pedido y en la auditoría; el plazo de gracia da tiempo a avisar a mano.
		l.log.Error("area delete: sealed holders", "workspace", w.ID, "err", err)
		return
	}
	for _, u := range users {
		if err := l.mail.Enqueue(ctx, mail.Message{To: u.Email, Lang: u.Locale, Template: "area_delete_sealed",
			Vars: map[string]string{"name": u.FullName, "area": w.Name, "date": at.UTC().Format(time.DateOnly)}}); err != nil {
			l.log.Error("area delete: sealed holders mail", "workspace", w.ID, "err", err)
		}
	}
}

// CancelDelete deja el área como estaba antes del pedido.
func (l *Lifecycle) CancelDelete(ctx context.Context, id string) error {
	if err := l.store.CancelWorkspaceDelete(ctx, id); err != nil {
		return err
	}
	l.audit.Record(ctx, audit.Event{Action: audit.WsDeleteCancel, WsID: id, TargetType: "workspace", TargetID: id})
	return nil
}

// Rename cambia el slug; el viejo queda como alias, así las URL y los enlaces a media que lo
// usan siguen funcionando para sus miembros (B17).
func (l *Lifecycle) Rename(ctx context.Context, id, slug string) error {
	slug = strings.TrimSpace(slug)
	if !slugRe.MatchString(slug) {
		return ErrInvalidArea
	}
	old, err := l.store.RenameWorkspaceSlug(ctx, id, slug)
	if errors.Is(err, repo.ErrConflict) {
		return ErrSlugTaken
	}
	if err != nil || old == slug {
		return err
	}
	l.audit.Record(ctx, audit.Event{Action: audit.WsSlugChange, WsID: id, TargetType: "workspace", TargetID: id,
		Meta: map[string]any{"from": old, "to": slug}})
	return nil
}

// Run purga, una vez por hora, las áreas cuyo plazo de gracia terminó.
func (l *Lifecycle) Run(ctx context.Context) {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		if err := l.PurgeDue(ctx); err != nil && ctx.Err() == nil {
			l.log.Error("area purge", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// PurgeDue borra para siempre las áreas vencidas, con sus archivos en el disco. La carpeta del
// área (storage/ws_<id>) se va entera: también los .enc de sus sellados, cuyas llaves envueltas
// ya no existen.
func (l *Lifecycle) PurgeDue(ctx context.Context) error {
	due, err := l.store.DueWorkspaceDeletes(ctx, l.now())
	if err != nil {
		return err
	}
	for _, w := range due {
		pages, files, err := l.store.PurgeWorkspace(ctx, w.ID, l.now())
		if errors.Is(err, repo.ErrNotFound) {
			continue // lo cancelaron mientras tanto
		}
		if err != nil {
			return err
		}
		if err := os.RemoveAll(filepath.Join(l.storageDir, "ws_"+w.ID)); err != nil {
			l.log.Error("area purge: files", "workspace", w.ID, "err", err)
		}
		l.audit.Record(ctx, audit.Event{Action: audit.WsPurge, WsID: w.ID, TargetType: "workspace", TargetID: w.ID, System: "jobs",
			Meta: map[string]any{"slug": w.Slug, "name": w.Name, "pages": pages, "files": len(files)}})
	}
	return nil
}
