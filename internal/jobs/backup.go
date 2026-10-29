package jobs

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/backup"
)

// Backup es el respaldo diario (SPEC §13.3): una vez al día a la hora configurada, y después
// la rotación. Queda en la auditoría como evento del sistema, con el nombre y el tamaño del
// archivo, nunca su contenido.
type Backup struct {
	DB           *sql.DB // el pool de lectura: VACUUM INTO no frena las escrituras
	StorageDir   string
	Dir          string
	Keep         backup.Retention
	Hour, Minute int
	Audit        *audit.Recorder
	Now          func() time.Time
	Log          *slog.Logger
}

// Run hace un respaldo y rota los viejos. Si falla, queda en el log; mañana se intenta de nuevo.
func (b Backup) Run(ctx context.Context) error {
	path, err := backup.Create(ctx, b.DB, b.StorageDir, b.Dir, backup.Daily, b.Now())
	if err != nil {
		return err
	}
	var size int64
	if st, err := os.Stat(path); err == nil {
		size = st.Size()
	}
	removed, err := backup.Prune(b.Dir, b.Keep)
	b.Audit.Record(ctx, audit.Event{Action: audit.BackupRun, TargetType: "backup", TargetID: filepath.Base(path), System: "daily_backup",
		Meta: map[string]any{"kind": string(backup.Daily), "bytes": size, "pruned": len(removed)}})
	b.Log.Info("daily backup", "file", filepath.Base(path), "bytes", size, "pruned", len(removed))
	return err
}

// Daily espera hasta la próxima hora configurada, respalda, y repite, hasta que ctx termine.
func (b Backup) Daily(ctx context.Context) {
	for {
		now := b.Now()
		select {
		case <-ctx.Done():
			return
		case <-time.After(nextRun(now, b.Hour, b.Minute).Sub(now)):
		}
		if err := b.Run(ctx); err != nil && ctx.Err() == nil {
			b.Log.Error("daily backup failed", "err", err)
		}
	}
}

// nextRun es la próxima vez que el reloj marca hour:minute en la zona de now (hoy o mañana).
func nextRun(now time.Time, hour, minute int) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}
