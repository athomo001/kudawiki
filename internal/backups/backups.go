package backups

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/backup"
	"github.com/athomo001/kudawiki/internal/crypto"
	"github.com/athomo001/kudawiki/internal/jobs"
	"github.com/athomo001/kudawiki/internal/repo"
)

type store interface {
	SiteSettings(ctx context.Context, prefix string) (map[string]string, error)
	SiteSecret(ctx context.Context, key string) ([]byte, int, error)
	SaveSiteSettings(ctx context.Context, values map[string]*string, secrets map[string]repo.SealedSetting, by string) error
	JobState(ctx context.Context, name string) (string, error)
	SetJobState(ctx context.Context, name, valueJSON string) error
	Wipe(ctx context.Context, keep string) (repo.WipeReport, []repo.MediaFile, error)
	EnsurePersonal(ctx context.Context, userID string) (repo.Workspace, error)
}

// Service es la pantalla de respaldos y el job diario, que lee su configuración en cada vuelta.
type Service struct {
	store      store
	audit      *audit.Recorder
	key        crypto.MasterKey
	fixed      Fixed
	db         *sql.DB // el pool de lectura: VACUUM INTO no frena las escrituras
	storageDir string
	dir        string
	keyFile    string
	notify     func(ctx context.Context, step, detail string)
	now        func() time.Time
	log        *slog.Logger

	// Un solo respaldo o borrado a la vez: dos VACUUM INTO juntos solo gastan disco, y borrar
	// mientras se respalda dejaría un respaldo que no es ni el antes ni el después.
	mu      sync.Mutex
	running string // "", "backup" o "reset"
	last    *Result
	base    context.Context // el del servidor: lo que se pide desde el panel sigue aunque se cierre la pestaña
	changed chan struct{}
}

// Result es cómo terminó lo último que se pidió desde el panel.
type Result struct {
	Kind  string    `json:"kind"` // backup | reset
	At    time.Time `json:"at"`
	File  string    `json:"file,omitempty"`
	OK    bool      `json:"ok"`
	Error string    `json:"error,omitempty"`
}

type Options struct {
	Fixed      Fixed
	DB         *sql.DB
	StorageDir string
	Dir        string
	KeyFile    string
	Notify     func(ctx context.Context, step, detail string)
	Now        func() time.Time
	Log        *slog.Logger
}

func NewService(s store, rec *audit.Recorder, key crypto.MasterKey, o Options) *Service {
	return &Service{store: s, audit: rec, key: key, fixed: o.Fixed, db: o.DB, storageDir: o.StorageDir, dir: o.Dir,
		keyFile: o.KeyFile, notify: o.Notify, now: o.Now, log: o.Log, base: context.Background(), changed: make(chan struct{}, 1)}
}

func (s *Service) hasKey() bool {
	_, err := backup.LoadKey(s.keyFile)
	return err == nil
}

func (s *Service) wake() {
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

// job arma el respaldo con la configuración vigente. Sin la llave de respaldo no sube nada: la
// subida queda como fallida (y se avisa), pero el respaldo local se hace igual.
func (s *Service) job(ctx context.Context, kind backup.Kind) (jobs.Backup, config, error) {
	c, err := s.settings(ctx, true)
	if err != nil {
		return jobs.Backup{}, c, err
	}
	t, _ := time.Parse("15:04", c.Time)
	j := jobs.Backup{DB: s.db, Kind: kind, StorageDir: s.storageDir, Dir: s.dir, Hour: t.Hour(), Minute: t.Minute(),
		Keep:  backup.Retention{Daily: c.KeepDaily, Weekly: c.KeepWeekly, Monthly: c.KeepMonthly},
		Audit: s.audit, State: s.store, Notify: s.notify, Now: s.now, Log: s.log}
	if key, err := backup.LoadKey(s.keyFile); err == nil {
		j.Key = key
	}
	if c.RemoteOn() {
		r, err := backup.NewS3(c.Remote.Endpoint, c.Remote.Region, c.Remote.Bucket, c.Remote.Prefix, c.Remote.AccessKeyID, c.secret, c.Remote.PathStyle)
		switch {
		case err != nil:
			s.log.Error("backup remote misconfigured", "err", err)
		case j.Key == nil:
			s.log.Error("backup remote needs the backup key", "file", s.keyFile)
		default:
			j.Remote = r
		}
	}
	return j, c, nil
}

// Daily espera hasta la hora configurada, respalda, y repite hasta que ctx termine. Si el panel
// cambia la hora, vuelve a calcular cuándo le toca.
func (s *Service) Daily(ctx context.Context) {
	s.mu.Lock()
	s.base = ctx
	s.mu.Unlock()
	for {
		j, _, err := s.job(ctx, backup.Daily)
		if err != nil {
			s.log.Error("backup settings", "err", err)
			j.Hour, j.Minute = 3, 0
		}
		now := s.now()
		select {
		case <-ctx.Done():
			return
		case <-s.changed:
			continue
		case <-time.After(jobs.NextRun(now, j.Hour, j.Minute).Sub(now)):
		}
		if !s.begin("backup") {
			continue // hay uno del panel en curso: hoy ya hay respaldo
		}
		if j, _, err = s.job(ctx, backup.Daily); err == nil {
			err = j.Run(ctx)
		}
		s.end(nil)
		if err != nil && ctx.Err() == nil {
			s.log.Error("daily backup failed", "err", err)
		}
	}
}

func (s *Service) begin(kind string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running != "" {
		return false
	}
	s.running = kind
	return true
}

func (s *Service) end(r *Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = ""
	if r != nil {
		s.last = r
	}
}

// Entry es un respaldo de la lista, con lo que se sabe de él.
type Entry struct {
	backup.Entry
	Uploaded  bool `json:"uploaded"`  // es el último que subió al destino externo
	Verified  bool `json:"verified"`  // es el último que se probó restaurar
	Protected bool `json:"protected"` // no se puede borrar desde el panel (Delete)
}

// protected: el último probado y los de antes de "Borrar todo" se quedan (ErrProtected).
func protected(kind backup.Kind, verified bool) bool {
	return verified || kind == backup.PreReset
}

// State es lo que muestra la pantalla: la configuración, los respaldos, qué está corriendo y
// cómo terminó lo último que se pidió desde el panel.
type State struct {
	Settings Settings     `json:"settings"`
	Backups  []Entry      `json:"backups"`
	Running  string       `json:"running"`
	Last     *Result      `json:"last"`
	Daily    *jobs.JobRun `json:"daily"`
	Upload   *jobs.JobRun `json:"upload"`
	Verify   *jobs.JobRun `json:"verify"`
}

func (s *Service) State(ctx context.Context) (State, error) {
	set, err := s.Settings(ctx)
	if err != nil {
		return State{}, err
	}
	list, err := backup.List(s.dir)
	if err != nil {
		return State{}, err
	}
	st := State{Settings: set, Backups: []Entry{}}
	runs := map[string]**jobs.JobRun{jobs.BackupDailyJob: &st.Daily, jobs.BackupUploadJob: &st.Upload, jobs.BackupVerifyJob: &st.Verify}
	for name, dst := range runs {
		if r, ok, err := jobs.LastRun(ctx, s.store, name); err != nil {
			return State{}, err
		} else if ok {
			*dst = &r
		}
	}
	for _, e := range list {
		x := Entry{Entry: e}
		if st.Upload != nil && st.Upload.OK && strings.TrimSuffix(st.Upload.File, backup.EncSuffix) == e.Name {
			x.Uploaded = true
		}
		if st.Verify != nil && st.Verify.OK && st.Verify.File == e.Name {
			x.Verified = true
		}
		x.Protected = protected(e.Kind, x.Verified)
		st.Backups = append(st.Backups, x)
	}
	s.mu.Lock()
	st.Running, st.Last = s.running, s.last
	s.mu.Unlock()
	return st, nil
}

// RunNow es "Respaldar ahora": un respaldo manual completo, con la misma subida y prueba que el
// diario. Corre aparte, porque puede tardar más que una petición; el panel mira State. El actor
// queda en la auditoría.
func (s *Service) RunNow(ctx context.Context) error {
	if !s.begin("backup") {
		return ErrBusy
	}
	go func() {
		ctx := s.detach(ctx)
		r := &Result{Kind: "backup"}
		j, _, err := s.job(ctx, backup.Manual)
		var path string
		if err == nil {
			path, err = j.Make(ctx)
		}
		r.At, r.File, r.OK = s.now(), filepath.Base(path), err == nil
		if err != nil {
			r.Error = err.Error()
		}
		s.end(r)
	}()
	return nil
}

// Find es el respaldo de la lista con ese nombre: el nombre viene del navegador y solo vale uno
// que está en la carpeta con un nombre nuestro, nunca una ruta armada con otra cosa.
func (s *Service) Find(name string) (backup.Entry, error) {
	list, err := backup.List(s.dir)
	if err != nil {
		return backup.Entry{}, err
	}
	i := slices.IndexFunc(list, func(e backup.Entry) bool { return e.Name == name })
	if i < 0 {
		return backup.Entry{}, repo.ErrNotFound
	}
	return list[i], nil
}

// Download abre un respaldo para bajarlo desde el panel (F8-B.7, decisión del dueño del
// 2026-11-03: tal cual, sin cifrar). Trae la base entera, con las páginas de todas las áreas:
// por eso pide reautenticarse (la ruta) y queda en la auditoría con quién lo bajó.
func (s *Service) Download(ctx context.Context, name string) (*os.File, backup.Entry, error) {
	e, err := s.Find(name)
	if err != nil {
		return nil, e, err
	}
	f, err := os.Open(filepath.Join(s.dir, e.Name))
	if err != nil {
		return nil, e, err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.BackupDownload, TargetType: "backup", TargetID: e.Name,
		Meta: map[string]any{"kind": string(e.Kind), "bytes": e.Size}})
	return f, e, nil
}

// Delete borra un respaldo de la carpeta (F8-B.6), para que no se llene el disco. Solo uno que
// está en la lista: el nombre viene del navegador y nunca se arma una ruta con otra cosa. La
// copia del destino externo, si la hay, queda: allá la rota quien administra el bucket. No
// borra mientras se respalda o se borra todo, que pueden estar usando ese archivo.
func (s *Service) Delete(ctx context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running != "" {
		return ErrBusy
	}
	e, err := s.Find(name)
	if err != nil {
		return err
	}
	v, ok, err := jobs.LastRun(ctx, s.store, jobs.BackupVerifyJob)
	if err != nil {
		return err
	}
	if protected(e.Kind, ok && v.OK && v.File == e.Name) {
		return ErrProtected
	}
	if err := os.Remove(filepath.Join(s.dir, e.Name)); err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.BackupDelete, TargetType: "backup", TargetID: e.Name,
		Meta: map[string]any{"kind": string(e.Kind), "bytes": e.Size}})
	return nil
}

// detach sigue con el actor de la petición (para la auditoría) pero con la vida del servidor.
func (s *Service) detach(ctx context.Context) context.Context {
	s.mu.Lock()
	base := s.base
	s.mu.Unlock()
	return audit.WithActorFrom(base, ctx)
}

// ConfirmPhrase es lo que hay que escribir para borrar todo, igual en los dos idiomas: no se
// escribe por costumbre.
const ConfirmPhrase = "BORRAR TODO"

// Reset es "Borrar todo" (F8-B, decisión del dueño del 2026-11-03): deja la instalación con
// solo la cuenta de quien lo pide, que tiene que ser superadmin. Antes hace un respaldo completo
// y prueba restaurarlo; si algo de eso falla, no borra nada. La auditoría y la configuración del
// sitio quedan (repo.Wipe). Corre aparte, como RunNow.
func (s *Service) Reset(ctx context.Context, keep, confirm string) error {
	if confirm != ConfirmPhrase {
		return ErrConfirm
	}
	if !s.begin("reset") {
		return ErrBusy
	}
	go func() {
		ctx := s.detach(ctx)
		r := &Result{Kind: "reset"}
		file, err := s.reset(ctx, keep)
		r.At, r.File, r.OK = s.now(), file, err == nil
		if err != nil {
			r.Error = err.Error()
			s.log.Error("reset failed", "err", err)
		}
		s.end(r)
	}()
	return nil
}

func (s *Service) reset(ctx context.Context, keep string) (string, error) {
	j, _, err := s.job(ctx, backup.PreReset)
	if err != nil {
		return "", err
	}
	path, err := j.Make(ctx)
	if err != nil {
		return "", err
	}
	file := filepath.Base(path)
	// La prueba de restaurar es obligatoria acá, no semanal: después del borrado este respaldo
	// es lo único que queda.
	if _, err := jobs.CheckBackup(ctx, path, j.Key, s.dir); err != nil {
		return file, errors.Join(errors.New("the backup before the reset does not restore; nothing was deleted"), err)
	}
	rep, _, err := s.store.Wipe(ctx, keep)
	if err != nil {
		return file, err
	}
	// Todo lo que había en storage/ era de áreas y espacios que ya no existen. Los nombres son
	// ws_<id>: no se toca nada más de la carpeta.
	entries, err := os.ReadDir(s.storageDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return file, err
	}
	var errs error
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "ws_") {
			errs = errors.Join(errs, os.RemoveAll(filepath.Join(s.storageDir, e.Name())))
		}
	}
	if _, err := s.store.EnsurePersonal(ctx, keep); err != nil {
		errs = errors.Join(errs, err)
	}
	s.audit.Record(ctx, audit.Event{Action: audit.SiteReset, TargetType: "site", TargetID: file,
		Meta: map[string]any{"backup": file, "areas": rep.Areas, "users": rep.Users, "pages": rep.Pages, "files": rep.Files}})
	s.log.Warn("installation reset", "backup", file, "areas", rep.Areas, "users", rep.Users, "pages", rep.Pages)
	return file, errs
}
