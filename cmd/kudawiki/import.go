package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/athomo001/kudawiki/internal/areaio"
	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/config"
	"github.com/athomo001/kudawiki/internal/database"
	"github.com/athomo001/kudawiki/internal/media"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/athomo001/kudawiki/internal/wiki"
	"github.com/athomo001/kudawiki/internal/wikijs"
)

const importUsage = `usage:
  kudawiki import wikijs2 --map <mapping.yaml> --as <superadmin email> (--dir <folder> | --db <dsn>) [--dry-run]
  kudawiki import markdown --area <slug> --as <superadmin email> <folder | file.zip> [--dry-run]`

// wikijsPlanJob guarda la huella del último --dry-run del importador de Wiki.js (§13.8).
const wikijsPlanJob = "wikijs_import_plan"

// runImport importa páginas desde la consola: una instalación de Wiki.js 2.x o una carpeta de
// Markdown (la web recibe un ZIP; una carpeta grande en el servidor, mejor desde aquí).
func runImport(ctx context.Context, cfg config.Config, args []string, dryRun bool) error {
	if len(args) == 0 || (args[0] != "wikijs2" && args[0] != "markdown") {
		return errors.New(importUsage)
	}
	fl := flag.NewFlagSet("import", flag.ContinueOnError)
	mapFile := fl.String("map", "", "wikijs2: the YAML mapping of Wiki.js paths to areas")
	dir := fl.String("dir", "", "wikijs2: the folder of the Wiki.js disk or Git storage")
	dsn := fl.String("db", "", "wikijs2: postgres://… or mysql://… of the Wiki.js database")
	areaSlug := fl.String("area", "", "markdown: the area to import into")
	as := fl.String("as", "", "the superadmin the pages are created by")
	if err := fl.Parse(args[1:]); err != nil {
		return err
	}
	if *as == "" {
		return errors.New(importUsage)
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
	actor, err := store.UserByEmail(ctx, *as)
	if err != nil || !actor.IsActive || !actor.IsGlobalAdmin {
		return fmt.Errorf("%s is not an active superadmin", *as)
	}
	// Lo que se crea queda en la auditoría a nombre de quien importa, como si lo hiciera en la web.
	ctx = audit.WithActor(ctx, audit.Actor{ID: actor.ID, Email: actor.Email, Name: actor.FullName, IP: "cli"})
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	rec := audit.NewRecorder(store, time.Now, logger)
	im := areaio.Importer{Store: store, Pages: wiki.NewService(store, rec), Media: media.NewService(store, rec, cfg.StorageDir(), cfg.Upload.MaxBytes)}
	areas, err := store.ListWorkspaces(ctx)
	if err != nil {
		return err
	}
	target := func(slug string) (areaio.Target, error) {
		for _, w := range areas {
			if w.Slug == slug && w.Kind == "area" {
				if w.Status != "active" || w.DeleteAt != nil {
					return areaio.Target{}, fmt.Errorf("area %q is not active", slug)
				}
				return areaio.Target{Scope: repo.Scope{WsID: w.ID, MaxRank: 3}, Area: w.Slug, ActorID: actor.ID}, nil
			}
		}
		return areaio.Target{}, fmt.Errorf("there is no area %q", slug)
	}
	run := func(t areaio.Target, fsys fs.FS, pages []areaio.Page, source string, skipped []areaio.Skip, warnings []areaio.Warning) error {
		rep, err := im.Run(ctx, t, fsys, pages, dryRun)
		if err != nil {
			return err
		}
		rep.Skipped = append(skipped, rep.Skipped...)
		rep.Warnings = append(warnings, rep.Warnings...)
		printReport(os.Stdout, t.Area, rep, dryRun)
		if !dryRun {
			rec.Record(ctx, audit.Event{Action: audit.WsImport, WsID: t.Scope.WsID, TargetType: "workspace", TargetID: t.Scope.WsID,
				Meta: map[string]any{"source": source, "pages": len(rep.Pages), "media": rep.Media, "skipped": len(rep.Skipped)}})
		}
		return nil
	}

	if args[0] == "markdown" {
		if *areaSlug == "" || fl.NArg() != 1 {
			return errors.New(importUsage)
		}
		t, err := target(*areaSlug)
		if err != nil {
			return err
		}
		fsys, closeFS, err := openSource(fl.Arg(0))
		if err != nil {
			return err
		}
		defer closeFS()
		pages, skipped, err := areaio.Read(fsys)
		if err != nil {
			return err
		}
		return run(t, fsys, pages, "markdown", skipped, nil)
	}

	if *mapFile == "" || (*dir == "") == (*dsn == "") {
		return errors.New(importUsage)
	}
	raw, err := os.ReadFile(*mapFile)
	if err != nil {
		return err
	}
	mapping, err := wikijs.ParseMapping(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", *mapFile, err)
	}
	var pages []wikijs.Page
	var assets fs.FS
	var unreadable []string
	if *dir != "" {
		assets = os.DirFS(*dir)
		if pages, unreadable, err = wikijs.ReadFolder(assets); err != nil {
			return err
		}
	} else {
		src, err := wikijs.OpenDB(*dsn)
		if err != nil {
			return err
		}
		defer src.Close()
		if pages, err = src.Pages(ctx); err != nil {
			return fmt.Errorf("read the Wiki.js database: %w", err)
		}
		if assets, err = src.Assets(ctx); err != nil {
			return fmt.Errorf("read the Wiki.js assets: %w", err)
		}
	}
	plan := wikijs.Build(pages, mapping)
	for _, p := range unreadable {
		plan.Skipped = append(plan.Skipped, areaio.Skip{Path: p, Reason: "unreadable"})
	}
	targets := map[string]areaio.Target{}
	for _, a := range plan.AreaNames() {
		if targets[a], err = target(a); err != nil {
			return err
		}
	}
	// --dry-run es obligatorio primero (§13.8): la importación real tiene que ser la del último
	// simulacro; si la fuente o el mapeo cambiaron, hay que volver a simular.
	fp := plan.Fingerprint()
	if dryRun {
		state, _ := json.Marshal(map[string]string{"plan": fp, "at": time.Now().UTC().Format(time.RFC3339)})
		if err := store.SetJobState(ctx, wikijsPlanJob, string(state)); err != nil {
			return err
		}
	} else {
		var last struct{ Plan string }
		raw, err := store.JobState(ctx, wikijsPlanJob)
		if err != nil {
			return err
		}
		if json.Unmarshal([]byte(raw), &last); last.Plan != fp {
			return errors.New("run the same command with --dry-run first and review the report (or the source or the mapping changed since the last one)")
		}
	}
	printSkips(os.Stdout, "", plan.Skipped)
	for _, a := range plan.AreaNames() {
		var warnings []areaio.Warning
		for _, w := range plan.Warnings {
			for _, p := range plan.Areas[a] {
				if p.Path == w.Path {
					warnings = append(warnings, w)
					break
				}
			}
		}
		if err := run(targets[a], assets, plan.Areas[a], "wikijs2", nil, warnings); err != nil {
			return err
		}
	}
	if dryRun {
		fmt.Println("\nNothing was imported. Levels: Wiki.js page rules and groups are not translated; assign them in each area after importing (unpublished pages come in at level 3).")
	}
	return nil
}

// openSource abre una carpeta o un ZIP como fs.FS.
func openSource(p string) (fs.FS, func(), error) {
	st, err := os.Stat(p)
	if err != nil {
		return nil, nil, err
	}
	if st.IsDir() {
		return os.DirFS(p), func() {}, nil
	}
	zr, err := zip.OpenReader(p)
	if err != nil {
		return nil, nil, err
	}
	return zr, func() { zr.Close() }, nil
}

func printReport(w io.Writer, area string, rep areaio.Report, dryRun bool) {
	verb := "imported"
	if dryRun {
		verb = "would import"
	}
	fmt.Fprintf(w, "\n== %s: %s %d pages, %d files; %d skipped, %d warnings\n", area, verb, len(rep.Pages), rep.Media, len(rep.Skipped), len(rep.Warnings))
	for _, p := range rep.Pages {
		fmt.Fprintln(w, "  +", p)
	}
	printSkips(w, area, rep.Skipped)
	for _, x := range rep.Warnings {
		detail := ""
		if x.Detail != "" {
			detail = " (" + x.Detail + ")"
		}
		fmt.Fprintf(w, "  ! %s: %s%s\n", x.Path, x.Reason, detail)
	}
}

func printSkips(w io.Writer, area string, skips []areaio.Skip) {
	if len(skips) > 0 && area == "" {
		fmt.Fprintf(w, "\n== not imported: %d\n", len(skips))
	}
	for _, s := range skips {
		fmt.Fprintf(w, "  - %s: %s\n", s.Path, strings.ReplaceAll(s.Reason, ":", " "))
	}
}
