package health

import (
	"context"
	"time"

	"github.com/athomo001/kudawiki/internal/jobs"
)

// jobStates es lo que los chequeos de los respaldos leen de repo.
type jobStates interface {
	JobState(ctx context.Context, name string) (string, error)
}

// Backups es el estado de los respaldos que muestra la pantalla Salud del superadmin (§21.2.4:
// "última restauración probada: hace 5 días"); nil donde el paso nunca corrió.
type Backups struct {
	Daily  *jobs.JobRun `json:"daily,omitempty"`
	Upload *jobs.JobRun `json:"upload,omitempty"`
	Verify *jobs.JobRun `json:"verify,omitempty"`
}

// Plazos: un respaldo diario de más de dos días, o una prueba de restauración de más de ocho
// (es semanal), quiere decir que el job no está corriendo.
const (
	backupStale = 2 * 24 * time.Hour
	verifyStale = jobs.VerifyEvery + 24*time.Hour
)

// BackupChecks revisa los respaldos (§13.3, §21.2.4). Son de la instalación, no de un área:
// los ven el superadmin y doctor.
func BackupChecks(ctx context.Context, st jobStates, now time.Time) (Backups, []Problem, error) {
	var b Backups
	var out []Problem
	add := func(check string, sev Severity, detail string) {
		out = append(out, Problem{Check: check, Severity: sev, Detail: detail})
	}
	for _, step := range []struct {
		name string
		dst  **jobs.JobRun
	}{{jobs.BackupDailyJob, &b.Daily}, {jobs.BackupUploadJob, &b.Upload}, {jobs.BackupVerifyJob, &b.Verify}} {
		run, ok, err := jobs.LastRun(ctx, st, step.name)
		if err != nil {
			return b, nil, err
		}
		if ok {
			*step.dst = &run
		}
	}
	day := func(t time.Time) string { return t.UTC().Format(time.DateOnly) }
	switch {
	case b.Daily == nil:
		add("backup.none", Warning, "")
	case b.Daily.Error != "":
		add("backup.failed", Error, b.Daily.Error)
	case now.Sub(b.Daily.At) > backupStale:
		add("backup.stale", Error, day(b.Daily.At))
	}
	if b.Upload != nil && b.Upload.Error != "" {
		add("backup.upload_failed", Error, b.Upload.Error)
	}
	switch {
	case b.Verify == nil:
		add("backup.never_verified", Warning, "")
	case b.Verify.Error != "":
		add("backup.verify_failed", Error, b.Verify.Error)
	case now.Sub(b.Verify.At) > verifyStale:
		add("backup.verify_stale", Warning, day(b.Verify.At))
	}
	return b, out, nil
}
