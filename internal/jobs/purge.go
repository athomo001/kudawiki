// Package jobs tiene las tareas periódicas del sistema (SPEC §16). Lo que hace un job lo hace
// el sistema y no una persona: queda en la auditoría sin actor y con meta.system (§11.2).
package jobs

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/auth"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/athomo001/kudawiki/internal/wiki"
)

const (
	unusedMedia   = 24 * time.Hour       // media que nadie usa, o que alguien borró (§4.4)
	missRetention = 90 * 24 * time.Hour  // búsquedas sin resultados (§9.6)
	viewRetention = 90 * 24 * time.Hour  // visitas por página (§4.17)
	mailRetention = 30 * 24 * time.Hour  // correos ya entregados o rendidos (§12)
	draftAge      = 30 * 24 * time.Hour  // borradores del autoguardado sin tocar (§4.11)
	readNotices   = 90 * 24 * time.Hour  // notificaciones ya leídas (§13.10)
	loginSeen     = 365 * 24 * time.Hour // IP y navegador vistos, para el aviso de login nuevo (§8.2, §13.10)
	system        = "daily_purge"        // meta.system de lo que registra este job
)

type store interface {
	PurgeUnusedMedia(ctx context.Context, cutoff time.Time, refs func(string) []string) ([]repo.MediaFile, error)
	PurgeTrash(ctx context.Context, cutoff time.Time) ([]repo.PurgedPage, []repo.MediaFile, error)
	PurgeSearchMisses(ctx context.Context, cutoff time.Time) error
	PurgePageViews(ctx context.Context, cutoff time.Time) error
	PurgeEmailOutbox(ctx context.Context, cutoff time.Time) error
	PurgeDrafts(ctx context.Context, cutoff time.Time) error
	PurgeRevisions(ctx context.Context, now time.Time) (map[string]int, error)
	PurgeAudit(ctx context.Context, cutoff string) (int64, error)
	PurgePersonal(ctx context.Context, c repo.Retention) (map[string]int64, error)
	TruncateWAL(ctx context.Context) error
	Optimize(ctx context.Context) error
}

type files interface {
	Remove(wsID, id string) error
}

// Purge es la purga diaria: la papelera (§4.5), la media que nadie usa (§4.4), las búsquedas sin resultados (§9.6), las
// visitas por página (§4.17), los correos ya entregados (§12), los borradores abandonados
// (§4.11), las revisiones fuera de la retención de cada área (§4.3) y la auditoría vencida (§11.1).
type Purge struct {
	Store          store
	Files          files
	Audit          *audit.Recorder
	Retention      time.Duration // trash.retention_days
	AuditRetention time.Duration // audit.retention_days, nunca menos de 180 días
	QuizRetention  time.Duration // quizzes.retention_days: resultados de cuestionarios y aperturas (§13.10)
	Now            func() time.Time
	Log            *slog.Logger
}

// Run hace una pasada. Si falla, lo vencido sigue vencido y la próxima pasada lo retoma.
func (p Purge) Run(ctx context.Context) error {
	now := p.Now()
	pages, purged, err := p.Store.PurgeTrash(ctx, now.Add(-p.Retention))
	if err != nil {
		return err
	}
	for _, pg := range pages {
		p.Audit.Record(ctx, audit.Event{Action: audit.PagePurge, WsID: pg.WsID, TargetType: "page", TargetID: pg.ID, System: system})
	}
	// Un archivo que no se pudo borrar ya no tiene fila: lo encuentra la salud del contenido (§13.7).
	var errs error
	for _, f := range purged {
		errs = errors.Join(errs, p.Files.Remove(f.WsID, f.ID))
	}
	errs = errors.Join(errs, p.Store.PurgeSearchMisses(ctx, now.Add(-missRetention)))
	errs = errors.Join(errs, p.Store.PurgePageViews(ctx, now.Add(-viewRetention)))
	errs = errors.Join(errs, p.Store.PurgeEmailOutbox(ctx, now.Add(-mailRetention)))
	errs = errors.Join(errs, p.Store.PurgeDrafts(ctx, now.Add(-draftAge)))
	revisions, err := p.Store.PurgeRevisions(ctx, now)
	errs = errors.Join(errs, err)
	for wsID, n := range revisions {
		p.Audit.Record(ctx, audit.Event{Action: audit.RevisionPurge, WsID: wsID, TargetType: "workspace", TargetID: wsID, System: system,
			Meta: map[string]any{"count": n}})
	}
	// Después de las revisiones: la imagen que solo mostraba una revisión recién borrada ya está libre.
	unused, err := p.Store.PurgeUnusedMedia(ctx, now.Add(-unusedMedia), wiki.MediaRefs)
	errs = errors.Join(errs, err)
	for _, f := range unused {
		errs = errors.Join(errs, p.Files.Remove(f.WsID, f.ID))
	}
	events, err := p.Store.PurgeAudit(ctx, now.Add(-p.AuditRetention).UTC().Format(audit.TimeFormat))
	errs = errors.Join(errs, err)
	// Datos personales con vencimiento (§13.10): queda cuántos, nunca de quién.
	personal, err := p.Store.PurgePersonal(ctx, repo.Retention{SessionSeen: now.Add(-auth.SessionIdle), SessionCreated: now.Add(-auth.SessionAbsolute),
		NotificationRead: now.Add(-readNotices), LoginHistory: now.Add(-loginSeen), QuizResults: now.Add(-p.QuizRetention)})
	errs = errors.Join(errs, err)
	if personal["quiz_attempts"]+personal["read_views"]+personal["login_history"] > 0 {
		meta := map[string]any{}
		for k, v := range personal {
			meta[k] = v
		}
		p.Audit.Record(ctx, audit.Event{Action: audit.PrivacyRetention, TargetType: "system", TargetID: "retention", System: system, Meta: meta})
	}
	// Después de borrar, una vez al día (§13.4): el WAL vuelve a cero y SQLite actualiza sus
	// estadísticas. No más seguido: con 50 000 páginas, vaciar el WAL enfría la caché y la
	// búsqueda empeora (docs/rendimiento.md); entre pasadas, SQLite hace sus checkpoints solo.
	errs = errors.Join(errs, p.Store.TruncateWAL(ctx), p.Store.Optimize(ctx))
	p.Log.Info("daily purge", "pages", len(pages), "media_files", len(purged), "unused_media", len(unused), "audit_events", events)
	return errs
}

// Daily corre Run al arrancar y después cada 24 horas, hasta que ctx termine.
func (p Purge) Daily(ctx context.Context) {
	tick := time.NewTicker(24 * time.Hour)
	defer tick.Stop()
	for {
		if err := p.Run(ctx); err != nil && ctx.Err() == nil {
			p.Log.Error("daily purge failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
