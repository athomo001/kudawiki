package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnvOverridesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kudawiki.yaml")
	os.WriteFile(path, []byte("listen: \":9000\"\ndata_dir: /srv/kw\n"), 0o600)
	t.Setenv("KUDAWIKI_DATA_DIR", "/data")

	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":9000" || c.DataDir != "/data" {
		t.Errorf("got listen=%q data_dir=%q", c.Listen, c.DataDir)
	}
}

func TestIsLocal(t *testing.T) {
	for url, want := range map[string]bool{
		"http://localhost:8080":       true,
		"http://127.0.0.1:8080":       true,
		"https://wiki.empresa.cl":     false,
		"http://localhost.empresa.cl": false,
	} {
		if got := (Config{BaseURL: url}).IsLocal(); got != want {
			t.Errorf("IsLocal(%s) = %v, want %v", url, got, want)
		}
	}
}

func TestExplicitMissingFileFails(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "no-existe.yaml")); err == nil {
		t.Error("want error for missing explicit config")
	}
}

func TestRejectsBadBaseURL(t *testing.T) {
	t.Setenv("KUDAWIKI_BASE_URL", "wiki.empresa.cl")
	path := filepath.Join(t.TempDir(), "kudawiki.yaml")
	os.WriteFile(path, []byte(`listen: ":8080"`), 0o600)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Errorf("got %v, want base_url error", err)
	}
}

func TestTrashRetention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kudawiki.yaml")
	os.WriteFile(path, []byte("trash:\n  retention_days: 7\n"), 0o600)
	if c, err := Load(path); err != nil || c.TrashRetention().Hours() != 7*24 {
		t.Errorf("got %v, %v; want 7 days", c.TrashRetention(), err)
	}
	t.Setenv("KUDAWIKI_TRASH_RETENTION_DAYS", "0")
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "trash.retention_days") {
		t.Errorf("got %v, want trash.retention_days error", err)
	}
}

// §11.2: una retención de auditoría bajo el piso no se obedece; se lee como 180 días.
func TestAuditRetentionFloor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kudawiki.yaml")
	os.WriteFile(path, []byte("audit:\n  retention_days: 30\n"), 0o600)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.AuditRetention().Hours() / 24; got != AuditFloorDays {
		t.Errorf("audit retention %v days, want %d", got, AuditFloorDays)
	}
}

// Respaldo diario (SPEC §13.3): 03:00 y 7/4/6 por omisión; la hora se valida.
func TestBackupSchedule(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kudawiki.yaml")
	os.WriteFile(path, []byte("backup:\n  keep_monthly: 12\n"), 0o600)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if h, m := c.BackupAt(); h != 3 || m != 0 || c.Backup.KeepDaily != 7 || c.Backup.KeepWeekly != 4 || c.Backup.KeepMonthly != 12 {
		t.Errorf("backup = %+v", c.Backup)
	}
	t.Setenv("KUDAWIKI_BACKUP_TIME", "23:30")
	if c, err := Load(path); err != nil || c.Backup.Time != "23:30" {
		t.Errorf("env time: %+v, %v", c.Backup, err)
	}
	t.Setenv("KUDAWIKI_BACKUP_TIME", "3 am")
	if _, err := Load(path); err == nil {
		t.Error("a bad backup.time was accepted")
	}
}

// La marca del archivo queda presente (no nil) aunque esté vacía, y las variables ganan.
func TestBranding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kudawiki.yaml")
	os.WriteFile(path, []byte("branding:\n  title: Wiki ACME\n  company: \"\"\n  show_powered_by: false\n"), 0o600)
	t.Setenv("KUDAWIKI_BRANDING_COLOR_PRIMARY", "#C8102E")
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	b := c.Branding
	if *b.Title != "Wiki ACME" || b.Company == nil || *b.Company != "" || *b.ShowPoweredBy || *b.ColorPrimary != "#C8102E" || b.ColorHeader != nil {
		t.Errorf("branding = %+v", b)
	}
	t.Setenv("KUDAWIKI_BRANDING_SHOW_POWERED_BY", "quizás")
	if _, err := Load(path); err == nil {
		t.Error("want error for a bad boolean")
	}
}

// El servidor de correo del archivo queda presente y las variables ganan, también el puerto.
func TestSMTP(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kudawiki.yaml")
	os.WriteFile(path, []byte("smtp:\n  host: smtp.acme.cl\n  port: 587\n  from: wiki@acme.cl\n"), 0o600)
	t.Setenv("KUDAWIKI_SMTP_PASSWORD", "secreta")
	t.Setenv("KUDAWIKI_SMTP_PORT", "465")
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	m := c.SMTP
	if *m.Host != "smtp.acme.cl" || *m.Port != 465 || *m.From != "wiki@acme.cl" || *m.Password != "secreta" || m.Security != nil || m.Username != nil {
		t.Errorf("smtp = %+v", m)
	}
	t.Setenv("KUDAWIKI_SMTP_PORT", "veinticinco")
	if _, err := Load(path); err == nil {
		t.Error("want error for a bad port")
	}
}

// Recuperar la contraseña viene encendido con enlaces de 30 minutos e invitaciones de 7 días.
func TestAuthLocal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kudawiki.yaml")
	os.WriteFile(path, []byte("auth:\n  local:\n    reset_ttl: 15m\n"), 0o600)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Auth.Local.PasswordReset || c.ResetTTL() != 15*time.Minute || c.InviteTTL() != 7*24*time.Hour {
		t.Errorf("auth.local = %+v", c.Auth.Local)
	}
	// El 2FA no se le exige a nadie salvo que se pida (§8.1, decidido el 2026-11-02).
	if c.Auth.TOTP.EnforceFor != "none" {
		t.Errorf("auth.totp.enforce_for = %q", c.Auth.TOTP.EnforceFor)
	}
	t.Setenv("KUDAWIKI_AUTH_TOTP_ENFORCE_FOR", "nobody")
	if _, err := Load(path); err == nil {
		t.Error("an unknown 2FA policy should be rejected")
	}
	t.Setenv("KUDAWIKI_AUTH_TOTP_ENFORCE_FOR", "admins")
	t.Setenv("KUDAWIKI_AUTH_LOCAL_PASSWORD_RESET", "false")
	if c, _ := Load(path); c.Auth.Local.PasswordReset {
		t.Error("the variable should turn it off")
	}
	t.Setenv("KUDAWIKI_AUTH_LOCAL_RESET_TTL", "72h")
	if _, err := Load(path); err == nil {
		t.Error("a reset link valid for days should be rejected")
	}
}

// La baja (§8.4, §13.10): 30 días de gracia y un año para seudonimizar, decididos el 2026-11-01.
func TestOffboarding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kudawiki.yaml")
	os.WriteFile(path, []byte("{}\n"), 0o600)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.OffboardingGrace() != 30*24*time.Hour || c.PseudonymizeAfter() != 365*24*time.Hour {
		t.Errorf("defaults: grace %v, pseudonymize after %v", c.OffboardingGrace(), c.PseudonymizeAfter())
	}
	t.Setenv("KUDAWIKI_OFFBOARDING_GRACE_DAYS", "0")
	if c, err = Load(path); err != nil || c.OffboardingGrace() != 0 {
		t.Errorf("grace 0: %v %v", c.OffboardingGrace(), err)
	}
	for env, v := range map[string]string{"KUDAWIKI_OFFBOARDING_GRACE_DAYS": "-1", "KUDAWIKI_AUDIT_PSEUDONYMIZE_AFTER_DAYS": "0"} {
		t.Setenv(env, v)
		if _, err := Load(path); err == nil {
			t.Errorf("%s=%s accepted", env, v)
		}
		t.Setenv(env, "30")
	}
}

// §8.6.4: la conexión LDAP puede venir del archivo o de KUDAWIKI_AUTH_LDAP_*; la contraseña de
// la cuenta de servicio, solo de un archivo aparte.
func TestLDAP(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "ldap.secret")
	os.WriteFile(secret, []byte("clave-svc\r\n"), 0o600)
	path := filepath.Join(dir, "kudawiki.yaml")
	os.WriteFile(path, []byte("auth:\n  ldap:\n    flavor: ad\n    urls: [ldaps://dc01:636]\n    bind_password_file: "+secret+"\n    attrs: { email: mail }\n"), 0o600)
	t.Setenv("KUDAWIKI_AUTH_LDAP_URLS", "ldaps://dc01:636,ldaps://dc02:636")
	t.Setenv("KUDAWIKI_AUTH_LDAP_ENABLED", "true")
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	l := c.Auth.LDAP
	if *l.Flavor != "ad" || len(*l.URLs) != 2 || !*l.Enabled || *l.Attrs.Email != "mail" || l.Attrs.Name != nil || l.BindDN != nil {
		t.Errorf("ldap = %+v", l)
	}
	if pw, err := c.LDAPBindPassword(); err != nil || *pw != "clave-svc" {
		t.Errorf("bind password = %v, %v", pw, err)
	}
	// La sincronización (§8.3.3, §8.3.4) y la foto (§8.6.3) también se fijan desde el entorno.
	t.Setenv("KUDAWIKI_AUTH_LDAP_ATTRS_PHOTO", "jpegPhoto")
	t.Setenv("KUDAWIKI_AUTH_LDAP_PASSWORD_HELP_URL", "https://cuenta.acme.cl")
	t.Setenv("KUDAWIKI_AUTH_LDAP_SYNC_INTERVAL", "30m")
	t.Setenv("KUDAWIKI_AUTH_LDAP_PRECREATE_USERS", "false")
	t.Setenv("KUDAWIKI_AUTH_LDAP_SYNC_MAX_REMOVAL_RATIO", "0.25")
	if c, err = Load(path); err != nil {
		t.Fatal(err)
	}
	l = c.Auth.LDAP
	if *l.Attrs.Photo != "jpegPhoto" || *l.PasswordHelpURL != "https://cuenta.acme.cl" || *l.Sync.Interval != "30m" ||
		*l.PrecreateUsers || *l.Sync.MaxRemovalRatio != 0.25 {
		t.Errorf("ldap sync = %+v", l)
	}
	t.Setenv("KUDAWIKI_AUTH_LDAP_SYNC_MAX_REMOVAL_RATIO", "mucho")
	if _, err := Load(path); err == nil {
		t.Error("a bad removal ratio was accepted")
	}
	os.Unsetenv("KUDAWIKI_AUTH_LDAP_SYNC_MAX_REMOVAL_RATIO")
	t.Setenv("KUDAWIKI_AUTH_LDAP_BIND_PASSWORD_FILE", filepath.Join(dir, "no-existe"))
	c, _ = Load(path)
	if _, err := c.LDAPBindPassword(); err == nil {
		t.Error("a missing password file was accepted")
	}
}

// §21.2.1: fuera de localhost, sin https:// no se arranca salvo con --insecure-http (con aviso);
// con TLS propio, base_url tiene que ser https://; el certificado y la llave van juntos.
func TestCheckTransport(t *testing.T) {
	for _, tc := range []struct {
		base, cert string
		insecure   bool
		wantErr    bool
		wantWarn   bool
	}{
		{base: "https://wiki.empresa.cl"},
		{base: "http://localhost:8080"},
		{base: "http://wiki.empresa.cl", wantErr: true},
		{base: "http://wiki.empresa.cl", insecure: true, wantWarn: true},
		{base: "https://wiki.empresa.cl", cert: "/etc/kudawiki/tls.crt"},
		{base: "http://localhost:8080", cert: "/etc/kudawiki/tls.crt", wantErr: true},
	} {
		c := Config{BaseURL: tc.base}
		c.TLS.CertFile = tc.cert
		warn, err := c.CheckTransport(tc.insecure)
		if (err != nil) != tc.wantErr || (warn != "") != tc.wantWarn {
			t.Errorf("%+v: warn=%q err=%v", tc, warn, err)
		}
	}

	path := filepath.Join(t.TempDir(), "kudawiki.yaml")
	os.WriteFile(path, []byte("tls:\n  cert_file: /x.crt\n"), 0o600)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "go together") {
		t.Errorf("cert without key: %v", err)
	}
	t.Setenv("KUDAWIKI_TRUST_PROXY", "true")
	c, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err == nil {
		t.Fatal("explicit missing file must fail")
	}
	os.WriteFile(path, []byte("listen: \":8080\"\n"), 0o600)
	if c, err = Load(path); err != nil || !c.TrustProxy {
		t.Errorf("KUDAWIKI_TRUST_PROXY: %v %v", c.TrustProxy, err)
	}
}
