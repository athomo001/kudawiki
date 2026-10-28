// Package jobs tiene las tareas periódicas del sistema (SPEC §16). Lo que hace un job lo hace
// el sistema y no una persona: queda en la auditoría sin actor y con meta.system (§11.2).
package jobs

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/repo"
)

const (
	orphanAge     = 7 * 24 * time.Hour  // media sin página → papelera (§4.4)
	missRetention = 90 * 24 * time.Hour // búsquedas sin resultados (§9.6)
	system        = "daily_purge"       // meta.system de lo que registra este job
)

type store interface {
	TrashOrphanMedia(ctx context.Context, cutoff time.Time) (int64, error)
	PurgeTrash(ctx context.Context, cutoff time.Time) ([]repo.PurgedPage, []repo.MediaFile, error)
	PurgeSearchMisses(ctx context.Context, cutoff time.Time) error
	PurgeAudit(ctx context.Context, cutoff string) (int64, error)
}

type files interface {
	Remove(wsID, id string) error
}

// Purge es la purga diaria: la papelera (§4.5), las búsquedas sin resultados (§9.6) y la
// auditoría vencida (§11.1).
type Purge struct {
	Store          store
	Files          files
	Audit          *audit.Recorder
	Retention      time.Duration // trash.retention_days
	AuditRetention time.Duration // audit.retention_days, nunca menos de 180 días
	Now            func() time.Time
	Log            *slog.Logger
}

// Run hace una pasada. Si falla, lo vencido sigue vencido y la próxima pasada lo retoma.
func (p Purge) Run(ctx context.Context) error {
	now := p.Now()
	orphans, err := p.Store.TrashOrphanMedia(ctx, now.Add(-orphanAge))
	if err != nil {
		return err
	}
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
	events, err := p.Store.PurgeAudit(ctx, now.Add(-p.AuditRetention).UTC().Format(audit.TimeFormat))
	errs = errors.Join(errs, err)
	p.Log.Info("daily purge", "pages", len(pages), "media_files", len(purged), "orphans_trashed", orphans, "audit_events", events)
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
