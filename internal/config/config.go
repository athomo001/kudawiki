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
		"KUDAWIKI_LISTEN":          &c.Listen,
		"KUDAWIKI_BASE_URL":        &c.BaseURL,
		"KUDAWIKI_DATA_DIR":        &c.DataDir,
		"KUDAWIKI_MASTER_KEY_FILE": &c.MasterKeyFile,
		"KUDAWIKI_BACKUP_TIME":     &c.Backup.Time,
	} {
		if v, ok := os.LookupEnv(env); ok {
			*dst = v
		}
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
	if c.Listen == "" || c.DataDir == "" || c.MasterKeyFile == "" {
		return errors.New("listen, data_dir and master_key_file are required")
	}
	return nil
}
