// Package ldap conecta Kudawiki con el directorio de la empresa (SPEC §8.3, §8.6): la
// configuración de la conexión, el diagnóstico y, desde F6.2b, el login. La configura solo el
// superadmin; lo que está en kudawiki.yaml o en KUDAWIKI_AUTH_LDAP_* gana y el panel lo
// muestra bloqueado (§8.6.4).
package ldap

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	goldap "github.com/go-ldap/ldap/v3"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/crypto"
	"github.com/athomo001/kudawiki/internal/repo"
)

var (
	ErrInvalid    = errors.New("invalid_ldap")
	ErrIncomplete = errors.New("ldap_incomplete") // falta algo sin lo que no se puede conectar
	ErrLocked     = errors.New("ldap_locked")     // la clave está en el archivo de configuración
	// ErrPasswordRequired: se cambió adónde se conecta sin volver a escribir la contraseña.
	ErrPasswordRequired = errors.New("ldap_password_required")
	ErrTestFailed       = errors.New("ldap_test_failed")
	ErrUnavailable      = errors.New("ldap_unavailable")
)

// FieldError dice qué campo del formulario está mal, para marcarlo en el panel.
type FieldError struct {
	Field string
	Err   error
}

func (e *FieldError) Error() string { return e.Err.Error() }
func (e *FieldError) Unwrap() error { return e.Err }

func fieldErr(key string, err error) error {
	return &FieldError{Field: strings.TrimPrefix(key, "auth.ldap."), Err: err}
}

// Mask es lo que la API muestra en lugar de la contraseña de la cuenta de servicio: es un
// secreto de solo escritura (§3.1), y reenviar la máscara es "sin cambios".
const Mask = "••••••"

const passwordKey = "auth.ldap.bind_password"

// Attrs son los atributos del directorio de donde salen el correo, el nombre y la identidad.
type Attrs struct {
	Email string `json:"email"`
	Name  string `json:"name"`
	GUID  string `json:"guid"`
}

// Settings es la configuración tal como la ve el panel. Un texto vacío en los campos que
// dependen del flavor es "el valor por defecto del flavor" (Defaults).
type Settings struct {
	Enabled            bool     `json:"enabled"`
	Flavor             string   `json:"flavor"` // ad | openldap | generic
	URLs               []string `json:"urls"`
	StartTLS           bool     `json:"start_tls"`
	CAFile             string   `json:"ca_file"`
	InsecureSkipVerify bool     `json:"insecure_skip_verify"`
	BindDN             string   `json:"bind_dn"`
	BindPassword       string   `json:"bind_password"` // Mask si hay una; nunca el valor
	DomainNetbios      string   `json:"domain_netbios"`
	UserBaseDN         string   `json:"user_base_dn"`
	UserFilter         string   `json:"user_filter"`
	LoginAttrs         []string `json:"login_attrs"`
	GroupBaseDN        string   `json:"group_base_dn"`
	Attrs              Attrs    `json:"attrs"`
	DisabledFilter     string   `json:"disabled_filter"`
	Timeout            string   `json:"timeout"`
	Locked             []string `json:"locked"` // claves fijadas en el archivo: el panel no las cambia
}

// Defaults es lo que cada flavor pone donde el superadmin no escribió nada (§8.6.1).
type Defaults struct {
	UserFilter     string   `json:"user_filter"`
	LoginAttrs     []string `json:"login_attrs"`
	Attrs          Attrs    `json:"attrs"`
	DisabledFilter string   `json:"disabled_filter"`
}

// Flavors: el flavor solo cambia estos valores; cualquiera se puede sobrescribir. En generic la
// identidad queda vacía: sin un atributo estable se usa el DN, y el diagnóstico lo avisa.
var Flavors = map[string]Defaults{
	"ad": {
		UserFilter:     "(&(objectClass=user)(objectCategory=person))",
		LoginAttrs:     []string{"sAMAccountName", "userPrincipalName", "mail"},
		Attrs:          Attrs{Email: "mail", Name: "displayName", GUID: "objectGUID"},
		DisabledFilter: "(userAccountControl:1.2.840.113556.1.4.803:=2)", // bit 0x2, cuenta deshabilitada
	},
	"openldap": {
		UserFilter:     "(objectClass=inetOrgPerson)",
		LoginAttrs:     []string{"uid", "mail"},
		Attrs:          Attrs{Email: "mail", Name: "cn", GUID: "entryUUID"},
		DisabledFilter: "(|(nsAccountLock=TRUE)(pwdAccountLockedTime=*))",
	},
	"generic": {
		UserFilter: "(objectClass=person)",
		LoginAttrs: []string{"uid", "mail"},
		Attrs:      Attrs{Email: "mail", Name: "cn"},
	},
}

const defaultTimeout = 10 * time.Second

// AttrsPatch y Patch cambian algunas claves: nil no la toca y "" (o una lista vacía, o false)
// la vuelve al valor por defecto. En la contraseña, Mask tampoco la toca. Sirve igual para el
// panel y para el archivo.
type AttrsPatch struct {
	Email *string `json:"email"`
	Name  *string `json:"name"`
	GUID  *string `json:"guid"`
}

type Patch struct {
	Enabled            *bool       `json:"enabled"`
	Flavor             *string     `json:"flavor"`
	URLs               *[]string   `json:"urls"`
	StartTLS           *bool       `json:"start_tls"`
	CAFile             *string     `json:"ca_file"`
	InsecureSkipVerify *bool       `json:"insecure_skip_verify"`
	BindDN             *string     `json:"bind_dn"`
	BindPassword       *string     `json:"bind_password"`
	DomainNetbios      *string     `json:"domain_netbios"`
	UserBaseDN         *string     `json:"user_base_dn"`
	UserFilter         *string     `json:"user_filter"`
	LoginAttrs         *[]string   `json:"login_attrs"`
	GroupBaseDN        *string     `json:"group_base_dn"`
	Attrs              *AttrsPatch `json:"attrs"`
	DisabledFilter     *string     `json:"disabled_filter"`
	Timeout            *string     `json:"timeout"`
}

// values devuelve las claves que el Patch toca, validadas una por una y en JSON; nil borra la
// fila. Lo que depende de más de un campo lo revisa check. La contraseña va aparte, sin cifrar
// todavía: changed dice si el Patch la toca.
func (p Patch) values() (vals map[string]*string, password string, changed bool, err error) {
	vals = map[string]*string{}
	put := func(key string, v any, empty bool) {
		vals[key] = nil
		if !empty {
			b, _ := json.Marshal(v)
			vals[key] = new(string(b))
		}
	}
	strs := map[string]*string{"auth.ldap.flavor": p.Flavor, "auth.ldap.ca_file": p.CAFile, "auth.ldap.bind_dn": p.BindDN,
		"auth.ldap.domain_netbios": p.DomainNetbios, "auth.ldap.user_base_dn": p.UserBaseDN, "auth.ldap.user_filter": p.UserFilter,
		"auth.ldap.group_base_dn": p.GroupBaseDN, "auth.ldap.disabled_filter": p.DisabledFilter, "auth.ldap.timeout": p.Timeout}
	if p.Attrs != nil {
		strs["auth.ldap.attrs.email"], strs["auth.ldap.attrs.name"], strs["auth.ldap.attrs.guid"] = p.Attrs.Email, p.Attrs.Name, p.Attrs.GUID
	}
	for key, v := range strs {
		if v == nil {
			continue
		}
		norm, err := validate(key, *v)
		if err != nil {
			return nil, "", false, fieldErr(key, err)
		}
		put(key, norm, norm == "")
	}
	for key, v := range map[string]*[]string{"auth.ldap.urls": p.URLs, "auth.ldap.login_attrs": p.LoginAttrs} {
		if v == nil {
			continue
		}
		norm, err := validateList(key, *v)
		if err != nil {
			return nil, "", false, fieldErr(key, err)
		}
		put(key, norm, len(norm) == 0)
	}
	for key, v := range map[string]*bool{"auth.ldap.enabled": p.Enabled, "auth.ldap.start_tls": p.StartTLS, "auth.ldap.insecure_skip_verify": p.InsecureSkipVerify} {
		if v != nil {
			put(key, *v, !*v)
		}
	}
	if p.BindPassword != nil && *p.BindPassword != Mask {
		for _, r := range *p.BindPassword {
			if r < 0x20 || r == 0x7f {
				return nil, "", false, fieldErr(passwordKey, ErrInvalid)
			}
		}
		if utf8.RuneCountInString(*p.BindPassword) > 1024 {
			return nil, "", false, fieldErr(passwordKey, ErrInvalid)
		}
		return vals, *p.BindPassword, true, nil
	}
	return vals, "", false, nil
}

// attrName: un nombre de atributo de LDAP (RFC 4512) o su OID.
var attrName = regexp.MustCompile(`^(?:[A-Za-z][A-Za-z0-9-]{0,63}|[0-9]+(?:\.[0-9]+)+)$`)

var netbiosName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,14}$`)

func validate(key, v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	if strings.ContainsFunc(v, func(r rune) bool { return r < ' ' || r == 0x7f }) || utf8.RuneCountInString(v) > 1024 {
		return "", ErrInvalid
	}
	switch key {
	case "auth.ldap.flavor":
		if _, ok := Flavors[v]; !ok {
			return "", ErrInvalid
		}
	case "auth.ldap.ca_file":
		// Ruta en el servidor, como en el archivo de configuración (§8.6.4).
		if !filepath.IsAbs(v) {
			return "", ErrInvalid
		}
	case "auth.ldap.bind_dn", "auth.ldap.user_base_dn", "auth.ldap.group_base_dn":
		if _, err := goldap.ParseDN(v); err != nil {
			return "", ErrInvalid
		}
	case "auth.ldap.user_filter", "auth.ldap.disabled_filter":
		if _, err := goldap.CompileFilter(v); err != nil {
			return "", ErrInvalid
		}
	case "auth.ldap.domain_netbios":
		if !netbiosName.MatchString(v) {
			return "", ErrInvalid
		}
		return strings.ToUpper(v), nil
	case "auth.ldap.attrs.email", "auth.ldap.attrs.name", "auth.ldap.attrs.guid":
		if !attrName.MatchString(v) {
			return "", ErrInvalid
		}
	case "auth.ldap.timeout":
		// Más de un minuto ya no es "el directorio tarda": es que no responde.
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Second || d > time.Minute {
			return "", ErrInvalid
		}
	}
	return v, nil
}

func validateList(key string, list []string) ([]string, error) {
	var out []string
	for _, v := range list {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		switch key {
		case "auth.ldap.urls":
			u, err := normalizeURL(v)
			if err != nil {
				return nil, err
			}
			v = u
		case "auth.ldap.login_attrs":
			if !attrName.MatchString(v) {
				return nil, ErrInvalid
			}
		}
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	if len(out) > 10 {
		return nil, ErrInvalid
	}
	return out, nil
}

// normalizeURL deja solo esquema, host y puerto: un ldap:// con ruta, usuario o filtro (RFC 4516)
// no tiene sentido aquí, y aceptarlo escondería un error de tipeo.
func normalizeURL(v string) (string, error) {
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "ldap" && u.Scheme != "ldaps") || u.Hostname() == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", ErrInvalid
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return "", ErrInvalid
		}
	}
	return u.Scheme + "://" + u.Host, nil
}

type store interface {
	SiteSettings(ctx context.Context, prefix string) (map[string]string, error)
	SiteSecret(ctx context.Context, key string) ([]byte, int, error)
	SaveSiteSettings(ctx context.Context, values map[string]*string, secrets map[string]repo.SealedSetting, by string) error
}

// Service es la conexión al directorio.
type Service struct {
	store         store
	audit         *audit.Recorder
	key           []byte
	fixed         map[string]*string
	fixedPassword *string
	locked        []string
	now           func() time.Time
	log           *slog.Logger

	// last es la URL que respondió la última vez: se prueba primero (failover sin balanceador, §8.6.2).
	mu   sync.Mutex
	last string
}

// NewService arma el servicio con lo que viene del archivo, ya validado: un error ahí detiene
// el arranque, como en el resto de la configuración.
func NewService(s store, rec *audit.Recorder, masterKey []byte, fromConfig Patch, now func() time.Time, log *slog.Logger) (*Service, error) {
	fixed, password, hasPassword, err := fromConfig.values()
	if err != nil {
		var fe *FieldError
		errors.As(err, &fe)
		return nil, errors.New("auth.ldap." + fe.Field + " in the configuration file is not valid")
	}
	svc := &Service{store: s, audit: rec, key: masterKey, fixed: fixed, locked: []string{}, now: now, log: log}
	if hasPassword {
		svc.fixedPassword = &password
		svc.locked = append(svc.locked, "bind_password")
	}
	for k := range fixed {
		svc.locked = append(svc.locked, strings.TrimPrefix(k, "auth.ldap."))
	}
	slices.Sort(svc.locked)
	return svc, nil
}

// config es la configuración con la contraseña en claro: solo vive en memoria, para conectar.
type config struct {
	Settings
	password string
}

// fields asocia cada clave guardada con su campo.
func (st *Settings) fields() map[string]any {
	return map[string]any{"auth.ldap.enabled": &st.Enabled, "auth.ldap.flavor": &st.Flavor, "auth.ldap.urls": &st.URLs,
		"auth.ldap.start_tls": &st.StartTLS, "auth.ldap.ca_file": &st.CAFile, "auth.ldap.insecure_skip_verify": &st.InsecureSkipVerify,
		"auth.ldap.bind_dn": &st.BindDN, "auth.ldap.domain_netbios": &st.DomainNetbios, "auth.ldap.user_base_dn": &st.UserBaseDN,
		"auth.ldap.user_filter": &st.UserFilter, "auth.ldap.login_attrs": &st.LoginAttrs, "auth.ldap.group_base_dn": &st.GroupBaseDN,
		"auth.ldap.attrs.email": &st.Attrs.Email, "auth.ldap.attrs.name": &st.Attrs.Name, "auth.ldap.attrs.guid": &st.Attrs.GUID,
		"auth.ldap.disabled_filter": &st.DisabledFilter, "auth.ldap.timeout": &st.Timeout}
}

// settings arma la configuración: lo guardado, encima lo que el panel está por cambiar
// (overlay) y encima lo del archivo. Con open, descifra la contraseña guardada.
func (s *Service) settings(ctx context.Context, overlay map[string]*string, open bool) (config, error) {
	rows, err := s.store.SiteSettings(ctx, "auth.ldap.")
	if err != nil {
		return config{}, err
	}
	for _, layer := range []map[string]*string{overlay, s.fixed} {
		for k, v := range layer {
			if v == nil {
				delete(rows, k)
			} else {
				rows[k] = *v
			}
		}
	}
	c := config{Settings: Settings{Flavor: "ad", URLs: []string{}, LoginAttrs: []string{}, Timeout: defaultTimeout.String()}}
	for key, dst := range c.fields() {
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
			return config{}, errors.New("ldap bind password sealed with another master key version")
		default:
			plain, err := crypto.Open(s.key, enc, []byte(passwordKey))
			if err != nil {
				return config{}, err
			}
			c.password = string(plain)
		}
	}
	if c.password != "" {
		c.BindPassword = Mask
	}
	c.Locked = s.locked
	return c, nil
}

// Settings es la configuración para el panel, con la contraseña enmascarada.
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	c, err := s.settings(ctx, nil, false)
	return c.Settings, err
}

// candidate es la configuración que quedaría con el Patch, sin guardarla: la que se prueba y,
// si pasa, la que se guarda.
type candidate struct {
	config
	values    map[string]*string
	pwChanged bool
}

func (s *Service) candidate(ctx context.Context, p Patch) (candidate, error) {
	values, password, pwChanged, err := p.values()
	if err != nil {
		return candidate{}, err
	}
	for k := range values {
		if _, ok := s.fixed[k]; ok {
			return candidate{}, fieldErr(k, ErrLocked)
		}
	}
	if pwChanged && s.fixedPassword != nil {
		return candidate{}, fieldErr(passwordKey, ErrLocked)
	}
	before, err := s.settings(ctx, nil, true)
	if err != nil {
		return candidate{}, err
	}
	c, err := s.settings(ctx, values, true)
	if err != nil {
		return candidate{}, err
	}
	if pwChanged {
		c.password = password
		c.BindPassword = ""
		if password != "" {
			c.BindPassword = Mask
		}
	}
	// La contraseña guardada no viaja a otro destino: quien cambia adónde se conecta tiene que
	// volver a escribirla. Si no, con una sesión de superadmin robada bastaría apuntar las URLs a
	// un servidor propio y probar la conexión para llevarse la contraseña de la cuenta de servicio.
	if !pwChanged && before.password != "" && !sameDestination(before.Settings, c.Settings) {
		return candidate{}, fieldErr(passwordKey, ErrPasswordRequired)
	}
	if err := c.check(); err != nil {
		return candidate{}, err
	}
	return candidate{config: c, values: values, pwChanged: pwChanged}, nil
}

func sameDestination(a, b Settings) bool {
	return slices.Equal(a.URLs, b.URLs) && a.BindDN == b.BindDN && a.StartTLS == b.StartTLS &&
		a.InsecureSkipVerify == b.InsecureSkipVerify && a.CAFile == b.CAFile
}

// check revisa lo que depende de más de un campo. Se exige cifrado (§8.3.4): un ldap:// sin
// StartTLS mandaría las contraseñas en claro por la red. A ldaps:// el StartTLS no le aplica.
func (c config) check() error {
	if !c.StartTLS && slices.ContainsFunc(c.URLs, func(u string) bool { return strings.HasPrefix(u, "ldap://") }) {
		return fieldErr("auth.ldap.start_tls", ErrInvalid)
	}
	if c.Enabled {
		return c.complete()
	}
	return nil
}

// complete: lo mínimo para conectarse y buscar a alguien.
func (c config) complete() error {
	switch {
	case len(c.URLs) == 0:
		return fieldErr("auth.ldap.urls", ErrIncomplete)
	case c.BindDN == "":
		return fieldErr("auth.ldap.bind_dn", ErrIncomplete)
	case c.password == "":
		return fieldErr(passwordKey, ErrIncomplete)
	case c.UserBaseDN == "":
		return fieldErr("auth.ldap.user_base_dn", ErrIncomplete)
	}
	return nil
}

// effective completa con los valores del flavor lo que el superadmin dejó vacío.
func (c config) effective() config {
	d := Flavors[c.Flavor]
	if c.UserFilter == "" {
		c.UserFilter = d.UserFilter
	}
	if len(c.LoginAttrs) == 0 {
		c.LoginAttrs = d.LoginAttrs
	}
	if c.Attrs.Email == "" {
		c.Attrs.Email = d.Attrs.Email
	}
	if c.Attrs.Name == "" {
		c.Attrs.Name = d.Attrs.Name
	}
	if c.Attrs.GUID == "" {
		c.Attrs.GUID = d.Attrs.GUID
	}
	if c.DisabledFilter == "" {
		c.DisabledFilter = d.DisabledFilter
	}
	return c
}

func (c config) timeout() time.Duration {
	d, err := time.ParseDuration(c.Timeout)
	if err != nil {
		return defaultTimeout
	}
	return d
}

// Update guarda lo que cambió el superadmin; la contraseña, cifrada con la MASTER_KEY (§10.2).
// No se guarda una configuración activa que no conecta (§8.6.4): si la prueba falla, devuelve
// ErrTestFailed con el informe, salvo con force ("Guardar igual"), que queda en la auditoría.
// Tocar una clave fijada en el archivo es un error: no tendría efecto.
func (s *Service) Update(ctx context.Context, actorID string, p Patch, force bool) (Settings, *Report, error) {
	c, err := s.candidate(ctx, p)
	if err != nil {
		return Settings{}, nil, err
	}
	var report *Report
	if c.Enabled {
		r := s.testConnection(ctx, c.config)
		report = &r
		if !r.OK && !force {
			return Settings{}, report, ErrTestFailed
		}
	}
	before, err := s.Settings(ctx)
	if err != nil {
		return Settings{}, nil, err
	}
	var secrets map[string]repo.SealedSetting
	if c.pwChanged {
		secrets = map[string]repo.SealedSetting{passwordKey: {}}
		if c.password != "" {
			enc, err := crypto.Seal(s.key, []byte(c.password), []byte(passwordKey))
			if err != nil {
				return Settings{}, nil, err
			}
			secrets[passwordKey] = repo.SealedSetting{Enc: enc, KeyVersion: crypto.MasterKeyVersion}
		}
	}
	if err := s.store.SaveSiteSettings(ctx, c.values, secrets, actorID); err != nil {
		return Settings{}, nil, err
	}
	after, err := s.Settings(ctx)
	if err != nil {
		return Settings{}, nil, err
	}
	changed := changedKeys(before, after)
	// Una contraseña nueva se ve igual enmascarada: cuenta como cambio aunque la máscara no cambie.
	if c.pwChanged && !slices.Contains(changed, "bind_password") {
		changed = append(changed, "bind_password")
	}
	if len(changed) > 0 {
		slices.Sort(changed)
		meta := map[string]any{"keys": changed}
		if report != nil && !report.OK {
			meta["forced"] = true
		}
		s.audit.Record(ctx, audit.Event{Action: audit.LDAPConfigUpdate, TargetType: "site", Meta: meta})
	}
	return after, nil, nil
}

func changedKeys(a, b Settings) []string {
	var out []string
	fa, fb := a.fields(), b.fields()
	for key, va := range fa {
		ja, _ := json.Marshal(va)
		jb, _ := json.Marshal(fb[key])
		if string(ja) != string(jb) {
			out = append(out, strings.TrimPrefix(key, "auth.ldap."))
		}
	}
	if a.BindPassword != b.BindPassword {
		out = append(out, "bind_password")
	}
	return out
}

// LogInsecure deja el warning de cada arranque si se apagó la verificación del certificado
// (§8.3.4): es una excepción que nadie debería olvidar que existe.
func (s *Service) LogInsecure(ctx context.Context) {
	c, err := s.settings(ctx, nil, false)
	if err == nil && c.Enabled && c.InsecureSkipVerify {
		s.log.Warn("ldap: insecure_skip_verify is on; the directory certificate is not verified")
	}
}
