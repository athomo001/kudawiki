package areaio

import (
	"archive/zip"
	"context"
	"io"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/repo"
)

type serviceStore interface {
	exportStore
	importStore
}

type mediaFiles interface {
	opener
	uploader
}

// Service es exportar e importar desde el panel del área, con su auditoría (§13.8).
type Service struct {
	store serviceStore
	pages Writer
	media mediaFiles
	audit *audit.Recorder
	now   func() time.Time
}

func NewService(st serviceStore, pages Writer, media mediaFiles, rec *audit.Recorder, now func() time.Time) *Service {
	return &Service{store: st, pages: pages, media: media, audit: rec, now: now}
}

// Export escribe el ZIP del área con lo que el rango de sc deja ver.
func (s *Service) Export(ctx context.Context, sc repo.Scope, area repo.Workspace, w io.Writer) error {
	m, err := Export(ctx, s.store, s.media, sc, area, w, s.now())
	if err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.WsExport, WsID: sc.WsID, TargetType: "workspace", TargetID: sc.WsID,
		Meta: map[string]any{"pages": m.Pages, "media": m.Media}})
	return nil
}

// Import crea en el área las páginas de un ZIP. Con dryRun solo dice qué haría, y no se audita.
func (s *Service) Import(ctx context.Context, t Target, zr *zip.Reader, dryRun bool) (Report, error) {
	pages, skipped, err := Read(zr)
	if err != nil {
		return Report{}, err
	}
	rep, err := Importer{Store: s.store, Pages: s.pages, Media: s.media}.Run(ctx, t, zr, pages, dryRun)
	rep.Skipped = append(skipped, rep.Skipped...)
	if err != nil || dryRun {
		return rep, err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.WsImport, WsID: t.Scope.WsID, TargetType: "workspace", TargetID: t.Scope.WsID,
		Meta: map[string]any{"pages": len(rep.Pages), "media": rep.Media, "skipped": len(rep.Skipped)}})
	return rep, nil
}
