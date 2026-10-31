// Package config lee kudawiki.yaml y las variables KUDAWIKI_*; las variables ganan (SPEC §13.1).
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"go.yaml.in/yaml/v3"
)

// DefaultPath es donde se busca el archivo si no se indica otro (SPEC §13.2).
const DefaultPath = "/etc/kudawiki/kudawiki.yaml"

// Config tiene solo las claves que usa la fase actual; las demás de §13.1 se agregan con su función.
type Config struct {
	Listen        string `yaml:"listen"`
	BaseURL       string `yaml:"base_url"`
	DataDir       string `yaml:"data_dir"`
	MasterKeyFile string `yaml:"master_key_file"`
	Upload        struct {
		MaxBytes int64 `yaml:"max_bytes"`
	} `yaml:"upload"`
	Trash struct {
		RetentionDays int `yaml:"retention_days"`
	} `yaml:"trash"`
	Audit struct {
		RetentionDays int `yaml:"retention_days"`
	} `yaml:"audit"`
	// Backup: el respaldo automático, una vez al día a esa hora local, y cuántos se guardan
	// (SPEC §13.3). El destino externo llega con F8.
	Backup struct {
		Time        string `yaml:"time"` // HH:MM
		KeepDaily   int    `yaml:"keep_daily"`
		KeepWeekly  int    `yaml:"keep_weekly"`
		KeepMonthly int    `yaml:"keep_monthly"`
	} `yaml:"backup"`
	// Branding: la marca de la empresa (SPEC §15.1). Lo que esté aquí gana sobre el panel y se
	// muestra bloqueado; nil es "no está en el archivo". La valida el paquete branding.
	Branding struct {
		Title         *string `yaml:"title"`
		Company       *string `yaml:"company"`
		ColorPrimary  *string `yaml:"color_primary"`
		ColorHeader   *string `yaml:"color_header"`
		ColorSidebar  *string `yaml:"color_sidebar"`
		LoginMessage  *string `yaml:"login_message"`
		ShowPoweredBy *bool   `yaml:"show_powered_by"`
	} `yaml:"branding"`
	// SMTP: el servidor de correo (SPEC §12). Misma regla que la marca: lo que esté aquí gana
	// sobre el panel y se muestra bloqueado. Lo valida el paquete mail.
	SMTP struct {
		Host     *string `yaml:"host"`
		Port     *int    `yaml:"port"`
		Security *string `yaml:"security"` // starttls | tls | none
		Username *string `yaml:"username"`
		Password *string `yaml:"password"`
		From     *string `yaml:"from"`
	} `yaml:"smtp"`
	// Auth.Local: recuperar la contraseña y las invitaciones de las cuentas locales (SPEC §8.5).
	// Los plazos son duraciones de Go: 30m, 168h.
	Auth struct {
		Local struct {
			PasswordReset bool   `yaml:"password_reset"`
			ResetTTL      string `yaml:"reset_ttl"`
			InviteTTL     string `yaml:"invite_ttl"`
		} `yaml:"local"`
	} `yaml:"auth"`
	// Notify.EmailDelay: cuánto espera un aviso sin leer antes de salir por correo (SPEC §12.1).
	Notify struct {
		EmailDelay string `yaml:"email_delay"`
	} `yaml:"notify"`
	// Approval.RemindAfterDays: a los cuántos días una propuesta pendiente se le recuerda al
	// admin (SPEC §5.3); la bandeja marca "esperando hace N días" desde ahí.
	Approval struct {
		RemindAfterDays int `yaml:"remind_after_days"`
	} `yaml:"approval"`
}

// AuditFloorDays es el piso de retención de la auditoría (SPEC §11.2): el superadmin es a quien
// el log vigila, así que un valor menor no se rechaza ni se obedece: se lee como 180.
const AuditFloorDays = 180

// DBPath y StorageDir siguen el layout de disco de SPEC §13.2.
func (c Config) DBPath() string     { return filepath.Join(c.DataDir, "data", "wiki.db") }
func (c Config) StorageDir() string { return filepath.Join(c.DataDir, "storage") }
func (c Config) BackupDir() string  { return filepath.Join(c.DataDir, "backups") }

// BackupAt es la hora y el minuto del respaldo diario (ya validados).
func (c Config) BackupAt() (hour, minute int) {
	t, _ := time.Parse("15:04", c.Backup.Time)
	return t.Hour(), t.Minute()
}

// ResetTTL e InviteTTL son cuánto valen los enlaces de §8.5 (ya validados).
func (c Config) ResetTTL() time.Duration {
	d, _ := time.ParseDuration(c.Auth.Local.ResetTTL)
	return d
}

func (c Config) InviteTTL() time.Duration {
	d, _ := time.ParseDuration(c.Auth.Local.InviteTTL)
	return d
}

// EmailDelay y RemindAfter son la ventana del correo de los avisos y el plazo del recordatorio
// (ya validados).
func (c Config) EmailDelay() time.Duration {
	d, _ := time.ParseDuration(c.Notify.EmailDelay)
	return d
}

func (c Config) RemindAfter() time.Duration {
	return time.Duration(c.Approval.RemindAfterDays) * 24 * time.Hour
}

// TrashRetention es cuánto queda algo en la papelera antes de la purga (SPEC §4.5).
func (c Config) TrashRetention() time.Duration {
	return time.Duration(c.Trash.RetentionDays) * 24 * time.Hour
}

// AuditRetention es cuánto se guarda la auditoría, nunca menos del piso (SPEC §11.1, §11.2).
func (c Config) AuditRetention() time.Duration {
	return time.Duration(max(c.Audit.RetentionDays, AuditFloorDays)) * 24 * time.Hour
}

// Load arma la configuración: valores por defecto, luego el archivo y al final las variables.
// Si path está vacío se usa DefaultPath solo si existe; un path explícito que no existe es un error.
func Load(path string) (Config, error) {
	c := Config{
		Listen:        ":8080",
		BaseURL:       "http://localhost:8080",
		DataDir:       "/var/lib/kudawiki",
		MasterKeyFile: "/etc/kudawiki/master.key",
	}
	c.Upload.MaxBytes = 25 << 20 // 25 MB (SPEC §4.4)
	c.Trash.RetentionDays = 30   // SPEC §4.5
	c.Audit.RetentionDays = 730  // 2 años (SPEC §11.1)
	c.Backup.Time = "03:00"      // SPEC §13.3
	c.Backup.KeepDaily, c.Backup.KeepWeekly, c.Backup.KeepMonthly = 7, 4, 6
	c.Auth.Local.PasswordReset = true // SPEC §8.5.1
	c.Auth.Local.ResetTTL, c.Auth.Local.InviteTTL = "30m", "168h"
	c.Notify.EmailDelay = "10m"    // SPEC §12.1
	c.Approval.RemindAfterDays = 3 // SPEC §5.3

	explicit := path != ""
	if !explicit {
		path = DefaultPath
	}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := yaml.Unmarshal(raw, &c); err != nil {
			return Config{}, fmt.Errorf("parse %s: %w", path, err)
		}
	case explicit || !errors.Is(err, os.ErrNotExist):
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	for env, dst := range map[string]*string{
		"KUDAWIKI_LISTEN":                &c.Listen,
		"KUDAWIKI_BASE_URL":              &c.BaseURL,
		"KUDAWIKI_DATA_DIR":              &c.DataDir,
		"KUDAWIKI_MASTER_KEY_FILE":       &c.MasterKeyFile,
		"KUDAWIKI_BACKUP_TIME":           &c.Backup.Time,
		"KUDAWIKI_AUTH_LOCAL_RESET_TTL":  &c.Auth.Local.ResetTTL,
		"KUDAWIKI_AUTH_LOCAL_INVITE_TTL": &c.Auth.Local.InviteTTL,
		"KUDAWIKI_NOTIFY_EMAIL_DELAY":    &c.Notify.EmailDelay,
	} {
		if v, ok := os.LookupEnv(env); ok {
			*dst = v
		}
	}
	for env, dst := range map[string]**string{
		"KUDAWIKI_BRANDING_TITLE":         &c.Branding.Title,
		"KUDAWIKI_BRANDING_COMPANY":       &c.Branding.Company,
		"KUDAWIKI_BRANDING_COLOR_PRIMARY": &c.Branding.ColorPrimary,
		"KUDAWIKI_BRANDING_COLOR_HEADER":  &c.Branding.ColorHeader,
		"KUDAWIKI_BRANDING_COLOR_SIDEBAR": &c.Branding.ColorSidebar,
		"KUDAWIKI_BRANDING_LOGIN_MESSAGE": &c.Branding.LoginMessage,
		"KUDAWIKI_SMTP_HOST":              &c.SMTP.Host,
		"KUDAWIKI_SMTP_SECURITY":          &c.SMTP.Security,
		"KUDAWIKI_SMTP_USERNAME":          &c.SMTP.Username,
		"KUDAWIKI_SMTP_PASSWORD":          &c.SMTP.Password,
		"KUDAWIKI_SMTP_FROM":              &c.SMTP.From,
	} {
		if v, ok := os.LookupEnv(env); ok {
			*dst = &v
		}
	}
	if v, ok := os.LookupEnv("KUDAWIKI_BRANDING_SHOW_POWERED_BY"); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("KUDAWIKI_BRANDING_SHOW_POWERED_BY: %w", err)
		}
		c.Branding.ShowPoweredBy = &b
	}
	if v, ok := os.LookupEnv("KUDAWIKI_AUTH_LOCAL_PASSWORD_RESET"); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("KUDAWIKI_AUTH_LOCAL_PASSWORD_RESET: %w", err)
		}
		c.Auth.Local.PasswordReset = b
	}
	if v, ok := os.LookupEnv("KUDAWIKI_SMTP_PORT"); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("KUDAWIKI_SMTP_PORT: %w", err)
		}
		c.SMTP.Port = &n
	}
	if v, ok := os.LookupEnv("KUDAWIKI_UPLOAD_MAX_BYTES"); ok {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return Config{}, fmt.Errorf("KUDAWIKI_UPLOAD_MAX_BYTES: %w", err)
		}
		c.Upload.MaxBytes = n
	}
	if v, ok := os.LookupEnv("KUDAWIKI_TRASH_RETENTION_DAYS"); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("KUDAWIKI_TRASH_RETENTION_DAYS: %w", err)
		}
		c.Trash.RetentionDays = n
	}
	if v, ok := os.LookupEnv("KUDAWIKI_APPROVAL_REMIND_AFTER_DAYS"); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("KUDAWIKI_APPROVAL_REMIND_AFTER_DAYS: %w", err)
		}
		c.Approval.RemindAfterDays = n
	}
	if v, ok := os.LookupEnv("KUDAWIKI_AUDIT_RETENTION_DAYS"); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("KUDAWIKI_AUDIT_RETENTION_DAYS: %w", err)
		}
		c.Audit.RetentionDays = n
	}
	return c, c.validate()
}

// IsLocal: la instalación solo se usa desde la misma máquina (desarrollo). Relaja la política
// de contraseñas (SPEC §8.1); en cuanto base_url apunta a otro host, rige la política completa.
func (c Config) IsLocal() bool {
	u, _ := url.Parse(c.BaseURL) // ya validada
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

func (c Config) validate() error {
	if _, err := time.Parse("15:04", c.Backup.Time); err != nil {
		return fmt.Errorf("backup.time must be HH:MM, got %q", c.Backup.Time)
	}
	if c.Backup.KeepDaily < 1 || c.Backup.KeepWeekly < 0 || c.Backup.KeepMonthly < 0 {
		return errors.New("backup.keep_daily must be at least 1, and keep_weekly and keep_monthly not negative")
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("base_url must be an absolute http(s) URL, got %q", c.BaseURL)
	}
	if c.Upload.MaxBytes <= 0 {
		return errors.New("upload.max_bytes must be positive")
	}
	if c.Trash.RetentionDays <= 0 {
		return errors.New("trash.retention_days must be positive")
	}
	// Un enlace de recuperación más largo que un día deja de ser "de un rato" (§8.5.1).
	if d, err := time.ParseDuration(c.Auth.Local.ResetTTL); err != nil || d < 5*time.Minute || d > 24*time.Hour {
		return fmt.Errorf("auth.local.reset_ttl must be a duration between 5m and 24h, got %q", c.Auth.Local.ResetTTL)
	}
	if d, err := time.ParseDuration(c.Auth.Local.InviteTTL); err != nil || d < time.Hour || d > 30*24*time.Hour {
		return fmt.Errorf("auth.local.invite_ttl must be a duration between 1h and 720h, got %q", c.Auth.Local.InviteTTL)
	}
	// Sin ventana, cada guardado de una página seguida sería un correo; más de un día ya no es aviso.
	if d, err := time.ParseDuration(c.Notify.EmailDelay); err != nil || d < time.Minute || d > 24*time.Hour {
		return fmt.Errorf("notify.email_delay must be a duration between 1m and 24h, got %q", c.Notify.EmailDelay)
	}
	if c.Approval.RemindAfterDays < 1 || c.Approval.RemindAfterDays > 90 {
		return fmt.Errorf("approval.remind_after_days must be between 1 and 90, got %d", c.Approval.RemindAfterDays)
	}
	if c.Listen == "" || c.DataDir == "" || c.MasterKeyFile == "" {
		return errors.New("listen, data_dir and master_key_file are required")
	}
	return nil
}
