// Comando kudawiki: único punto donde se arman las dependencias (SPEC §16.2).
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/athomo001/kudawiki"
	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/auth"
	"github.com/athomo001/kudawiki/internal/backup"
	"github.com/athomo001/kudawiki/internal/branding"
	"github.com/athomo001/kudawiki/internal/config"
	"github.com/athomo001/kudawiki/internal/crypto"
	"github.com/athomo001/kudawiki/internal/database"
	"github.com/athomo001/kudawiki/internal/health"
	"github.com/athomo001/kudawiki/internal/httpapi"
	"github.com/athomo001/kudawiki/internal/jobs"
	"github.com/athomo001/kudawiki/internal/mail"
	"github.com/athomo001/kudawiki/internal/media"
	"github.com/athomo001/kudawiki/internal/notify"
	"github.com/athomo001/kudawiki/internal/personal"
	"github.com/athomo001/kudawiki/internal/presence"
	"github.com/athomo001/kudawiki/internal/proposals"
	"github.com/athomo001/kudawiki/internal/quiz"
	"github.com/athomo001/kudawiki/internal/reading"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/athomo001/kudawiki/internal/wiki"
	"github.com/athomo001/kudawiki/internal/workspace"
	"golang.org/x/term"
)

const usage = `usage: kudawiki <command> [flags]

commands:
  init         create the database, the master key and the first superadmin
               (KUDAWIKI_INIT_EMAIL, KUDAWIKI_INIT_NAME; password is prompted)
  serve        run the web server
  healthcheck  exit 0 if the local server answers /healthz (Docker HEALTHCHECK)
  search       "search rebuild": regenerate the search index of every page
  audit        "audit verify": check the hash chain of the audit log (exit 1 if broken)
  migrate      "migrate --dry-run": list the pending migrations without applying them
               (serve applies them, after a backup)
  backup       make a backup now into <data_dir>/backups (the master key is not included)
  restore      "restore <file>": verify a backup and put it in place; the server must be stopped
  doctor       check the content health (broken links, tree, media, search index);
               --ws <slug> for one area, --fix to rebuild the index and remove stray files
               (with the server stopped). Exit 1 if there are errors
  admin        console recovery, for when no superadmin can get in (recorded in the audit log):
                 admin unlock <email>                     lift the lockout after failed logins
                 admin set-password <email>               password is prompted; closes their sessions
                 admin create-superadmin <email> <name>   local emergency account; password is prompted
                 admin revoke-sessions --all | <email>    close the sessions of everyone or of one person`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	configPath := fs.String("config", "", "path to kudawiki.yaml (default "+config.DefaultPath+" if present)")
	dryRun := fs.Bool("dry-run", false, "migrate: only list what would be applied")
	wsSlug := fs.String("ws", "", "doctor: only this area")
	fix := fs.Bool("fix", false, "doctor: apply the automatic fixes")
	_ = fs.Parse(os.Args[2:])

	cfg, err := config.Load(*configPath)
	if err == nil {
		switch os.Args[1] {
		case "init":
			err = runInit(ctx, cfg, os.Getenv("KUDAWIKI_INIT_EMAIL"), os.Getenv("KUDAWIKI_INIT_NAME"))
		case "serve":
			err = runServe(ctx, cfg)
		case "healthcheck":
			err = runHealthcheck(cfg)
		case "search":
			err = runSearchRebuild(ctx, cfg)
		case "audit":
			err = runAuditVerify(ctx, cfg)
		case "migrate":
			err = runMigrateDryRun(ctx, cfg, *dryRun)
		case "backup":
			err = runBackup(ctx, cfg)
		case "restore":
			err = runRestore(cfg, fs.Arg(0))
		case "doctor":
			err = runDoctor(ctx, cfg, *wsSlug, *fix)
		case "admin":
			err = runAdmin(ctx, cfg, fs.Args())
		default:
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "kudawiki:", err)
		os.Exit(1)
	}
}

// runInit toma el correo y el nombre del entorno (deploy/.env); la contraseña nunca:
// se pide por consola para que no quede escrita en un archivo.
// Crea la llave primero (O_EXCL: falla si ya existe, así init no corre dos veces)
// y la borra si el resto falla, para poder reintentar (SPEC §13.6).
func runInit(ctx context.Context, cfg config.Config, email, name string) error {
	if email == "" || name == "" {
		return errors.New("init requires KUDAWIKI_INIT_EMAIL and KUDAWIKI_INIT_NAME (deploy/.env)")
	}
	password, err := readPassword()
	if err != nil {
		return err
	}
	if err := crypto.GenerateMasterKey(cfg.MasterKeyFile); err != nil {
		return err
	}
	if err := initDB(ctx, cfg, email, name, password); err != nil {
		os.Remove(cfg.MasterKeyFile)
		return consoleError(err, cfg)
	}
	if cfg.IsLocal() {
		fmt.Println("warning: base_url is localhost, so the password policy was relaxed (development only)")
	}
	fmt.Println("kudawiki initialized; back up the master key separately from the data:", cfg.MasterKeyFile)
	return nil
}

func initDB(ctx context.Context, cfg config.Config, email, name, password string) error {
	db, err := database.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		return err
	}
	// El alta del primer superadmin es de la consola, no de una persona en la wiki: no se audita.
	svc, err := auth.NewService(repo.New(db), audit.NewRecorder(repo.New(db), time.Now, slog.Default()), time.Now, cfg.IsLocal())
	if err != nil {
		return err
	}
	return svc.CreateSuperadmin(ctx, email, name, password)
}

// readPassword nunca acepta la contraseña por flag (quedaría en el historial de la shell).
func readPassword() (string, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	fmt.Fprint(os.Stderr, "Password: ")
	p1, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprint(os.Stderr, "\nRepeat password: ")
	p2, err2 := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err := errors.Join(err, err2); err != nil {
		return "", err
	}
	if string(p1) != string(p2) {
		return "", errors.New("passwords do not match")
	}
	return string(p1), nil
}

func runServe(ctx context.Context, cfg config.Config) error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// Sin llave no se arranca: sin ella la contraseña SMTP guardada y los sellados de F7 serían
	// ilegibles (SPEC §6.3).
	masterKey, err := crypto.LoadMasterKey(cfg.MasterKeyFile)
	if err != nil {
		return fmt.Errorf("%w (run `kudawiki init` first)", err)
	}
	db, err := database.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer db.Close()
	// Con migraciones pendientes, primero un respaldo de la base (SPEC §13.9): para volver atrás
	// se restaura. Una base de un binario más nuevo no se toca y el servicio no arranca.
	preMigrate, err := backupBeforeMigrate(ctx, db, cfg, logger)
	if err != nil {
		return err
	}
	if err := db.Migrate(ctx); err != nil {
		if preMigrate != "" {
			return fmt.Errorf("%w (nothing was left half-applied; the backup is %s)", err, preMigrate)
		}
		return err
	}
	store := repo.New(db)
	// Páginas sin indexar (las de antes de la búsqueda, o un índice dañado): se reconstruye al
	// arrancar, porque el índice es derivado (SPEC §9.7).
	if stale, err := store.IndexStale(ctx); err != nil {
		return err
	} else if stale {
		logger.Info("rebuilding search index")
		if err := store.RebuildIndex(ctx, wiki.IndexDoc); err != nil {
			return err
		}
	}
	rec := audit.NewRecorder(store, time.Now, logger)
	quizzes := quiz.NewService(store, rec, time.Now)
	if preMigrate != "" { // recién ahora: antes de migrar, la tabla de auditoría podía no existir
		rec.Record(ctx, audit.Event{Action: audit.BackupRun, TargetType: "backup", TargetID: filepath.Base(preMigrate), System: "pre_migrate",
			Meta: map[string]any{"kind": string(backup.PreMigrate)}})
	}
	svc, err := auth.NewService(store, rec, time.Now, cfg.IsLocal())
	if err != nil {
		return err
	}
	base, _ := url.Parse(cfg.BaseURL) // validada en config.Load
	files := media.NewService(store, rec, cfg.StorageDir(), cfg.Upload.MaxBytes)
	b := cfg.Branding
	brand, err := branding.NewService(store, rec, branding.Patch{Title: b.Title, Company: b.Company, ColorPrimary: b.ColorPrimary,
		ColorHeader: b.ColorHeader, ColorSidebar: b.ColorSidebar, LoginMessage: b.LoginMessage, ShowPoweredBy: b.ShowPoweredBy})
	if err != nil {
		return err
	}
	m := cfg.SMTP
	mailer, err := mail.NewService(store, brand, rec, masterKey, mail.Patch{Host: m.Host, Port: m.Port, Security: m.Security,
		Username: m.Username, Password: m.Password, From: m.From}, kudawiki.I18n(), cfg.BaseURL, time.Now)
	if err != nil {
		return err
	}
	svc.SetEmail(auth.Email{Mail: mailer, BaseURL: cfg.BaseURL, PasswordReset: cfg.Auth.Local.PasswordReset,
		ResetTTL: cfg.ResetTTL(), InviteTTL: cfg.InviteTTL(), Log: logger})

	// Los jobs terminan antes de cerrar la BD: este defer corre antes que el de db.Close.
	jobCtx, stopJobs := context.WithCancel(ctx)
	var jobsDone sync.WaitGroup
	jobsDone.Go(func() {
		jobs.Purge{Store: store, Files: files, Audit: rec, Retention: cfg.TrashRetention(), AuditRetention: cfg.AuditRetention(),
			Now: time.Now, Log: logger}.Daily(jobCtx)
	})
	jobsDone.Go(func() { mailer.Run(jobCtx, logger) })
	pages := wiki.NewService(store, rec)
	props := proposals.NewService(store, pages, rec)
	notes := notify.NewService(store, mailer, props, masterKey, cfg.BaseURL, cfg.EmailDelay(), cfg.RemindAfter(), time.Now)
	jobsDone.Go(func() { notes.Run(jobCtx, logger) })
	hour, minute := cfg.BackupAt()
	jobsDone.Go(func() {
		jobs.Backup{DB: db.Read, StorageDir: cfg.StorageDir(), Dir: cfg.BackupDir(), Hour: hour, Minute: minute, Audit: rec, Now: time.Now, Log: logger,
			Keep: backup.Retention{Daily: cfg.Backup.KeepDaily, Weekly: cfg.Backup.KeepWeekly, Monthly: cfg.Backup.KeepMonthly}}.Daily(jobCtx)
	})
	defer func() { stopJobs(); jobsDone.Wait() }()

	editing := presence.NewHub()
	srv := &http.Server{
		Addr: cfg.Listen,
		Handler: httpapi.New(httpapi.Deps{Auth: svc, Workspaces: workspace.NewService(store, rec), Wiki: pages, Proposals: props, Notify: notes, Drafts: store, Presence: editing,
			Personal: personal.NewService(store, pages, props, files, rec), Reading: reading.NewService(store, quizzes, rec, time.Now), Quizzes: quizzes,
			RemindAfter: cfg.RemindAfter(), Media: files, Branding: brand, Mail: mailer, Read: store, DB: db, UI: kudawiki.UI(), BaseURL: base, TrashRetention: cfg.TrashRetention(), StorageDir: cfg.StorageDir(), Log: logger}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	// Shutdown espera a que terminen las peticiones; los avisos de edición abiertos no terminan
	// solos (§4.11).
	srv.RegisterOnShutdown(editing.Close)
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	logger.Info("listening", "addr", cfg.Listen, "base_url", cfg.BaseURL)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}

// runSearchRebuild regenera el índice de búsqueda (SPEC §9.7).
func runSearchRebuild(ctx context.Context, cfg config.Config) error {
	if len(os.Args) < 3 || os.Args[2] != "rebuild" {
		return errors.New(`usage: kudawiki search rebuild`)
	}
	db, err := database.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		return err
	}
	return repo.New(db).RebuildIndex(ctx, wiki.IndexDoc)
}

// runAuditVerify recorre la cadena de la auditoría (SPEC §11.1). Abre la BD sin migrar: es
// una revisión, y tiene que poder correr sobre una copia restaurada sin tocarla.
func runAuditVerify(ctx context.Context, cfg config.Config) error {
	if len(os.Args) < 3 || os.Args[2] != "verify" {
		return errors.New(`usage: kudawiki audit verify`)
	}
	db, err := database.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer db.Close()
	rep, err := audit.Verify(ctx, repo.New(db))
	if err != nil {
		return err
	}
	if rep.Events == 0 {
		fmt.Println("audit chain ok: no events yet")
		return nil
	}
	fmt.Printf("audit chain ok: %d events (ids %d to %d)\n", rep.Events, rep.FirstID, rep.LastID)
	if rep.FirstID != 1 {
		fmt.Printf("events before %d were removed by the retention policy; the chain is anchored at %d\n", rep.FirstID, rep.FirstID)
	}
	return nil
}

// runHealthcheck existe porque la imagen distroless no tiene shell ni curl (SPEC §13.11).
func runHealthcheck(cfg config.Config) error {
	_, port, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		return err
	}
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz returned %d", resp.StatusCode)
	}
	return nil
}

// runMigrateDryRun lista las migraciones pendientes sin tocar nada (SPEC §13.9). Aplicarlas es
// cosa de `serve`, que antes hace el respaldo: no hay otro camino para migrar.
func runMigrateDryRun(ctx context.Context, cfg config.Config, dryRun bool) error {
	if !dryRun {
		return errors.New("migrations are applied by `kudawiki serve`, after a backup; use `kudawiki migrate --dry-run` to list them")
	}
	if _, err := os.Stat(cfg.DBPath()); err != nil {
		return fmt.Errorf("no database at %s (run `kudawiki init` first)", cfg.DBPath())
	}
	db, err := database.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer db.Close()
	pending, err := db.Pending(ctx)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		fmt.Println("no pending migrations")
		return nil
	}
	fmt.Printf("%d pending migrations (a backup is made before applying them):\n", len(pending))
	for _, name := range pending {
		fmt.Println("  " + name)
	}
	return nil
}

// backupBeforeMigrate respalda la base si hay migraciones pendientes y ya tiene datos (SPEC
// §13.9). Solo la base: migrar no toca storage/. Devuelve la ruta del respaldo, o "" si no hizo
// falta.
func backupBeforeMigrate(ctx context.Context, db *database.DB, cfg config.Config, logger *slog.Logger) (string, error) {
	pending, err := db.Pending(ctx)
	if err != nil || len(pending) == 0 {
		return "", err
	}
	if v, err := database.Version(ctx, db.Read); err != nil || v == 0 {
		return "", err // base nueva: no hay nada que respaldar
	}
	path, err := backup.Create(ctx, db.Read, "", cfg.BackupDir(), backup.PreMigrate, time.Now())
	if err != nil {
		return "", fmt.Errorf("backup before migrating: %w", err)
	}
	logger.Info("backup before migrating", "file", path, "migrations", len(pending))
	return path, nil
}

// runBackup hace un respaldo ahora (SPEC §13.3) y lo verifica. La MASTER_KEY no va adentro.
func runBackup(ctx context.Context, cfg config.Config) error {
	if _, err := os.Stat(cfg.DBPath()); err != nil {
		return fmt.Errorf("no database at %s (run `kudawiki init` first)", cfg.DBPath())
	}
	db, err := database.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer db.Close()
	path, err := backup.Create(ctx, db.Read, cfg.StorageDir(), cfg.BackupDir(), backup.Manual, time.Now())
	if err != nil {
		return err
	}
	m, err := backup.Verify(path)
	if err != nil {
		return fmt.Errorf("the backup was written but does not verify: %w", err)
	}
	// Con el esquema al día queda en la auditoría; con migraciones pendientes la tabla puede no existir.
	if pending, err := db.Pending(ctx); err == nil && len(pending) == 0 {
		rec := audit.NewRecorder(repo.New(db), time.Now, slog.New(slog.NewTextHandler(os.Stderr, nil)))
		rec.Record(ctx, audit.Event{Action: audit.BackupRun, TargetType: "backup", TargetID: filepath.Base(path), System: "cli",
			Meta: map[string]any{"kind": string(backup.Manual)}})
	}
	fmt.Printf("backup ok: %s (%d files, schema %d)\n", path, len(m.Files), m.Schema)
	fmt.Println("the master key is not in the backup: keep a copy of it separately")
	return nil
}

// runRestore pone un respaldo en lugar de la base y de storage/ (SPEC §13.3). Lo que había
// queda aparte en before-restore-<hora>. Con el servidor corriendo, no: la base cambiaría bajo
// sus pies.
func runRestore(cfg config.Config, file string) error {
	if file == "" {
		return errors.New("usage: kudawiki restore <file>")
	}
	if used, err := backup.InUse(cfg.DBPath()); err != nil {
		return err
	} else if used {
		return errors.New("the database is in use: stop `kudawiki serve` (or the container) before restoring")
	}
	m, previous, err := backup.Restore(file, cfg.DBPath(), cfg.StorageDir(), database.Latest())
	if err != nil {
		return err
	}
	fmt.Printf("restored %s backup from %s (%d files, schema %d)\n", m.Kind, m.CreatedAt.Format(time.RFC3339), len(m.Files), m.Schema)
	if previous != "" {
		fmt.Printf("what was there before is in %s\n", previous)
	}
	if m.Schema < database.Latest() {
		fmt.Println("the backup has an older schema: the next `kudawiki serve` will migrate it (after its own backup)")
	}
	fmt.Println("check it with: kudawiki audit verify")
	return nil
}

// runDoctor revisa la salud del contenido (SPEC §13.7), con el SHA-256 de cada archivo. Con
// --fix aplica los dos arreglos automáticos: reconstruir el índice y borrar los archivos que no
// tienen fila. Para eso el servidor tiene que estar detenido: un archivo recién subido, antes
// de que se guarde su fila, parecería sobrante.
func runDoctor(ctx context.Context, cfg config.Config, wsSlug string, fix bool) error {
	if fix {
		if used, err := backup.InUse(cfg.DBPath()); err != nil {
			return err
		} else if used {
			return errors.New("--fix needs the server stopped; without --fix, doctor only reads and can run anytime")
		}
	}
	db, err := database.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer db.Close()
	if pending, err := db.Pending(ctx); err != nil {
		return err
	} else if len(pending) > 0 {
		return errors.New("the database has pending migrations: start the server once to apply them")
	}
	store := repo.New(db)
	areas, err := store.ListWorkspaces(ctx)
	if err != nil {
		return err
	}
	if wsSlug != "" {
		areas = slices.DeleteFunc(areas, func(w repo.Workspace) bool { return w.Slug != wsSlug })
		if len(areas) == 0 {
			return fmt.Errorf("no area %q", wsSlug)
		}
	}
	opts := health.Options{StorageDir: cfg.StorageDir(), Areas: areas, Global: wsSlug == "", Hash: true, Now: time.Now(), RemindAfter: cfg.RemindAfter()}
	problems, err := health.Check(ctx, store, opts)
	if err != nil {
		return err
	}
	if fix && len(problems) > 0 {
		if err := doctorFix(ctx, store, cfg, areas, problems); err != nil {
			return err
		}
		if problems, err = health.Check(ctx, store, opts); err != nil {
			return err
		}
	}
	errs := 0
	for _, p := range problems {
		if p.Severity == health.Error {
			errs++
		}
		fmt.Printf("%-7s  %-26s  %-12s  %s  %s\n", p.Severity, p.Check, p.Area, p.Page, p.Detail)
	}
	fmt.Printf("%d areas checked: %d errors, %d warnings\n", len(areas), errs, len(problems)-errs)
	if errs > 0 {
		return errors.New("the content has errors")
	}
	return nil
}

// runAdmin es la recuperación por consola (SPEC §13.6). Escribe en la BD con el servidor
// corriendo (SQLite serializa a los dos escritores) y queda en la auditoría como el sistema
// "cli". El aviso a los superadmins llega con las notificaciones (F4).
func runAdmin(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) < 2 {
		return errUsageAdmin
	}
	if _, err := os.Stat(cfg.DBPath()); err != nil {
		return fmt.Errorf("no database at %s (run `kudawiki init` first)", cfg.DBPath())
	}
	db, err := database.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer db.Close()
	if pending, err := db.Pending(ctx); err != nil {
		return err
	} else if len(pending) > 0 {
		return errors.New("the database has pending migrations: start the server once to apply them")
	}
	store := repo.New(db)
	svc, err := auth.NewService(store, audit.NewRecorder(store, time.Now, slog.New(slog.NewTextHandler(os.Stderr, nil))), time.Now, cfg.IsLocal())
	if err != nil {
		return err
	}
	msg, err := adminCommand(ctx, svc, args)
	if err != nil {
		return consoleError(err, cfg)
	}
	fmt.Println(msg)
	fmt.Println("recorded in the audit log")
	return nil
}

var errUsageAdmin = errors.New("usage: kudawiki admin unlock <email> | set-password <email> | create-superadmin <email> <name> | revoke-sessions --all | <email>")

// adminCommand corre un subcomando de `kudawiki admin` y devuelve qué decirle a quien opera.
func adminCommand(ctx context.Context, svc *auth.Service, args []string) (string, error) {
	cmd, email := args[0], args[1]
	switch {
	case cmd == "unlock" && len(args) == 2:
		return email + " can log in again", svc.UnlockLogin(ctx, email)
	case cmd == "set-password" && len(args) == 2:
		password, err := readPassword()
		if err != nil {
			return "", err
		}
		return "password set for " + email + "; their sessions were closed", svc.SetPassword(ctx, email, password)
	case cmd == "create-superadmin" && len(args) >= 3:
		password, err := readPassword()
		if err != nil {
			return "", err
		}
		return "superadmin created: " + email, svc.CreateEmergencySuperadmin(ctx, email, strings.Join(args[2:], " "), password)
	case cmd == "revoke-sessions" && len(args) == 2:
		if email == "--all" {
			email = ""
		}
		n, err := svc.RevokeSessions(ctx, email)
		return fmt.Sprintf("sessions closed: %d", n), err
	}
	return "", errUsageAdmin
}

// consoleError traduce los errores de dominio a un mensaje legible: el código del error no le
// dice nada a quien instala u opera.
func consoleError(err error, cfg config.Config) error {
	switch {
	case errors.Is(err, repo.ErrNotFound):
		return errors.New("there is no person with that email")
	case errors.Is(err, auth.ErrEmailTaken):
		return errors.New("that email already has an account: use `kudawiki admin set-password`")
	case errors.Is(err, auth.ErrInvalidUser):
		return errors.New("a valid email and a name are required")
	case errors.Is(err, auth.ErrPasswordTooShort) && cfg.IsLocal():
		return errors.New("password rejected: it cannot be empty")
	case errors.Is(err, auth.ErrPasswordTooShort):
		return fmt.Errorf("password rejected: it must have at least %d characters", auth.MinPasswordLength)
	case errors.Is(err, auth.ErrPasswordCommon):
		return errors.New("password rejected: it is in the list of common passwords, choose another")
	}
	return err
}

// doctorFix aplica los arreglos automáticos de §13.7 y deja lo demás para una persona.
func doctorFix(ctx context.Context, store *repo.Repo, cfg config.Config, areas []repo.Workspace, problems []health.Problem) error {
	wsID := make(map[string]string, len(areas))
	for _, w := range areas {
		wsID[w.Slug] = w.ID
	}
	reindex := false
	for _, p := range problems {
		switch {
		case strings.HasPrefix(p.Check, "search."):
			reindex = true
		case p.Check == "media.stray_file":
			if err := os.Remove(filepath.Join(cfg.StorageDir(), "ws_"+wsID[p.Area], p.Detail)); err != nil {
				return err
			}
			fmt.Println("fixed: removed stray file", p.Area, p.Detail)
		}
	}
	if reindex {
		if err := store.RebuildIndex(ctx, wiki.IndexDoc); err != nil {
			return err
		}
		fmt.Println("fixed: search index rebuilt")
	}
	return nil
}
