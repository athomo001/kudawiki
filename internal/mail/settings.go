// Package mail es el correo de la wiki (SPEC §12): la configuración del servidor SMTP, las
// plantillas con la marca de la empresa y el envío. La cambia solo el superadmin; lo que está en
// kudawiki.yaml o en KUDAWIKI_SMTP_* gana y el panel lo muestra bloqueado (§13.1).
package mail

import (
	"context"
	"encoding/json"
	"errors"
	"net/mail"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/crypto"
	"github.com/athomo001/kudawiki/internal/repo"
)

var (
	ErrInvalid       = errors.New("invalid_smtp")
	ErrLocked        = errors.New("smtp_locked") // la clave está en el archivo de configuración
	ErrNotConfigured = errors.New("smtp_not_configured")
)

// Mask es lo que la API muestra en lugar de la contraseña guardada: un secreto de solo
// escritura nunca llega al navegador, y reenviar la máscara es "sin cambios" (§1).
const Mask = "••••••"

const passwordKey = "smtp.password"

// Settings es la configuración vigente tal como la ve el panel.
type Settings struct {
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	Security string   `json:"security"` // starttls | tls | none
	Username string   `json:"username"`
	Password string   `json:"password"` // Mask si hay una; nunca el valor
	From     string   `json:"from"`
	Locked   []string `json:"locked"`  // claves fijadas en el archivo: el panel no las cambia
	Enabled  bool     `json:"enabled"` // sin servidor y remitente no hay correo, solo avisos en la wiki (§12)
}

// defaults: STARTTLS en el puerto de envío es lo que piden hoy Microsoft 365, Gmail y la
// mayoría de los servidores internos.
var defaults = Settings{Port: 587, Security: "starttls"}

// Patch cambia algunas claves: nil no la toca y "" (o 0 en el puerto) la vuelve al valor por
// defecto. En la contraseña, Mask tampoco la toca. Sirve igual para el panel y para el archivo.
type Patch struct {
	Host     *string `json:"host"`
	Port     *int    `json:"port"`
	Security *string `json:"security"`
	Username *string `json:"username"`
	Password *string `json:"password"`
	From     *string `json:"from"`
}

// values devuelve las claves que el Patch toca, validadas y en JSON; nil borra la fila. La
// contraseña va aparte, sin cifrar todavía: changed dice si el Patch la toca.
func (p Patch) values() (vals map[string]*string, password string, changed bool, err error) {
	vals = map[string]*string{}
	put := func(key string, v any, empty bool) {
		vals[key] = nil
		if !empty {
			b, _ := json.Marshal(v)
			vals[key] = new(string(b))
		}
	}
	for key, v := range map[string]*string{"smtp.host": p.Host, "smtp.security": p.Security, "smtp.username": p.Username, "smtp.from": p.From} {
		if v == nil {
			continue
		}
		norm, err := validate(key, *v)
		if err != nil {
			return nil, "", false, err
		}
		put(key, norm, norm == "")
	}
	if p.Port != nil {
		if *p.Port < 0 || *p.Port > 65535 {
			return nil, "", false, ErrInvalid
		}
		put("smtp.port", *p.Port, *p.Port == 0)
	}
	if p.Password != nil && *p.Password != Mask {
		for _, r := range *p.Password {
			if r < 0x20 || r == 0x7f {
				return nil, "", false, ErrInvalid
			}
		}
		if utf8.RuneCountInString(*p.Password) > 1024 {
			return nil, "", false, ErrInvalid
		}
		return vals, *p.Password, true, nil
	}
	return vals, "", false, nil
}

func validate(key, v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	if strings.ContainsFunc(v, func(r rune) bool { return r <= ' ' || r == 0x7f }) {
		return "", ErrInvalid
	}
	switch key {
	case "smtp.host":
		if len(v) > 253 || strings.ContainsAny(v, "/@") {
			return "", ErrInvalid
		}
	case "smtp.security":
		if !slices.Contains([]string{"starttls", "tls", "none"}, v) {
			return "", ErrInvalid
		}
	case "smtp.username":
		if utf8.RuneCountInString(v) > 254 {
			return "", ErrInvalid
		}
	case "smtp.from":
		// Solo la dirección: el nombre que se ve es el título del sitio (§12).
		a, err := mail.ParseAddress(v)
		if err != nil || a.Name != "" || len(a.Address) > 254 {
			return "", ErrInvalid
		}
		return a.Address, nil
	}
	return v, nil
}

type store interface {
	SiteSettings(ctx context.Context, prefix string) (map[string]string, error)
	SiteSecret(ctx context.Context, key string) ([]byte, int, error)
	SaveSiteSettings(ctx context.Context, values map[string]*string, secrets map[string]repo.SealedSetting, by string) error
	queueStore
}

// config es la configuración con la contraseña en claro: solo vive en memoria, para enviar.
type config struct {
	Settings
	password string
}

// settings arma la configuración: valores por defecto, encima lo del panel y encima lo del
// archivo. Con open, descifra la contraseña guardada.
func (s *Service) settings(ctx context.Context, open bool) (config, error) {
	rows, err := s.store.SiteSettings(ctx, "smtp.")
	if err != nil {
		return config{}, err
	}
	for k, v := range s.fixed {
		if v == nil {
			delete(rows, k)
		} else {
			rows[k] = *v
		}
	}
	c := config{Settings: defaults}
	for key, dst := range map[string]any{"smtp.host": &c.Host, "smtp.port": &c.Port, "smtp.security": &c.Security,
		"smtp.username": &c.Username, "smtp.from": &c.From} {
		if raw, ok := rows[key]; ok {
			if err := json.Unmarshal([]byte(raw), dst); err != nil {
				return config{}, err
			}
		}
	}
	if s.fixedPassword != nil {
		c.password = *s.fixedPassword
	} else {
		enc, version, err := s.store.SiteSecret(ctx, passwordKey)
		switch {
		case errors.Is(err, repo.ErrNotFound):
		case err != nil:
			return config{}, err
		case !open:
			c.password = Mask
		case version != crypto.MasterKeyVersion:
			return config{}, errors.New("smtp password sealed with another master key version")
		default:
			plain, err := crypto.Open(s.key, enc, []byte(passwordKey))
			if err != nil {
				return config{}, err
			}
			c.password = string(plain)
		}
	}
	if c.password != "" {
		c.Password = Mask
	}
	c.Locked = s.locked
	c.Enabled = c.Host != "" && c.From != ""
	return c, nil
}

// Settings es la configuración para el panel, con la contraseña enmascarada.
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	c, err := s.settings(ctx, false)
	return c.Settings, err
}

// Update guarda lo que cambió el superadmin; la contraseña, cifrada con la MASTER_KEY (§10.2).
// La auditoría lleva las claves que cambiaron, nunca los valores (§11.2). Tocar una clave
// fijada en el archivo es un error: no tendría efecto.
func (s *Service) Update(ctx context.Context, actorID string, p Patch) (Settings, error) {
	values, password, pwChanged, err := p.values()
	if err != nil {
		return Settings{}, err
	}
	for k := range values {
		if _, ok := s.fixed[k]; ok {
			return Settings{}, ErrLocked
		}
	}
	if pwChanged && s.fixedPassword != nil {
		return Settings{}, ErrLocked
	}
	before, err := s.Settings(ctx)
	if err != nil {
		return Settings{}, err
	}
	var secrets map[string]repo.SealedSetting
	if pwChanged {
		secrets = map[string]repo.SealedSetting{passwordKey: {}}
		if password != "" {
			enc, err := crypto.Seal(s.key, []byte(password), []byte(passwordKey))
			if err != nil {
				return Settings{}, err
			}
			secrets[passwordKey] = repo.SealedSetting{Enc: enc, KeyVersion: crypto.MasterKeyVersion}
		}
	}
	if err := s.store.SaveSiteSettings(ctx, values, secrets, actorID); err != nil {
		return Settings{}, err
	}
	after, err := s.Settings(ctx)
	if err != nil {
		return Settings{}, err
	}
	changed := changedKeys(before, after)
	// Una contraseña nueva se ve igual enmascarada: cuenta como cambio aunque la máscara no cambie.
	if pwChanged && !slices.Contains(changed, "password") {
		changed = append(changed, "password")
	}
	if len(changed) > 0 {
		slices.Sort(changed)
		s.audit.Record(ctx, audit.Event{Action: audit.SiteSMTPUpdate, TargetType: "site", Meta: map[string]any{"keys": changed}})
	}
	return after, nil
}

func changedKeys(a, b Settings) []string {
	var out []string
	for k, pair := range map[string][2]any{"host": {a.Host, b.Host}, "port": {a.Port, b.Port}, "security": {a.Security, b.Security},
		"username": {a.Username, b.Username}, "password": {a.Password, b.Password}, "from": {a.From, b.From}} {
		if pair[0] != pair[1] {
			out = append(out, k)
		}
	}
	return out
}
