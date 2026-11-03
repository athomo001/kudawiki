// Package backups es Administración → Respaldos (SPEC §13.3, F8-B): la configuración del
// respaldo diario desde el panel, la lista de respaldos, "Respaldar ahora" y "Borrar todo". Lo
// usa solo el superadmin; lo que fija kudawiki.yaml o una variable gana y se ve bloqueado
// (§13.1), como en el correo. La carpeta de los respaldos solo se cambia en el archivo: desde la
// web sería escribir archivos donde diga una sesión.
package backups

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/backup"
	"github.com/athomo001/kudawiki/internal/crypto"
	"github.com/athomo001/kudawiki/internal/repo"
)

var (
	ErrInvalid = errors.New("invalid_backup_settings")
	ErrLocked  = errors.New("backup_locked")       // la clave está en el archivo de configuración
	ErrNoKey   = errors.New("backup_key_missing")  // sin la llave de respaldo no se sube nada (§13.3)
	ErrBusy    = errors.New("backup_running")      // ya hay un respaldo o un borrado en curso
	ErrConfirm = errors.New("reset_confirm_wrong") // la frase de "Borrar todo" no coincide
	// El último respaldo que se probó restaurar y los de antes de "Borrar todo" no se borran
	// desde el panel (F8-B.6): son la vuelta atrás que se sabe que funciona.
	ErrProtected = errors.New("backup_protected")
)

// Mask reemplaza a la clave secreta del destino: nunca sale hacia el navegador, y reenviarla
// es "sin cambios" (§1), como la contraseña SMTP.
const Mask = "••••••"

const secretKey = "backup.remote.secret"

// Remote es el destino S3-compatible (§21.2.4).
type Remote struct {
	Endpoint    string `json:"endpoint"`
	Region      string `json:"region"`
	Bucket      string `json:"bucket"`
	Prefix      string `json:"prefix"`
	AccessKeyID string `json:"access_key_id"`
	Secret      string `json:"secret"` // Mask si hay una; nunca el valor
	PathStyle   bool   `json:"path_style"`
}

// Settings es la configuración vigente tal como la ve el panel.
type Settings struct {
	Time        string   `json:"time"` // HH:MM, hora del servidor
	KeepDaily   int      `json:"keep_daily"`
	KeepWeekly  int      `json:"keep_weekly"`
	KeepMonthly int      `json:"keep_monthly"`
	Remote      Remote   `json:"remote"`
	Locked      []string `json:"locked"`  // claves fijadas en el archivo: el panel no las cambia
	Dir         string   `json:"dir"`     // dónde quedan; solo se cambia en el archivo
	HasKey      bool     `json:"has_key"` // existe la llave de respaldo
}

// RemoteOn dice si hay destino externo: con el endpoint alcanza para intentarlo; lo demás lo
// exige Update al guardarlo.
func (s Settings) RemoteOn() bool { return s.Remote.Endpoint != "" }

// Patch cambia algunas claves: nil no la toca; "" la borra (vuelve al valor por defecto). En la
// clave secreta, Mask tampoco la toca.
type Patch struct {
	Time        *string      `json:"time"`
	KeepDaily   *int         `json:"keep_daily"`
	KeepWeekly  *int         `json:"keep_weekly"`
	KeepMonthly *int         `json:"keep_monthly"`
	Remote      *RemotePatch `json:"remote"`
}

type RemotePatch struct {
	Endpoint    *string `json:"endpoint"`
	Region      *string `json:"region"`
	Bucket      *string `json:"bucket"`
	Prefix      *string `json:"prefix"`
	AccessKeyID *string `json:"access_key_id"`
	Secret      *string `json:"secret"`
	PathStyle   *bool   `json:"path_style"`
}

// Fixed es lo que trae el archivo de configuración, ya validado al arrancar: las claves de Set
// ganan sobre el panel. SecretFile es la clave secreta leída de su archivo, si está ahí.
type Fixed struct {
	Settings
	Set        map[string]bool
	SecretFile string
}

// defaults de §13.3: diario a las 03:00, 7 diarios, 4 semanales y 6 mensuales.
var defaults = Settings{Time: "03:00", KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 6}

// Las claves en site_settings (prefijo "backup.") y en Locked, iguales a las del archivo.
var keys = []string{"time", "keep_daily", "keep_weekly", "keep_monthly", "remote.endpoint", "remote.region",
	"remote.bucket", "remote.prefix", "remote.access_key_id", "remote.secret_access_key_file", "remote.path_style"}

// values valida el Patch y lo pasa a filas de site_settings; nil borra la fila. La clave secreta
// va aparte, sin cifrar todavía.
func (p Patch) values() (vals map[string]*string, secret string, secretChanged bool, err error) {
	vals = map[string]*string{}
	put := func(key string, v any, empty bool) {
		vals["backup."+key] = nil
		if !empty {
			b, _ := json.Marshal(v)
			vals["backup."+key] = new(string(b))
		}
	}
	if p.Time != nil {
		t := strings.TrimSpace(*p.Time)
		if _, err := time.Parse("15:04", t); t != "" && (err != nil || len(t) != 5) {
			return nil, "", false, ErrInvalid
		}
		put("time", t, t == "")
	}
	for key, v := range map[string]struct {
		p        *int
		min, max int
	}{"keep_daily": {p.KeepDaily, 1, 3650}, "keep_weekly": {p.KeepWeekly, 0, 520}, "keep_monthly": {p.KeepMonthly, 0, 240}} {
		if v.p == nil {
			continue
		}
		if *v.p < v.min || *v.p > v.max {
			return nil, "", false, ErrInvalid
		}
		put(key, *v.p, false)
	}
	if r := p.Remote; r != nil {
		for key, v := range map[string]*string{"remote.endpoint": r.Endpoint, "remote.region": r.Region, "remote.bucket": r.Bucket,
			"remote.prefix": r.Prefix, "remote.access_key_id": r.AccessKeyID} {
			if v == nil {
				continue
			}
			s := strings.TrimSpace(*v)
			if utf8.RuneCountInString(s) > 512 || strings.ContainsFunc(s, func(r rune) bool { return r <= ' ' || r == 0x7f }) {
				return nil, "", false, ErrInvalid
			}
			put(key, s, s == "")
		}
		if r.PathStyle != nil {
			put("remote.path_style", *r.PathStyle, !*r.PathStyle)
		}
		if r.Secret != nil && *r.Secret != Mask {
			if utf8.RuneCountInString(*r.Secret) > 512 || strings.ContainsFunc(*r.Secret, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
				return nil, "", false, ErrInvalid
			}
			return vals, *r.Secret, true, nil
		}
	}
	return vals, "", false, nil
}

// config es la configuración con la clave secreta en claro: solo vive en memoria, para subir.
type config struct {
	Settings
	secret string
}

// settings arma la configuración: valores por defecto, encima lo del panel y encima lo del
// archivo. Con open, descifra la clave secreta guardada.
func (s *Service) settings(ctx context.Context, open bool) (config, error) {
	rows, err := s.store.SiteSettings(ctx, "backup.")
	if err != nil {
		return config{}, err
	}
	c := config{Settings: defaults}
	c.Locked = []string{}         // la lista va siempre, aunque vacía
	c.Remote.Region = "us-east-1" // lo que pide AWS por defecto; MinIO lo acepta
	for key, dst := range map[string]any{"time": &c.Time, "keep_daily": &c.KeepDaily, "keep_weekly": &c.KeepWeekly,
		"keep_monthly": &c.KeepMonthly, "remote.endpoint": &c.Remote.Endpoint, "remote.region": &c.Remote.Region,
		"remote.bucket": &c.Remote.Bucket, "remote.prefix": &c.Remote.Prefix, "remote.access_key_id": &c.Remote.AccessKeyID,
		"remote.path_style": &c.Remote.PathStyle} {
		if raw, ok := rows["backup."+key]; ok && !s.fixed.Set[key] {
			if err := json.Unmarshal([]byte(raw), dst); err != nil {
				return config{}, err
			}
		}
	}
	f := s.fixed.Settings
	for key, pair := range map[string][2]any{"time": {&c.Time, f.Time}, "keep_daily": {&c.KeepDaily, f.KeepDaily},
		"keep_weekly": {&c.KeepWeekly, f.KeepWeekly}, "keep_monthly": {&c.KeepMonthly, f.KeepMonthly},
		"remote.endpoint": {&c.Remote.Endpoint, f.Remote.Endpoint}, "remote.region": {&c.Remote.Region, f.Remote.Region},
		"remote.bucket": {&c.Remote.Bucket, f.Remote.Bucket}, "remote.prefix": {&c.Remote.Prefix, f.Remote.Prefix},
		"remote.access_key_id": {&c.Remote.AccessKeyID, f.Remote.AccessKeyID}, "remote.path_style": {&c.Remote.PathStyle, f.Remote.PathStyle}} {
		if !s.fixed.Set[key] {
			continue
		}
		switch dst := pair[0].(type) {
		case *string:
			*dst = pair[1].(string)
		case *int:
			*dst = pair[1].(int)
		case *bool:
			*dst = pair[1].(bool)
		}
	}
	if s.fixed.Set["remote.secret_access_key_file"] {
		c.secret = s.fixed.SecretFile
	} else {
		enc, version, err := s.store.SiteSecret(ctx, secretKey)
		switch {
		case errors.Is(err, repo.ErrNotFound):
		case err != nil:
			return config{}, err
		case !open:
			c.secret = Mask
		case version != s.key.Version:
			return config{}, errors.New("backup secret sealed with another master key version")
		default:
			plain, err := crypto.Open(s.key.Key, enc, []byte(secretKey))
			if err != nil {
				return config{}, err
			}
			c.secret = string(plain)
		}
	}
	if c.secret != "" {
		c.Remote.Secret = Mask
	}
	for _, k := range keys {
		if s.fixed.Set[k] {
			c.Locked = append(c.Locked, k)
		}
	}
	c.Dir = s.dir
	c.HasKey = s.hasKey()
	return c, nil
}

// Settings es la configuración para el panel, con la clave secreta enmascarada.
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	c, err := s.settings(ctx, false)
	return c.Settings, err
}

// Update guarda lo que cambia el panel. Un destino externo completo se prueba armándolo (como al
// arrancar) y exige la llave de respaldo: sin ella no se puede cifrar lo que sale (§13.3).
func (s *Service) Update(ctx context.Context, actorID string, p Patch) (Settings, error) {
	values, secret, secretChanged, err := p.values()
	if err != nil {
		return Settings{}, err
	}
	for k := range values {
		if s.fixed.Set[strings.TrimPrefix(k, "backup.")] {
			return Settings{}, ErrLocked
		}
	}
	if secretChanged && s.fixed.Set["remote.secret_access_key_file"] {
		return Settings{}, ErrLocked
	}
	before, err := s.settings(ctx, true)
	if err != nil {
		return Settings{}, err
	}
	// Cómo quedaría, para validarlo antes de guardar.
	next := before
	if err := apply(&next, values); err != nil {
		return Settings{}, err
	}
	if secretChanged {
		next.secret = secret
	}
	if next.RemoteOn() {
		if _, err := backup.NewS3(next.Remote.Endpoint, next.Remote.Region, next.Remote.Bucket, next.Remote.Prefix,
			next.Remote.AccessKeyID, next.secret, next.Remote.PathStyle); err != nil {
			return Settings{}, ErrInvalid
		}
		if !s.hasKey() {
			return Settings{}, ErrNoKey
		}
	}
	var secrets map[string]repo.SealedSetting
	if secretChanged {
		secrets = map[string]repo.SealedSetting{secretKey: {}}
		if secret != "" {
			enc, err := crypto.Seal(s.key.Key, []byte(secret), []byte(secretKey))
			if err != nil {
				return Settings{}, err
			}
			secrets[secretKey] = repo.SealedSetting{Enc: enc, KeyVersion: s.key.Version}
		}
	}
	if err := s.store.SaveSiteSettings(ctx, values, secrets, actorID); err != nil {
		return Settings{}, err
	}
	after, err := s.Settings(ctx)
	if err != nil {
		return Settings{}, err
	}
	changed := changedKeys(before.Settings, after)
	if secretChanged && !slices.Contains(changed, "remote.secret") {
		changed = append(changed, "remote.secret")
	}
	if len(changed) > 0 {
		slices.Sort(changed)
		s.audit.Record(ctx, audit.Event{Action: audit.BackupSettingsUpdate, TargetType: "site", Meta: map[string]any{"keys": changed}})
		s.wake() // la hora pudo cambiar: el job diario recalcula cuándo le toca
	}
	return after, nil
}

// apply pone en c lo que guardaría values, con los mismos valores por defecto que settings.
func apply(c *config, values map[string]*string) error {
	for k, v := range values {
		key := strings.TrimPrefix(k, "backup.")
		var dst any
		switch key {
		case "time":
			dst = &c.Time
		case "keep_daily":
			dst = &c.KeepDaily
		case "keep_weekly":
			dst = &c.KeepWeekly
		case "keep_monthly":
			dst = &c.KeepMonthly
		case "remote.endpoint":
			dst = &c.Remote.Endpoint
		case "remote.region":
			dst = &c.Remote.Region
		case "remote.bucket":
			dst = &c.Remote.Bucket
		case "remote.prefix":
			dst = &c.Remote.Prefix
		case "remote.access_key_id":
			dst = &c.Remote.AccessKeyID
		case "remote.path_style":
			dst = &c.Remote.PathStyle
		}
		if v == nil {
			switch key {
			case "time":
				c.Time = defaults.Time
			case "remote.region":
				c.Remote.Region = "us-east-1"
			case "remote.path_style":
				c.Remote.PathStyle = false
			default:
				if p, ok := dst.(*string); ok {
					*p = ""
				}
			}
			continue
		}
		if err := json.Unmarshal([]byte(*v), dst); err != nil {
			return err
		}
	}
	return nil
}

func changedKeys(a, b Settings) []string {
	var out []string
	for k, pair := range map[string][2]any{"time": {a.Time, b.Time}, "keep_daily": {a.KeepDaily, b.KeepDaily},
		"keep_weekly": {a.KeepWeekly, b.KeepWeekly}, "keep_monthly": {a.KeepMonthly, b.KeepMonthly},
		"remote.endpoint": {a.Remote.Endpoint, b.Remote.Endpoint}, "remote.region": {a.Remote.Region, b.Remote.Region},
		"remote.bucket": {a.Remote.Bucket, b.Remote.Bucket}, "remote.prefix": {a.Remote.Prefix, b.Remote.Prefix},
		"remote.access_key_id": {a.Remote.AccessKeyID, b.Remote.AccessKeyID}, "remote.path_style": {a.Remote.PathStyle, b.Remote.PathStyle},
		"remote.secret": {a.Remote.Secret, b.Remote.Secret}} {
		if pair[0] != pair[1] {
			out = append(out, k)
		}
	}
	return out
}
