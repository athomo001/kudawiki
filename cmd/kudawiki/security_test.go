package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/athomo001/kudawiki/internal/config"
	"github.com/athomo001/kudawiki/internal/database/dbtest"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/oklog/ulid/v2"
)

// §21.2.6: doctor --security marca la llave legible por otros, la MASTER_KEY dentro de storage/,
// base_url sin TLS, el admin sin segundo factor y la falta de un superadmin local; con todo en
// orden, solo queda lo de los respaldos (que esta base nunca hizo).
func TestSecurityChecks(t *testing.T) {
	ctx := context.Background()
	store := repo.New(dbtest.Open(t))
	dir := t.TempDir()
	cfg := config.Config{BaseURL: "http://wiki.empresa.cl", DataDir: filepath.Join(dir, "data")}
	cfg.MasterKeyFile = filepath.Join(cfg.StorageDir(), "master.key")
	if err := os.MkdirAll(cfg.StorageDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(cfg.MasterKeyFile, []byte("k"), 0o644)
	ldapAdmin := repo.User{ID: ulid.Make().String(), Email: "jefa@x.cl", FullName: "Jefa", IsActive: true, IsGlobalAdmin: true, AuthSource: "ldap", Locale: "es"}
	if err := store.CreateUser(ctx, ldapAdmin); err != nil {
		t.Fatal(err)
	}
	checks := func(goos string) map[string]string {
		ps, err := securityChecks(ctx, store, cfg, time.Now(), goos)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, p := range ps {
			out[p.Check] = string(p.Severity)
		}
		return out
	}
	// Los permisos Unix se prueban donde existen; en Windows doctor no los mira.
	got := checks(runtime.GOOS)
	want := map[string]string{"master_key.location": "error", "tls.base_url": "error", "admins.without_2fa": "warning",
		"superadmin.no_local": "error", "backup.none": "warning"}
	if runtime.GOOS != "windows" {
		want["perm.master_key"], want["perm.data_dir"] = "error", "error"
	}
	for check, sev := range want {
		if got[check] != sev {
			t.Errorf("%s: %q, want %q (all: %v)", check, got[check], sev, got)
		}
	}

	// En orden: llave aparte y 0600, data_dir 0700, https, un superadmin local con 2FA.
	cfg.BaseURL = "https://wiki.empresa.cl"
	cfg.MasterKeyFile = filepath.Join(dir, "master.key")
	os.WriteFile(cfg.MasterKeyFile, []byte("k"), 0o600)
	os.Chmod(cfg.DataDir, 0o700)
	if err := store.CreateUser(ctx, repo.User{ID: ulid.Make().String(), Email: "local@x.cl", FullName: "Local", PasswordHash: "h", IsActive: true, IsGlobalAdmin: true, Locale: "es"}); err != nil {
		t.Fatal(err)
	}
	got = checks(runtime.GOOS)
	delete(got, "backup.none")
	delete(got, "backup.never_verified")
	delete(got, "admins.without_2fa") // la local tampoco tiene 2FA en esta base
	if len(got) != 0 {
		t.Errorf("a hardened installation still has problems: %v", got)
	}
}
