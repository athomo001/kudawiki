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
	"strings"
	"syscall"
	"time"

	"github.com/athomo001/kudawiki"
	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/auth"
	"github.com/athomo001/kudawiki/internal/config"
	"github.com/athomo001/kudawiki/internal/crypto"
	"github.com/athomo001/kudawiki/internal/database"
	"github.com/athomo001/kudawiki/internal/httpapi"
	"github.com/athomo001/kudawiki/internal/jobs"
	"github.com/athomo001/kudawiki/internal/media"
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
  audit        "audit verify": check the hash chain of the audit log (exit 1 if broken)`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	configPath := fs.String("config", "", "path to kudawiki.yaml (default "+config.DefaultPath+" if present)")
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
		// Mensaje legible: el código del error no le dice nada a quien instala.
		switch {
		case errors.Is(err, auth.ErrPasswordTooShort) && cfg.IsLocal():
			return errors.New("password rejected: it cannot be empty")
		case errors.Is(err, auth.ErrPasswordTooShort):
			return fmt.Errorf("password rejected: it must have at least %d characters", auth.MinPasswordLength)
		case errors.Is(err, auth.ErrPasswordCommon):
			return errors.New("password rejected: it is in the list of common passwords, choose another")
		}
		return err
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

	// Sin llave no se arranca: sin ella los sellados de F7 serían ilegibles (SPEC §6.3).
	if _, err := crypto.LoadMasterKey(cfg.MasterKeyFile); err != nil {
		return fmt.Errorf("%w (run `kudawiki init` first)", err)
	}
	db, err := database.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
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
	svc, err := auth.NewService(store, rec, time.Now, cfg.IsLocal())
	if err != nil {
		return err
	}
	base, _ := url.Parse(cfg.BaseURL) // validada en config.Load
	files := media.NewService(store, rec, cfg.StorageDir(), cfg.Upload.MaxBytes)

	// La purga diaria termina antes de cerrar la BD: este defer corre antes que el de db.Close.
	jobCtx, stopJobs := context.WithCancel(ctx)
	jobDone := make(chan struct{})
	go func() {
		defer close(jobDone)
		jobs.Purge{Store: store, Files: files, Audit: rec, Retention: cfg.TrashRetention(), AuditRetention: cfg.AuditRetention(),
			Now: time.Now, Log: logger}.Daily(jobCtx)
	}()
	defer func() { stopJobs(); <-jobDone }()

	srv := &http.Server{
		Addr: cfg.Listen,
		Handler: httpapi.New(httpapi.Deps{Auth: svc, Workspaces: workspace.NewService(store, rec), Wiki: wiki.NewService(store, rec),
			Media: files, Read: store, DB: db, UI: kudawiki.UI(), BaseURL: base, TrashRetention: cfg.TrashRetention(), Log: logger}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
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
