// Comando kudawiki: único punto donde se arman las dependencias (SPEC §16.2).
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
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
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/athomo001/kudawiki"
	"github.com/athomo001/kudawiki/internal/accessreview"
	"github.com/athomo001/kudawiki/internal/areaio"
	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/auth"
	"github.com/athomo001/kudawiki/internal/backup"
	"github.com/athomo001/kudawiki/internal/backups"
	"github.com/athomo001/kudawiki/internal/branding"
	"github.com/athomo001/kudawiki/internal/config"
	"github.com/athomo001/kudawiki/internal/crypto"
	"github.com/athomo001/kudawiki/internal/database"
	"github.com/athomo001/kudawiki/internal/health"
	"github.com/athomo001/kudawiki/internal/httpapi"
	"github.com/athomo001/kudawiki/internal/jobs"
	"github.com/athomo001/kudawiki/internal/ldap"
	"github.com/athomo001/kudawiki/internal/mail"
	"github.com/athomo001/kudawiki/internal/masterkey"
	"github.com/athomo001/kudawiki/internal/media"
	"github.com/athomo001/kudawiki/internal/metrics"
	"github.com/athomo001/kudawiki/internal/notify"
	"github.com/athomo001/kudawiki/internal/offboarding"
	"github.com/athomo001/kudawiki/internal/personal"
	"github.com/athomo001/kudawiki/internal/presence"
	"github.com/athomo001/kudawiki/internal/proposals"
	"github.com/athomo001/kudawiki/internal/quiz"
	"github.com/athomo001/kudawiki/internal/reading"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/athomo001/kudawiki/internal/sealed"
	"github.com/athomo001/kudawiki/internal/shares"
	"github.com/athomo001/kudawiki/internal/transfer"
	"github.com/athomo001/kudawiki/internal/wiki"
	"github.com/athomo001/kudawiki/internal/workspace"
	"golang.org/x/term"
)

// version la graba el build con -ldflags "-X main.version=1.2.3" (Dockerfile, release.yml) a partir
// de la etiqueta del release (§13.9): así quien opera sabe qué corre sin mirar la imagen.
var version = "dev"

const usage = `usage: kudawiki <command> [flags]

commands:
  version      print the version of this binary
  init         create the database, the master key and the first superadmin
               (KUDAWIKI_INIT_EMAIL, KUDAWIKI_INIT_NAME; password is prompted)
  serve        run the web server; outside localhost base_url must be https:// unless
               --insecure-http is given (it warns at every start)
  healthcheck  exit 0 if the local server answers /healthz (Docker HEALTHCHECK)
  search       "search rebuild": regenerate the search index of every page
  audit        "audit verify": check the hash chain of the audit log (exit 1 if broken)
               "audit export --from YYYY-MM-DD --to YYYY-MM-DD [--out file.zip]": evidence package
               for an auditor: the events, the chain check, and a manifest signed with a key
               derived from the master key (exit 1 if the chain is broken)
  migrate      "migrate --dry-run": list the pending migrations without applying them
               (serve applies them, after a backup)
  backup       make a backup now into <data_dir>/backups (the master key is not included)
               "backup verify <file>": test-restore a backup (also .enc) in a temporary folder:
               manifest, SQLite integrity_check and the audit chain; recorded for the panel
               "backup keygen": create the backup key (backup.key_file) that encrypts the
               offsite copy, if the installation is older than it
  restore      "restore <file>": verify a backup and put it in place; the server must be stopped
  masterkey    "masterkey rotate": replace the master key and re-encrypt what it protects, with the
               server stopped and a backup first; if it is cut, run it again. The old key stays
               as <key file>.v<N>: the older backups need it
               "masterkey split --shares N --threshold K": print the key in N shares; any K rebuild it
               "masterkey combine": read shares from stdin, check them against the database and
               write the key file if it is missing
  ldap         "ldap sync [--dry-run]": sync accounts and area memberships with the directory now
               (--dry-run lists what it would do). Exit 1 if a safeguard aborted it
  import       "import wikijs2 --map <mapping.yaml> --as <email> (--dir <folder> | --db <dsn>)":
               bring a Wiki.js 2.x site into the areas of the mapping, from its disk/Git storage
               folder or its PostgreSQL/MySQL database (with history and assets). --dry-run first
               is required: the real run must match the last report
               "import markdown --area <slug> --as <email> <folder | file.zip>": a Markdown folder
               --as is an active superadmin; pages that already exist are never overwritten
  privacy      "privacy export <email> [--out file.zip]": what the wiki holds about a person
               (profile, memberships, sessions, notifications, quiz results, their audit events
               and their personal space); never sealed documents or secrets. Recorded in the audit
  doctor       check the content health (broken links, tree, media, search index);
               --ws <slug> for one area, --fix to rebuild the index and remove stray files
               (with the server stopped). Exit 1 if there are errors
               "doctor --security": check the installation instead: file permissions, where the
               master key lives, TLS, admins without 2FA, a local superadmin for emergencies and
               recent tested backups. Exit 1 if there are errors
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
	// Antes de leer la configuración: la versión se consulta también sin instalación.
	if os.Args[1] == "version" {
		fmt.Println(version)
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	configPath := fs.String("config", "", "path to kudawiki.yaml (default "+config.DefaultPath+" if present)")
	dryRun := fs.Bool("dry-run", false, "migrate, ldap sync, import: only list what would be applied")
	wsSlug := fs.String("ws", "", "doctor: only this area")
	fix := fs.Bool("fix", false, "doctor: apply the automatic fixes")
	security := fs.Bool("security", false, "doctor: check the installation hardening instead of the content")
	insecureHTTP := fs.Bool("insecure-http", false, "serve: allow a plain http:// base_url outside localhost")
	from := fs.String("from", "", "audit export: first day (YYYY-MM-DD)")
	to := fs.String("to", "", "audit export: last day (YYYY-MM-DD)")
	out := fs.String("out", "", "audit export, privacy export: file to write")
	positional := parseInterleaved(fs, os.Args[2:])

	cfg, err := config.Load(*configPath)
	if err == nil {
		switch os.Args[1] {
		case "init":
			err = runInit(ctx, cfg, os.Getenv("KUDAWIKI_INIT_EMAIL"), os.Getenv("KUDAWIKI_INIT_NAME"))
		case "serve":
			err = runServe(ctx, cfg, *insecureHTTP)
		case "healthcheck":
			err = runHealthcheck(cfg)
		case "search":
			err = runSearchRebuild(ctx, cfg)
		case "audit":
			err = runAudit(ctx, cfg, positional, *from, *to, *out)
		case "migrate":
			err = runMigrateDryRun(ctx, cfg, *dryRun)
		case "backup":
			err = runBackupCommand(ctx, cfg, positional)
		case "restore":
			err = runRestore(cfg, arg(positional, 0))
		case "masterkey":
			err = runMasterKey(ctx, cfg, positional)
		case "doctor":
			if *security {
				err = runDoctorSecurity(ctx, cfg)
			} else {
				err = runDoctor(ctx, cfg, *wsSlug, *fix)
			}
		case "admin":
			err = runAdmin(ctx, cfg, positional)
		case "ldap":
			err = runLDAPSync(ctx, cfg, positional, *dryRun)
		case "import":
			err = runImport(ctx, cfg, positional, *dryRun)
		case "privacy":
			err = runPrivacy(ctx, cfg, positional, *out)
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

// parseInterleaved lee los flags estén antes o después de los subcomandos: el paquete flag
// para en el primer argumento que no es flag, y `kudawiki ldap sync --dry-run` lo dejaría sin leer.
// Los flags que este FlagSet no conoce quedan como argumentos: son de un subcomando, que los lee
// con el suyo (`masterkey split --shares`, `admin revoke-sessions --all`).
func parseInterleaved(fs *flag.FlagSet, args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		name, _, hasValue := strings.Cut(strings.TrimLeft(args[i], "-"), "=")
		f := fs.Lookup(name)
		if !strings.HasPrefix(args[i], "-") || f == nil {
			positional = append(positional, args[i])
			continue
		}
		flags = append(flags, args[i])
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); !hasValue && !(ok && b.IsBoolFlag()) && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	_ = fs.Parse(flags) // ExitOnError: un valor inválido ya terminó el proceso
	return positional
}

func arg(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return ""
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
	// La llave de respaldo (§13.3): aparte de la MASTER_KEY, para la copia que sale del servidor.
	if err := backup.GenerateKey(cfg.Backup.KeyFile); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	if cfg.IsLocal() {
		fmt.Println("warning: base_url is localhost, so the password policy was relaxed (development only)")
	}
	fmt.Println("kudawiki initialized; back up the master key separately from the data:", cfg.MasterKeyFile)
	fmt.Println("and the backup key, without which the offsite copies cannot be read:", cfg.Backup.KeyFile)
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

func runServe(ctx context.Context, cfg config.Config, insecureHTTP bool) error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// /metrics solo para las redes configuradas (§13.5); sin ninguna, no responde a nadie.
	var collector *metrics.Collector
	if len(cfg.Metrics.AllowCIDRs) > 0 {
		var err error
		if collector, err = metrics.New(cfg.Metrics.AllowCIDRs); err != nil {
			return err
		}
	}

	// TLS obligatorio fuera de localhost (§21.2.1): antes de tocar nada.
	warning, err := cfg.CheckTransport(insecureHTTP)
	if err != nil {
		return err
	}
	if warning != "" {
		logger.Warn(warning, "base_url", cfg.BaseURL)
	}

	// Sin llave no se arranca: sin ella la contraseña SMTP guardada y los sellados serían
	// ilegibles (SPEC §6.3). Se mira antes de migrar; su versión, después (masterkey.Load).
	if _, err = crypto.LoadMasterKey(cfg.MasterKeyFile); err != nil {
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
	masterKey, err := masterkey.Load(ctx, store, cfg.MasterKeyFile)
	if err != nil {
		return err
	}
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
	areas := workspace.NewService(store, rec, logger)
	// La baja de usuarios (§8.4): la usan el panel, el job diario y la sincronización del directorio.
	leaving := offboarding.NewService(store, files, rec, masterKey.Key,
		offboarding.Config{Grace: cfg.OffboardingGrace(), PseudonymizeAfter: cfg.PseudonymizeAfter()}, time.Now, logger)
	directory, err := newDirectory(cfg, store, rec, masterKey, directoryDeps{areas: areas, leaving: leaving, mailer: mailer, log: logger})
	if err != nil {
		return err
	}
	directory.LogInsecure(ctx)
	svc.SetDirectory(directory)
	svc.SetSecondFactor(auth.SecondFactor{Key: masterKey, Enforce: auth.MFAPolicy(cfg.Auth.TOTP.EnforceFor), BaseURL: cfg.BaseURL})
	if !svc.PasskeysAvailable() {
		logger.Warn("passkeys disabled: base_url needs https and a domain name (or http://localhost)", "base_url", cfg.BaseURL)
	}
	svc.SetEmail(auth.Email{Mail: mailer, BaseURL: cfg.BaseURL, PasswordReset: cfg.Auth.Local.PasswordReset,
		ResetTTL: cfg.ResetTTL(), InviteTTL: cfg.InviteTTL(), Log: logger})

	// Respaldos (§13.3, F8-B): el job diario lee su configuración del panel en cada vuelta; lo que
	// fija el archivo gana. Un destino externo del archivo se valida al arrancar, como antes: la
	// llave de respaldo es obligatoria para subir.
	fixed, err := backupFixed(cfg)
	if err != nil {
		return err
	}
	backupsSvc := backups.NewService(store, rec, masterKey, backups.Options{Fixed: fixed, DB: db.Read, StorageDir: cfg.StorageDir(),
		Dir: cfg.BackupDir(), KeyFile: cfg.Backup.KeyFile, Notify: backupNotice(store, mailer, cfg.BaseURL, logger), Now: time.Now, Log: logger})
	// La copia de la auditoría fuera de la wiki (§11.2).
	sink, err := auditSink(cfg)
	if err != nil {
		return err
	}
	// Los jobs terminan antes de cerrar la BD: este defer corre antes que el de db.Close.
	jobCtx, stopJobs := context.WithCancel(ctx)
	var jobsDone sync.WaitGroup
	jobsDone.Go(func() {
		jobs.Purge{Store: store, Files: files, Audit: rec, Retention: cfg.TrashRetention(), AuditRetention: cfg.AuditRetention(),
			QuizRetention: cfg.QuizRetention(),
			Now:           time.Now, Log: logger}.Daily(jobCtx)
	})
	jobsDone.Go(func() { mailer.Run(jobCtx, logger) })
	pages := wiki.NewService(store, rec)
	props := proposals.NewService(store, pages, rec)
	notes := notify.NewService(store, mailer, props, masterKey.Key, cfg.BaseURL, cfg.EmailDelay(), cfg.RemindAfter(), time.Now)
	jobsDone.Go(func() { notes.Run(jobCtx, logger) })
	// Completa las bajas que vencieron y seudonimiza la auditoría (§8.4, §13.10).
	jobsDone.Go(func() { leaving.Daily(jobCtx) })
	jobsDone.Go(func() { directory.Run(jobCtx) })
	jobsDone.Go(func() { backupsSvc.Daily(jobCtx) })
	access := accessreview.NewService(store, rec, mailer, cfg.AccessReviewEvery(), cfg.BaseURL, time.Now)
	jobsDone.Go(func() { access.Run(jobCtx, logger) })
	lifecycle := workspace.NewLifecycle(store, rec, mailer, cfg.StorageDir(), cfg.DeleteGrace(), time.Now, logger)
	jobsDone.Go(func() { lifecycle.Run(jobCtx) })
	if sink != nil {
		jobsDone.Go(func() {
			audit.Forwarder{Store: store, Sink: sink, Interval: 2 * time.Second, Log: logger}.Run(jobCtx)
		})
	}
	defer func() { stopJobs(); jobsDone.Wait() }()

	editing := presence.NewHub()
	srv := &http.Server{
		Addr: cfg.Listen,
		Handler: httpapi.New(httpapi.Deps{Auth: svc, Workspaces: areas, Wiki: pages, Proposals: props, Notify: notes, Drafts: store, Presence: editing,
			Shares: shares.NewService(store, rec, logger), AccessReview: access, Lifecycle: lifecycle,
			Transfer: transfer.NewService(store, files, rec, logger),
			Metrics:  collector,
			// 64 MiB alcanzan para miles de páginas; lo que no entra se vuelve a renderizar.
			RenderCache: wiki.NewRenderCache(64 << 20),
			Clock:       &health.Clock{Server: cfg.Time.NTPServer, Now: time.Now},
			AreaIO:      areaio.NewService(store, pages, files, rec, time.Now),
			Backups:     backupsSvc,
			Sealed:      sealed.NewService(store, files, rec, masterKey, logger, time.Now),
			Personal:    personal.NewService(store, pages, props, files, rec), Reading: reading.NewService(store, quizzes, rec, time.Now), Quizzes: quizzes, Offboarding: leaving,
			RemindAfter: cfg.RemindAfter(), TrustProxy: cfg.TrustProxy, Audit: rec, Media: files, Branding: brand, Mail: mailer, LDAP: directory, Read: store, DB: db, UI: kudawiki.UI(), BaseURL: base, TrashRetention: cfg.TrashRetention(), StorageDir: cfg.StorageDir(), Log: logger}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		// TLS 1.2 como mínimo; Go prefiere 1.3 cuando el navegador lo ofrece (§21.2.1).
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}
	// Shutdown espera a que terminen las peticiones; los avisos de edición abiertos no terminan
	// solos (§4.11).
	srv.RegisterOnShutdown(editing.Close)
	errc := make(chan error, 1)
	go func() {
		if cfg.TLS.CertFile != "" {
			errc <- srv.ListenAndServeTLS(cfg.TLS.CertFile, cfg.TLS.KeyFile)
			return
		}
		errc <- srv.ListenAndServe()
	}()
	logger.Info("listening", "version", version, "addr", cfg.Listen, "base_url", cfg.BaseURL)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}

// backupFixed es la configuración de respaldos que trae el archivo (§13.3): lo que fija gana
// sobre el panel. Un destino externo completo en el archivo se valida al arrancar y exige la llave
// de respaldo, como antes de F8-B.
func backupFixed(cfg config.Config) (backups.Fixed, error) {
	b := cfg.Backup
	f := backups.Fixed{Set: b.Set}
	f.Time, f.KeepDaily, f.KeepWeekly, f.KeepMonthly = b.Time, b.KeepDaily, b.KeepWeekly, b.KeepMonthly
	f.Remote = backups.Remote{Endpoint: b.Remote.Endpoint, Region: b.Remote.Region, Bucket: b.Remote.Bucket, Prefix: b.Remote.Prefix,
		AccessKeyID: b.Remote.AccessKeyID, PathStyle: b.Remote.PathStyle}
	if b.Remote.SecretAccessKeyFile != "" {
		secret, err := cfg.BackupSecret()
		if err != nil {
			return f, err
		}
		f.SecretFile = secret
	}
	if b.Remote.Endpoint != "" {
		if _, err := backup.LoadKey(b.KeyFile); err != nil {
			return f, err
		}
		if _, err := backup.NewS3(b.Remote.Endpoint, b.Remote.Region, b.Remote.Bucket, b.Remote.Prefix, b.Remote.AccessKeyID,
			f.SecretFile, b.Remote.PathStyle); err != nil {
			return f, err
		}
	}
	return f, nil
}

// backupNotice avisa por correo a los superadmins que un paso del respaldo falló (§21.2.4).
func backupNotice(store *repo.Repo, mailer *mail.Service, baseURL string, logger *slog.Logger) func(ctx context.Context, step, detail string) {
	return func(ctx context.Context, step, detail string) {
		if on, err := mailer.Enabled(ctx); err != nil || !on {
			return
		}
		users, err := store.ListUsers(ctx)
		if err != nil {
			logger.Error("backup notice", "err", err)
			return
		}
		for _, u := range users {
			if !u.IsGlobalAdmin || !u.IsActive {
				continue
			}
			if err := mailer.Enqueue(ctx, mail.Message{To: u.Email, Lang: u.Locale, Template: "backup_failed",
				URL: strings.TrimSuffix(baseURL, "/") + "/admin/health", Vars: map[string]string{"name": u.FullName},
				Lines: []mail.Line{{Template: step, Vars: map[string]string{"detail": detail}}}}); err != nil {
				logger.Error("backup notice", "err", err)
			}
		}
	}
}

// auditSink arma el destino de la copia de la auditoría (§11.2), o nil si no hay.
func auditSink(cfg config.Config) (audit.Sink, error) {
	switch cfg.Audit.Forward {
	case "stdout":
		return audit.StdoutSink{W: os.Stdout}, nil
	case "syslog":
		return audit.NewSyslogSink(cfg.Audit.Syslog.Address, cfg.Audit.Syslog.CAFile)
	}
	return nil, nil
}

// newDirectory arma el servicio del directorio con la configuración del archivo y lo que la
// sincronización necesita: la baja de §8.4 y el aviso a los superadmins (§8.3.4). Lo usan serve
// y `kudawiki ldap sync`.
func newDirectory(cfg config.Config, store *repo.Repo, rec *audit.Recorder, masterKey crypto.MasterKey, deps directoryDeps) (*ldap.Service, error) {
	areas, leaving, mailer, logger := deps.areas, deps.leaving, deps.mailer, deps.log
	l := cfg.Auth.LDAP
	bindPassword, err := cfg.LDAPBindPassword()
	if err != nil {
		return nil, err
	}
	directory, err := ldap.NewService(store, areas, rec, masterKey, ldap.Patch{Enabled: l.Enabled, Flavor: l.Flavor, URLs: l.URLs,
		StartTLS: l.StartTLS, CAFile: l.CAFile, InsecureSkipVerify: l.InsecureSkipVerify, BindDN: l.BindDN, BindPassword: bindPassword,
		DomainNetbios: l.DomainNetbios, UserBaseDN: l.UserBaseDN, UserFilter: l.UserFilter, LoginAttrs: l.LoginAttrs, GroupBaseDN: l.GroupBaseDN,
		Attrs: &ldap.AttrsPatch{Email: l.Attrs.Email, Name: l.Attrs.Name, GUID: l.Attrs.GUID, Photo: l.Attrs.Photo}, DisabledFilter: l.DisabledFilter,
		GroupMemberFilter: l.GroupMemberFilter, Timeout: l.Timeout, PasswordHelpURL: l.PasswordHelpURL, PrecreateUsers: l.PrecreateUsers,
		SyncInterval: l.Sync.Interval, SyncMaxRemovalRatio: l.Sync.MaxRemovalRatio},
		time.Now, logger)
	if err != nil {
		return nil, err
	}
	directory.SetOffboarding(leaving)
	directory.SetMail(mailer, cfg.BaseURL)
	return directory, nil
}

type directoryDeps struct {
	areas   *workspace.Service
	leaving *offboarding.Service
	mailer  *mail.Service
	log     *slog.Logger
}

// runLDAPSync sincroniza con el directorio desde la consola (§8.3.3), con el servidor corriendo
// o no: SQLite serializa a los dos escritores. Con --dry-run solo muestra lo que haría. Las
// salvaguardas (§8.3.4) no se pueden forzar desde acá: forzar pide reautenticarse en el panel.
func runLDAPSync(ctx context.Context, cfg config.Config, args []string, dryRun bool) error {
	if len(args) != 1 || args[0] != "sync" {
		return errors.New("usage: kudawiki ldap sync [--dry-run]")
	}
	// #nosec G703 -- ruta que da quien opera la CLI, no un usuario de la wiki
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
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	store := repo.New(db)
	masterKey, err := masterkey.Load(ctx, store, cfg.MasterKeyFile)
	if err != nil {
		return err
	}
	rec := audit.NewRecorder(store, time.Now, logger)
	b := cfg.Branding
	brand, err := branding.NewService(store, rec, branding.Patch{Title: b.Title, Company: b.Company, ColorPrimary: b.ColorPrimary,
		ColorHeader: b.ColorHeader, ColorSidebar: b.ColorSidebar, LoginMessage: b.LoginMessage, ShowPoweredBy: b.ShowPoweredBy})
	if err != nil {
		return err
	}
	m := cfg.SMTP
	// El correo queda en la cola; lo envía el servidor.
	mailer, err := mail.NewService(store, brand, rec, masterKey, mail.Patch{Host: m.Host, Port: m.Port, Security: m.Security,
		Username: m.Username, Password: m.Password, From: m.From}, kudawiki.I18n(), cfg.BaseURL, time.Now)
	if err != nil {
		return err
	}
	files := media.NewService(store, rec, cfg.StorageDir(), cfg.Upload.MaxBytes)
	leaving := offboarding.NewService(store, files, rec, masterKey.Key,
		offboarding.Config{Grace: cfg.OffboardingGrace(), PseudonymizeAfter: cfg.PseudonymizeAfter()}, time.Now, logger)
	directory, err := newDirectory(cfg, store, rec, masterKey,
		directoryDeps{areas: workspace.NewService(store, rec, logger), leaving: leaving, mailer: mailer, log: logger})
	if err != nil {
		return err
	}
	rep, err := directory.Sync(ctx, ldap.SyncOptions{Trigger: "cli", DryRun: dryRun})
	if err != nil {
		return err
	}
	printSyncReport(rep)
	if rep.Aborted != "" {
		return errors.New("the sync was aborted and nothing was applied; review it in the panel, where it can be forced")
	}
	return nil
}

func printSyncReport(r ldap.SyncReport) {
	if r.DryRun {
		fmt.Println("dry run: nothing was applied")
	}
	fmt.Printf("groups: %d, people in mapped groups: %d\n", r.Groups, r.People)
	fmt.Printf("accounts created: %d, updated: %d\n", r.Created, r.Updated)
	fmt.Printf("memberships added: %d, changed: %d, removed: %d of %d (%.0f%%, limit %.0f%%)\n",
		r.Added, r.Changed, r.Removed, r.Managed, r.Ratio*100, r.MaxRatio*100)
	for _, l := range r.Deactivated {
		fmt.Printf("deactivated: %s <%s> (%s)\n", l.Name, l.Email, l.Reason)
	}
	for _, c := range r.Conflicts {
		fmt.Println("not created, the email belongs to another account:", c)
	}
	switch r.Aborted {
	case "removal_ratio":
		fmt.Println("aborted: it would remove more memberships than allowed")
	case "empty_group":
		fmt.Println("aborted: the group came back empty:", r.Group)
	case "truncated":
		fmt.Println("aborted: the group is too large to read whole:", r.Group)
	}
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

// runAudit es `audit verify` y `audit export`.
func runAudit(ctx context.Context, cfg config.Config, args []string, from, to, out string) error {
	switch {
	case len(args) == 1 && args[0] == "verify":
		return runAuditVerify(ctx, cfg)
	case len(args) == 1 && args[0] == "export":
		return runAuditExport(ctx, cfg, from, to, out)
	}
	return errors.New(`usage: kudawiki audit verify | audit export --from YYYY-MM-DD --to YYYY-MM-DD [--out file.zip]`)
}

// runAuditExport arma el paquete de evidencia (§21.2.7). Lee la base sin migrar, como verify, y
// necesita la MASTER_KEY para firmar. Queda en la auditoría como hecho desde la consola.
func runAuditExport(ctx context.Context, cfg config.Config, from, to, out string) error {
	if out == "" {
		out = "kudawiki-evidence-" + from + "-" + to + ".zip"
	}
	db, err := database.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer db.Close()
	store := repo.New(db)
	mk, err := masterkey.Load(ctx, store, cfg.MasterKeyFile)
	if err != nil {
		return err
	}
	// #nosec G703 -- ruta que da quien opera la CLI, no un usuario de la wiki
	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	rep, err := audit.WriteEvidence(ctx, store, f, from, to, crypto.EvidenceKey(mk.Key), time.Now())
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		// #nosec G703 -- ruta que da quien opera la CLI, no un usuario de la wiki
		os.Remove(out)
		if errors.Is(err, audit.ErrInvalidRange) {
			return errors.New("--from and --to must be dates (YYYY-MM-DD), from not after to")
		}
		return err
	}
	if pending, err := db.Pending(ctx); err == nil && len(pending) == 0 {
		audit.NewRecorder(store, time.Now, slog.New(slog.NewTextHandler(os.Stderr, nil))).Record(ctx, audit.Event{Action: audit.AuditExport,
			TargetType: "audit", TargetID: from + ".." + to, System: "cli", Meta: map[string]any{"format": "evidence", "events": rep.Events, "chain_ok": rep.ChainOK}})
	}
	fmt.Printf("evidence package: %s (%d events from %s to %s)\n", out, rep.Events, from, to)
	fmt.Printf("signing key id: %s (give it to the auditor through another channel)\n", rep.KeyID)
	if !rep.ChainOK {
		return fmt.Errorf("the package was written, but the audit chain is broken: %s", rep.ChainError)
	}
	return nil
}

// runAuditVerify recorre la cadena de la auditoría (SPEC §11.1). Abre la BD sin migrar: es
// una revisión, y tiene que poder correr sobre una copia restaurada sin tocarla.
func runAuditVerify(ctx context.Context, cfg config.Config) error {
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
	scheme := "http"
	if cfg.TLS.CertFile != "" {
		// Con TLS propio el certificado es del nombre público, no de 127.0.0.1: no se puede
		// verificar contra la dirección local, y aquí solo se pregunta si el proceso responde.
		scheme = "https"
		// #nosec G402 -- healthcheck contra el propio servidor en localhost: el certificado es del nombre público
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}} //nolint:gosec // solo /healthz en 127.0.0.1
	}
	resp, err := client.Get(scheme + "://127.0.0.1:" + port + "/healthz")
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
	// #nosec G703 -- ruta que da quien opera la CLI, no un usuario de la wiki
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
	// Uno por actualización, para siempre, llenaba el disco (F8-B.6): quedan los últimos tres.
	// Si no se pueden borrar los viejos, la actualización sigue igual.
	removed, err := backup.KeepLatest(cfg.BackupDir(), backup.PreMigrate, backup.KeepPreMigrate)
	if err != nil {
		logger.Warn("pruning pre-migrate backups", "err", err)
	} else if len(removed) > 0 {
		logger.Info("pre-migrate backups pruned", "removed", len(removed))
	}
	return path, nil
}

// runBackupCommand es `backup`, `backup verify <file>` y `backup keygen`.
func runBackupCommand(ctx context.Context, cfg config.Config, args []string) error {
	switch {
	case len(args) == 0:
		return runBackup(ctx, cfg)
	case len(args) == 1 && args[0] == "keygen":
		if err := backup.GenerateKey(cfg.Backup.KeyFile); err != nil {
			return err
		}
		fmt.Println("backup key created:", cfg.Backup.KeyFile)
		fmt.Println("keep a copy of it apart from the backups, like the master key: without it the offsite copies cannot be read")
		return nil
	case len(args) == 2 && args[0] == "verify":
		return runBackupVerify(ctx, cfg, args[1])
	}
	return errors.New("usage: kudawiki backup | backup verify <file> | backup keygen")
}

// runBackupVerify prueba restaurar un respaldo en una carpeta temporal (§21.2.4). Si la base de
// la instalación existe y está al día, deja el resultado para el panel y la auditoría.
func runBackupVerify(ctx context.Context, cfg config.Config, file string) error {
	key, _ := backup.LoadKey(cfg.Backup.KeyFile) // solo hace falta para un .enc; CheckBackup lo dice
	tmp := cfg.BackupDir()
	// #nosec G703 -- ruta que da quien opera la CLI, no un usuario de la wiki
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return err
	}
	res, checkErr := jobs.CheckBackup(ctx, file, key, tmp)
	// #nosec G703 -- ruta que da quien opera la CLI, no un usuario de la wiki
	if _, err := os.Stat(cfg.DBPath()); err == nil {
		if db, err := database.Open(cfg.DBPath()); err == nil {
			defer db.Close()
			if pending, err := db.Pending(ctx); err == nil && len(pending) == 0 {
				store := repo.New(db)
				run := jobs.JobRun{At: time.Now(), File: filepath.Base(file), Source: "cli", Events: res.Events, OK: checkErr == nil}
				action := audit.BackupVerify
				if checkErr != nil {
					run.Error, action = checkErr.Error(), audit.BackupFailed
				}
				jobs.SaveRun(ctx, store, jobs.BackupVerifyJob, run)
				audit.NewRecorder(store, time.Now, slog.New(slog.NewTextHandler(os.Stderr, nil))).Record(ctx, audit.Event{Action: action,
					TargetType: "backup", TargetID: filepath.Base(file), System: "cli", Meta: map[string]any{"ok": checkErr == nil, "step": jobs.BackupVerifyJob}})
			}
		}
	}
	if checkErr != nil {
		return fmt.Errorf("the backup does not restore: %w", checkErr)
	}
	m := res.Manifest
	fmt.Printf("backup ok: %s backup from %s, schema %d, %d files; integrity_check ok; audit chain ok (%d events)\n",
		m.Kind, m.CreatedAt.Format(time.RFC3339), m.Schema, len(m.Files), res.Events)
	return nil
}

// runBackup hace un respaldo ahora (SPEC §13.3) y lo verifica. La MASTER_KEY no va adentro.
func runBackup(ctx context.Context, cfg config.Config) error {
	// #nosec G703 -- ruta que da quien opera la CLI, no un usuario de la wiki
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
	if strings.HasSuffix(file, backup.EncSuffix) {
		// La copia del destino externo: se descifra al lado con la llave de respaldo (§13.3).
		key, err := backup.LoadKey(cfg.Backup.KeyFile)
		if err != nil {
			return err
		}
		plain := strings.TrimSuffix(file, backup.EncSuffix)
		if err := backup.DecryptFile(file, plain, key); err != nil {
			return err
		}
		defer os.Remove(plain)
		file = plain
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
	if wsSlug == "" {
		_, ps, err := health.BackupChecks(ctx, store, time.Now())
		if err != nil {
			return err
		}
		problems = append(ps, problems...)
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
	// #nosec G703 -- ruta que da quien opera la CLI, no un usuario de la wiki
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

var errUsageAdmin = errors.New("usage: kudawiki admin unlock <email> | set-password <email> | create-superadmin <email> <name> | revoke-sessions --all | <email> | reset-2fa <email>")

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
	case cmd == "reset-2fa" && len(args) == 2:
		return "2FA removed for " + email + "; if it is required for them, they will set it up at their next login", svc.Reset2FA(ctx, email)
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
			// #nosec G703 -- p.Detail es un nombre de archivo que listó health.Check dentro de storage/, no entrada de un usuario
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

var errUsageMasterKey = errors.New("usage: kudawiki masterkey rotate | split --shares N --threshold K | combine")

// runMasterKey rota la MASTER_KEY y la reparte o reconstruye con Shamir (SPEC §6.5). Queda en la
// auditoría como hecho desde la consola.
func runMasterKey(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) == 0 {
		return errUsageMasterKey
	}
	if args[0] == "rotate" {
		// Con el servidor corriendo no: seguiría cifrando con la llave vieja filas que ya no la usan.
		if used, err := backup.InUse(cfg.DBPath()); err != nil {
			return err
		} else if used {
			return errors.New("the database is in use: stop `kudawiki serve` (or the container) before rotating the master key")
		}
	}
	// #nosec G703 -- ruta que da quien opera la CLI, no un usuario de la wiki
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
	rec := audit.NewRecorder(store, time.Now, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	switch {
	case args[0] == "rotate" && len(args) == 1:
		return rotateMasterKey(ctx, cfg, db, store, rec)
	case args[0] == "split":
		return splitMasterKey(ctx, cfg, store, rec, args[1:])
	case args[0] == "combine" && len(args) == 1:
		return combineMasterKey(ctx, cfg, store, rec, os.Stdin)
	}
	return errUsageMasterKey
}

func rotateMasterKey(ctx context.Context, cfg config.Config, db *database.DB, store *repo.Repo, rec *audit.Recorder) error {
	r, err := masterkey.Rotate(ctx, store, cfg.MasterKeyFile, func() (string, error) {
		path, err := backup.Create(ctx, db.Read, "", cfg.BackupDir(), backup.PreRotate, time.Now())
		if err != nil {
			return "", err
		}
		_, err = backup.Verify(path)
		return path, err
	})
	if err != nil {
		return err
	}
	rec.Record(ctx, audit.Event{Action: audit.MasterKeyRotate, TargetType: "master_key", TargetID: strconv.Itoa(r.To), System: "cli",
		Meta: map[string]any{"from": r.From, "to": r.To, "resealed": r.Secrets, "backup": filepath.Base(r.Backup)}})
	fmt.Printf("master key rotated: version %d -> %d (%d secrets re-encrypted)\n", r.From, r.To, r.Secrets)
	if r.Backup != "" {
		fmt.Println("backup before rotating:", r.Backup)
	}
	fmt.Printf("the old key is in %s: keep it with the backups made before now (they need it) and remove it from this server\n", r.Old)
	fmt.Println("replace every other copy of the key; if it was split, run `kudawiki masterkey split` again and destroy the old shares")
	fmt.Println("unsubscribe links in emails sent before now stop working")
	return nil
}

// splitMasterKey imprime la llave vigente en partes de Shamir, una por línea: <x>-<hex>. Las
// instrucciones van a stderr, para que stdout tenga solo las partes.
func splitMasterKey(ctx context.Context, cfg config.Config, store *repo.Repo, rec *audit.Recorder, args []string) error {
	sf := flag.NewFlagSet("masterkey split", flag.ContinueOnError)
	shares := sf.Int("shares", 0, "how many shares")
	threshold := sf.Int("threshold", 0, "how many shares rebuild the key")
	if err := sf.Parse(args); err != nil || sf.NArg() > 0 {
		return errUsageMasterKey
	}
	mk, err := masterkey.Load(ctx, store, cfg.MasterKeyFile)
	if err != nil {
		return err
	}
	parts, err := crypto.Split(mk.Key, *shares, *threshold)
	if err != nil {
		return err
	}
	for _, p := range parts {
		fmt.Printf("%d-%x\n", p[0], p[1:])
	}
	rec.Record(ctx, audit.Event{Action: audit.MasterKeySplit, TargetType: "master_key", TargetID: strconv.Itoa(mk.Version), System: "cli",
		Meta: map[string]any{"shares": *shares, "threshold": *threshold}})
	fmt.Fprintf(os.Stderr, "give each share to a different person; any %d of them rebuild the key with `kudawiki masterkey combine`.\n", *threshold)
	fmt.Fprintln(os.Stderr, "each share is part of the key: never keep two together, on this server or next to the backups")
	return nil
}

// combineMasterKey reconstruye la llave con las partes que llegan por in, una por línea hasta una
// vacía o el final. La compara con la base antes de escribirla: con partes de menos o mal
// copiadas Shamir da otra llave sin darse cuenta.
func combineMasterKey(ctx context.Context, cfg config.Config, store *repo.Repo, rec *audit.Recorder, in io.Reader) error {
	fmt.Fprintln(os.Stderr, "paste the shares, one per line, then an empty line:")
	var parts [][]byte
	sc := bufio.NewScanner(in)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			break
		}
		x, hexY, ok := strings.Cut(line, "-")
		n, err := strconv.Atoi(x)
		y, herr := hex.DecodeString(hexY)
		if !ok || err != nil || herr != nil || n < 1 || n > 255 {
			return fmt.Errorf("share %d is not <number>-<hex>", len(parts)+1)
		}
		parts = append(parts, append([]byte{byte(n)}, y...))
	}
	if err := sc.Err(); err != nil {
		return err
	}
	key, err := crypto.Combine(parts)
	if err != nil {
		return err
	}
	mk, err := masterkey.Check(ctx, store, key)
	if errors.Is(err, masterkey.ErrUnknownKey) {
		return errors.New("the shares do not rebuild this database's key: too few, mistyped, or from another installation")
	} else if err != nil {
		return err
	}
	written := false
	if current, err := os.ReadFile(cfg.MasterKeyFile); err == nil {
		if !bytes.Equal(current, key) {
			return fmt.Errorf("%s holds a different key: move it away before writing the rebuilt one", cfg.MasterKeyFile)
		}
		fmt.Println("the shares rebuild the key, which is already at", cfg.MasterKeyFile)
	} else if err := crypto.WriteMasterKey(cfg.MasterKeyFile, key); err != nil {
		return err
	} else {
		written = true
		fmt.Println("key rebuilt and written to", cfg.MasterKeyFile)
	}
	rec.Record(ctx, audit.Event{Action: audit.MasterKeyCombine, TargetType: "master_key", TargetID: strconv.Itoa(mk.Version), System: "cli",
		Meta: map[string]any{"shares": len(parts), "written": written}})
	return nil
}
