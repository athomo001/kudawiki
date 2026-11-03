package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/backup"
)

// remote es el destino externo de los respaldos (§13.3, §21.2.4); lo implementa backup.S3.
type remote interface {
	Put(ctx context.Context, name string, body io.Reader, size int64) error
	Get(ctx context.Context, name string) (io.ReadCloser, error)
	Key(name string) string
}

// jobStore guarda el estado de cada paso, para /readyz y la pantalla Salud (§13.5, §21.2.4).
type jobStore interface {
	JobState(ctx context.Context, name string) (string, error)
	SetJobState(ctx context.Context, name, valueJSON string) error
}

// Nombres de los pasos en job_state.
const (
	BackupDailyJob  = "backup_daily"
	BackupUploadJob = "backup_upload"
	BackupVerifyJob = "backup_verify"
)

// VerifyEvery es cada cuánto se prueba restaurar el último respaldo (§21.2.4: semanal).
const VerifyEvery = 7 * 24 * time.Hour

// JobRun es el estado que queda de cada paso.
type JobRun struct {
	At     time.Time `json:"at"`
	File   string    `json:"file,omitempty"`
	Bytes  int64     `json:"bytes,omitempty"`
	Source string    `json:"source,omitempty"` // la verificación: local o remote
	Events int       `json:"events,omitempty"`
	OK     bool      `json:"ok"`
	Error  string    `json:"error,omitempty"`
}

// LastRun lee el estado de un paso; ok es false si nunca corrió.
func LastRun(ctx context.Context, s interface {
	JobState(ctx context.Context, name string) (string, error)
}, name string) (JobRun, bool, error) {
	raw, err := s.JobState(ctx, name)
	if err != nil || raw == "" {
		return JobRun{}, false, err
	}
	var r JobRun
	return r, true, json.Unmarshal([]byte(raw), &r)
}

// SaveRun guarda el estado de un paso.
func SaveRun(ctx context.Context, s jobStore, name string, r JobRun) error {
	b, _ := json.Marshal(r)
	return s.SetJobState(context.WithoutCancel(ctx), name, string(b))
}

// Backup es el respaldo diario (SPEC §13.3): una vez al día a la hora configurada, y después
// la rotación. Con destino externo, sube una copia cifrada con la llave de respaldo; y una vez
// por semana prueba restaurar el último (§21.2.4), del destino externo si lo hay. Cada paso
// queda en la auditoría como evento del sistema y en job_state; si uno falla, se avisa a los
// superadmins (Notify).
type Backup struct {
	DB           *sql.DB     // el pool de lectura: VACUUM INTO no frena las escrituras
	Kind         backup.Kind // vacío: el diario. Manual (Respaldar ahora) y PreReset los pide el superadmin (F8-B)
	StorageDir   string
	Dir          string
	Keep         backup.Retention
	Hour, Minute int
	Audit        *audit.Recorder
	State        jobStore
	Remote       remote // nil: solo local
	Key          []byte // la llave de respaldo; sin ella no se sube nada
	Notify       func(ctx context.Context, step, detail string)
	Now          func() time.Time
	Log          *slog.Logger
}

// Run hace un respaldo y rota los viejos; después lo sube y, si toca, prueba restaurar.
func (b Backup) Run(ctx context.Context) error {
	_, err := b.Make(ctx)
	return err
}

// system es quién figura en la auditoría: el job diario, o nadie, y entonces el actor del contexto
// (el superadmin que lo pidió desde el panel).
func (b Backup) system(name string) string {
	if b.Kind == "" || b.Kind == backup.Daily {
		return name
	}
	return ""
}

// Make es Run devolviendo la ruta del respaldo. Solo el diario rota los viejos: los demás los
// maneja quien opera (§13.3). Un fallo de la subida o de la prueba no lo deshace: queda en
// job_state y en la auditoría, y se avisa.
func (b Backup) Make(ctx context.Context) (string, error) {
	kind := b.Kind
	if kind == "" {
		kind = backup.Daily
	}
	path, err := backup.Create(ctx, b.DB, b.StorageDir, b.Dir, kind, b.Now())
	if err != nil {
		b.failed(ctx, BackupDailyJob, "", err)
		return "", err
	}
	var size int64
	if st, err := os.Stat(path); err == nil {
		size = st.Size()
	}
	var removed []string
	if kind == backup.Daily {
		removed, err = backup.Prune(b.Dir, b.Keep)
	}
	b.Audit.Record(ctx, audit.Event{Action: audit.BackupRun, TargetType: "backup", TargetID: filepath.Base(path), System: b.system("daily_backup"),
		Meta: map[string]any{"kind": string(kind), "bytes": size, "pruned": len(removed)}})
	b.Log.Info("backup", "kind", kind, "file", filepath.Base(path), "bytes", size, "pruned", len(removed))
	SaveRun(ctx, b.State, BackupDailyJob, JobRun{At: b.Now(), File: filepath.Base(path), Bytes: size, OK: true})

	uploaded := false
	if b.Remote != nil {
		if err := b.upload(ctx, path); err != nil {
			b.failed(ctx, BackupUploadJob, filepath.Base(path), err)
		} else {
			uploaded = true
		}
	}
	if last, ok, _ := LastRun(ctx, b.State, BackupVerifyJob); !ok || b.Now().Sub(last.At) >= VerifyEvery {
		b.Verify(ctx, path, uploaded)
	}
	return path, err
}

// upload cifra el respaldo con la llave de respaldo y lo sube; la copia cifrada local se borra.
func (b Backup) upload(ctx context.Context, path string) error {
	enc, err := backup.EncryptFile(path, b.Key)
	if err != nil {
		return err
	}
	defer os.Remove(enc)
	f, err := os.Open(enc)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	name := filepath.Base(enc)
	if err := b.Remote.Put(ctx, name, f, st.Size()); err != nil {
		return err
	}
	b.Audit.Record(ctx, audit.Event{Action: audit.BackupUpload, TargetType: "backup", TargetID: name, System: b.system("daily_backup"),
		Meta: map[string]any{"bytes": st.Size(), "key": b.Remote.Key(name)}})
	b.Log.Info("backup uploaded", "object", b.Remote.Key(name), "bytes", st.Size())
	return SaveRun(ctx, b.State, BackupUploadJob, JobRun{At: b.Now(), File: name, Bytes: st.Size(), OK: true})
}

// Verify prueba restaurar el respaldo: el que se acaba de subir, bajado del destino externo, o
// si no, el local. Es lo que el panel muestra como "última restauración probada".
func (b Backup) Verify(ctx context.Context, path string, fromRemote bool) {
	source, file := "local", path
	if fromRemote {
		source = "remote"
		name := filepath.Base(path) + backup.EncSuffix
		tmp, err := b.download(ctx, name)
		if err != nil {
			b.failed(ctx, BackupVerifyJob, name, err)
			return
		}
		defer os.Remove(tmp)
		file = tmp
	}
	res, err := CheckBackup(ctx, file, b.Key, b.Dir)
	if err != nil {
		b.failed(ctx, BackupVerifyJob, filepath.Base(path), err)
		return
	}
	b.Audit.Record(ctx, audit.Event{Action: audit.BackupVerify, TargetType: "backup", TargetID: filepath.Base(path), System: "backup_verify",
		Meta: map[string]any{"ok": true, "source": source, "files": len(res.Manifest.Files), "schema": res.Manifest.Schema, "events": res.Events}})
	b.Log.Info("backup verified", "file", filepath.Base(path), "source", source)
	SaveRun(ctx, b.State, BackupVerifyJob, JobRun{At: b.Now(), File: filepath.Base(path), Source: source, Events: res.Events, OK: true})
}

func (b Backup) download(ctx context.Context, name string) (string, error) {
	body, err := b.Remote.Get(ctx, name)
	if err != nil {
		return "", err
	}
	defer body.Close()
	f, err := os.CreateTemp(b.Dir, ".download-*"+backup.EncSuffix)
	if err != nil {
		return "", err
	}
	_, err = io.Copy(f, body)
	if err = errors.Join(err, f.Close()); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// failed deja el error del paso en job_state, la auditoría y el log, y avisa a los superadmins.
func (b Backup) failed(ctx context.Context, step, file string, err error) {
	if ctx.Err() != nil {
		return // el servidor se está deteniendo: no es un fallo del respaldo
	}
	b.Log.Error("backup step failed", "step", step, "file", file, "err", err)
	SaveRun(ctx, b.State, step, JobRun{At: b.Now(), File: file, Error: err.Error()})
	b.Audit.Record(ctx, audit.Event{Action: audit.BackupFailed, TargetType: "backup", TargetID: file, System: step,
		Meta: map[string]any{"step": step}})
	if b.Notify != nil {
		b.Notify(ctx, step, err.Error())
	}
}

// NextRun es la próxima vez que el reloj marca hour:minute en la zona de now (hoy o mañana).
func NextRun(now time.Time, hour, minute int) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}
