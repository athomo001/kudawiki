package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/athomo001/kudawiki/internal/config"
	"github.com/athomo001/kudawiki/internal/database"
	"github.com/athomo001/kudawiki/internal/health"
	"github.com/athomo001/kudawiki/internal/repo"
)

// runDoctorSecurity es `kudawiki doctor --security` (SPEC §21.2.6): revisa cómo está instalada
// la wiki, no su contenido. Solo lee; sale con 1 si hay errores.
func runDoctorSecurity(ctx context.Context, cfg config.Config) error {
	db, err := database.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer db.Close()
	problems, err := securityChecks(ctx, repo.New(db), cfg, time.Now(), runtime.GOOS)
	if err != nil {
		return err
	}
	errs := 0
	for _, p := range problems {
		if p.Severity == health.Error {
			errs++
		}
		fmt.Printf("%-7s  %-28s  %s\n", p.Severity, p.Check, p.Detail)
	}
	// Los avisos de seguridad de cada versión van en sus notas (§21.2.3): la wiki no consulta
	// internet para saber si hay uno (updates.check no existe en v1, §13.9).
	fmt.Printf("security advisories: see the release notes of version %s\n", version)
	fmt.Printf("security checks: %d errors, %d warnings\n", errs, len(problems)-errs)
	if errs > 0 {
		return errors.New("the installation has security errors")
	}
	return nil
}

// securityStore es lo que los chequeos leen de repo.
type securityStore interface {
	ListUsers(ctx context.Context) ([]repo.User, error)
	UserAreas(ctx context.Context) ([]repo.UserArea, error)
	JobState(ctx context.Context, name string) (string, error)
}

func securityChecks(ctx context.Context, st securityStore, cfg config.Config, now time.Time, goos string) ([]health.Problem, error) {
	var out []health.Problem
	add := func(sev health.Severity, check, detail string) {
		out = append(out, health.Problem{Check: check, Severity: sev, Detail: detail})
	}

	// Permisos (§21.2.6): data_dir 0700 y las llaves sin lectura para otros. En Windows los
	// permisos Unix no dicen nada: allá corre solo para desarrollo.
	if goos != "windows" {
		perm := func(path string, maxPerm fs.FileMode, check string) {
			fi, err := os.Stat(path)
			switch {
			case errors.Is(err, fs.ErrNotExist):
				return
			case err != nil:
				add(health.Error, check, err.Error())
			case fi.Mode().Perm()&^maxPerm != 0:
				add(health.Error, check, fmt.Sprintf("%s is %04o, at most %04o", path, fi.Mode().Perm(), maxPerm))
			}
		}
		perm(cfg.DataDir, 0o700, "perm.data_dir")
		perm(cfg.MasterKeyFile, 0o600, "perm.master_key")
		perm(cfg.Backup.KeyFile, 0o600, "perm.backup_key")
	}
	// La MASTER_KEY nunca dentro de lo que se respalda o se sirve (§6): un respaldo robado
	// traería la llave que abre los sellados.
	// Dentro de data_dir pero fuera de storage/ y backups/ no viaja en un respaldo, pero comparte
	// el volumen con los datos: la guía la pone aparte, como secret de solo lectura.
	switch {
	case inside(cfg.MasterKeyFile, cfg.StorageDir()) || inside(cfg.MasterKeyFile, cfg.BackupDir()):
		add(health.Error, "master_key.location", cfg.MasterKeyFile)
	case inside(cfg.MasterKeyFile, cfg.DataDir):
		add(health.Warning, "master_key.location", cfg.MasterKeyFile)
	}
	if cfg.Backup.KeyFile != "" && filepath.Clean(cfg.Backup.KeyFile) == filepath.Clean(cfg.MasterKeyFile) {
		add(health.Error, "backup_key.same_as_master", cfg.Backup.KeyFile)
	}

	// TLS (§21.2.1): lo mismo que exige serve; con --insecure-http el servidor arranca igual,
	// pero acá es un error.
	if warning, err := cfg.CheckTransport(false); err != nil {
		add(health.Error, "tls.base_url", err.Error())
	} else if warning != "" {
		add(health.Warning, "tls.base_url", warning)
	}

	users, err := st.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	roles, err := st.UserAreas(ctx)
	if err != nil {
		return nil, err
	}
	areaAdmin := map[string]bool{}
	for _, r := range roles {
		if r.Role == "admin" {
			areaAdmin[r.UserID] = true
		}
	}
	// El segundo factor de los admins (§8.1): la wiki lo pide en el primer login, pero quien
	// recibió el rol y no volvió a entrar todavía no lo tiene.
	var without []string
	emergency := false
	for _, u := range users {
		if !u.IsActive || (!u.IsGlobalAdmin && !areaAdmin[u.ID]) {
			continue
		}
		if !u.TOTPEnabled && u.Passkeys == 0 {
			without = append(without, u.Email)
		}
		if u.IsGlobalAdmin && u.AuthSource != "ldap" && u.PasswordHash != "" {
			emergency = true
		}
	}
	if len(without) > 0 {
		slices.Sort(without)
		add(health.Warning, "admins.without_2fa", strings.Join(without, ", "))
	}
	// Un superadmin local: si el directorio se cae, alguien tiene que poder entrar (§8.6).
	if !emergency {
		add(health.Error, "superadmin.no_local", "create one with: kudawiki admin create-superadmin")
	}

	// Respaldos recientes y probados (§21.2.4): los mismos chequeos que Salud.
	_, ps, err := health.BackupChecks(ctx, st, now)
	if err != nil {
		return nil, err
	}
	return append(out, ps...), nil
}

// inside dice si path queda dentro de dir (o es dir).
func inside(path, dir string) bool {
	if path == "" || dir == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
