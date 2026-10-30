package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
