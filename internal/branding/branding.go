// Package branding es la marca de la empresa (SPEC §15.1): título, colores, logo y favicon de
// la instalación, una sola para todas las áreas (§15.1.5). La cambia solo el superadmin; lo que
// está en kudawiki.yaml o en KUDAWIKI_* gana y el panel lo muestra bloqueado (§13.1).
package branding

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/repo"
)

var (
	ErrInvalid         = errors.New("invalid_branding")
	ErrLocked          = errors.New("branding_locked") // la clave está en el archivo de configuración
	ErrTooLarge        = errors.New("file_too_large")
	ErrUnsupportedType = errors.New("unsupported_file_type")
)

// Límites de §15.1.1. El mensaje del login es texto plano: nunca se interpreta como HTML.
const (
	maxTitle        = 40
	maxCompany      = 80
	maxLoginMessage = 500
)

// Settings es la marca vigente.
type Settings struct {
	Title         string `json:"title"`
	Company       string `json:"company"`
	ColorPrimary  string `json:"color_primary"`
	ColorHeader   string `json:"color_header"`
	ColorSidebar  string `json:"color_sidebar"` // vacío: el principal
	LoginMessage  string `json:"login_message"`
	ShowPoweredBy bool   `json:"show_powered_by"`
}

// Defaults es la marca de Kudawiki: la que se ve mientras la empresa no ponga la suya.
var Defaults = Settings{Title: "Kudawiki", ColorPrimary: DefaultPrimary, ColorHeader: DefaultHeader, ShowPoweredBy: true}

// Patch cambia algunas claves: nil no la toca y "" la vuelve al valor por defecto. Sirve igual
// para lo que manda el panel y para lo que fija el archivo de configuración.
type Patch struct {
	Title         *string `json:"title"`
	Company       *string `json:"company"`
	ColorPrimary  *string `json:"color_primary"`
	ColorHeader   *string `json:"color_header"`
	ColorSidebar  *string `json:"color_sidebar"`
	LoginMessage  *string `json:"login_message"`
	ShowPoweredBy *bool   `json:"show_powered_by"`
}

// field une la clave de site_settings (§10.2) con su campo en Settings y en Patch.
type field struct {
	key   string
	patch func(*Patch) any
	set   func(*Settings, json.RawMessage) error
}

func str(dst func(*Settings) *string) func(*Settings, json.RawMessage) error {
	return func(s *Settings, raw json.RawMessage) error { return json.Unmarshal(raw, dst(s)) }
}

var fields = []field{
	{"branding.title", func(p *Patch) any { return p.Title }, str(func(s *Settings) *string { return &s.Title })},
	{"branding.company", func(p *Patch) any { return p.Company }, str(func(s *Settings) *string { return &s.Company })},
	{"branding.color_primary", func(p *Patch) any { return p.ColorPrimary }, str(func(s *Settings) *string { return &s.ColorPrimary })},
	{"branding.color_header", func(p *Patch) any { return p.ColorHeader }, str(func(s *Settings) *string { return &s.ColorHeader })},
	{"branding.color_sidebar", func(p *Patch) any { return p.ColorSidebar }, str(func(s *Settings) *string { return &s.ColorSidebar })},
	{"branding.login_message", func(p *Patch) any { return p.LoginMessage }, str(func(s *Settings) *string { return &s.LoginMessage })},
	{"branding.show_powered_by", func(p *Patch) any { return p.ShowPoweredBy },
		func(s *Settings, raw json.RawMessage) error { return json.Unmarshal(raw, &s.ShowPoweredBy) }},
}

// values devuelve las claves que el Patch toca, con su valor ya validado y normalizado. Una
// cadena vacía vuelve al valor por defecto: se guarda como nil (se borra la fila).
func (p Patch) values() (map[string]*string, error) {
	out := map[string]*string{}
	for _, f := range fields {
		switch v := f.patch(&p).(type) {
		case *string:
			if v == nil {
				continue
			}
			norm, err := validate(f.key, *v)
			if err != nil {
				return nil, err
			}
			out[f.key] = nil
			if norm != "" {
				b, _ := json.Marshal(norm)
				out[f.key] = new(string(b))
			}
		case *bool:
			if v != nil {
				b, _ := json.Marshal(*v)
				out[f.key] = new(string(b))
			}
		}
	}
	return out, nil
}

func validate(key, v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	limit := map[string]int{"branding.title": maxTitle, "branding.company": maxCompany, "branding.login_message": maxLoginMessage}
	if n, ok := limit[key]; ok {
		// Texto plano de una línea (salvo el mensaje del login): sin caracteres de control.
		for _, r := range v {
			if r < 0x20 && (key != "branding.login_message" || r != '\n') {
				return "", ErrInvalid
			}
		}
		if utf8.RuneCountInString(v) > n {
			return "", ErrInvalid
		}
		return v, nil
	}
	c, ok := NormalizeColor(v)
	if !ok {
		return "", ErrInvalid
	}
	return c, nil
}

type store interface {
	SiteSettings(ctx context.Context, prefix string) (map[string]string, error)
	SetSiteSettings(ctx context.Context, values map[string]*string, by string) error
	SiteAssetVersions(ctx context.Context) (map[string]string, error)
	SiteAsset(ctx context.Context, kind, variant string) (repo.SiteAsset, error)
	ReplaceSiteAsset(ctx context.Context, kind string, variants []repo.SiteAsset) error
	DeleteSiteAsset(ctx context.Context, kind string) (string, error)
}

type Service struct {
	store  store
	audit  *audit.Recorder
	fixed  map[string]*string // claves del archivo o de las variables, ya validadas
	locked []string
}

// NewService valida lo que viene del archivo de configuración: un color mal escrito ahí impide
// arrancar, en vez de quedar ignorado.
func NewService(s store, rec *audit.Recorder, fromConfig Patch) (*Service, error) {
	fixed, err := fromConfig.values()
	if err != nil {
		return nil, errors.New("branding in the configuration file: titles up to 40 characters, colors as #RRGGBB")
	}
	locked := make([]string, 0, len(fixed))
	for k := range fixed {
		locked = append(locked, strings.TrimPrefix(k, "branding."))
	}
	slices.Sort(locked)
	return &Service{store: s, audit: rec, fixed: fixed, locked: locked}, nil
}

// Site es la marca vigente con lo que la interfaz necesita para dibujarla.
type Site struct {
	Settings
	Locked  []string          `json:"locked"`  // claves fijadas en el archivo: el panel no las cambia
	Palette Palette           `json:"palette"` // variables CSS y avisos
	Assets  map[string]string `json:"assets"`  // logo, logo_dark, favicon e icons → versión (sha256 corto)
}

// Site arma la marca: valores por defecto, encima lo del panel y encima lo del archivo.
func (s *Service) Site(ctx context.Context) (Site, error) {
	rows, err := s.store.SiteSettings(ctx, "branding.")
	if err != nil {
		return Site{}, err
	}
	for k, v := range s.fixed {
		if v == nil {
			delete(rows, k)
		} else {
			rows[k] = *v
		}
	}
	set := Defaults
	for _, f := range fields {
		if raw, ok := rows[f.key]; ok {
			if err := f.set(&set, json.RawMessage(raw)); err != nil {
				return Site{}, err
			}
		}
	}
	versions, err := s.store.SiteAssetVersions(ctx)
	if err != nil {
		return Site{}, err
	}
	// La versión es el comienzo del sha256: cambia la URL cuando cambia el archivo.
	assets := map[string]string{}
	for _, k := range Kinds {
		if v := versions[k+"/original"]; v != "" {
			assets[k] = v[:12]
		}
	}
	if v := cmp.Or(versions["favicon/ico"], versions["logo/ico"]); v != "" {
		assets["icons"] = v[:12]
	}
	return Site{Settings: set, Locked: s.locked, Assets: assets,
		Palette: Derive(Colors{Primary: set.ColorPrimary, Header: set.ColorHeader, Sidebar: set.ColorSidebar})}, nil
}

// Update guarda lo que cambió el superadmin y lo registra con las claves, sin los valores
// (§15.1.5, §11.2). Tocar una clave fijada en el archivo es un error: no tendría efecto.
func (s *Service) Update(ctx context.Context, actorID string, p Patch) (Site, error) {
	values, err := p.values()
	if err != nil {
		return Site{}, err
	}
	before, err := s.Site(ctx)
	if err != nil {
		return Site{}, err
	}
	for k := range values {
		if _, ok := s.fixed[k]; ok {
			return Site{}, ErrLocked
		}
	}
	if err := s.store.SetSiteSettings(ctx, values, actorID); err != nil {
		return Site{}, err
	}
	after, err := s.Site(ctx)
	if err != nil {
		return Site{}, err
	}
	if changed := changedKeys(before.Settings, after.Settings); len(changed) > 0 {
		s.audit.Record(ctx, audit.Event{Action: audit.SiteBrandingUpdate, TargetType: "site", Meta: map[string]any{"keys": changed}})
	}
	return after, nil
}

func changedKeys(a, b Settings) []string {
	var ja, jb map[string]any
	ra, _ := json.Marshal(a)
	rb, _ := json.Marshal(b)
	json.Unmarshal(ra, &ja)
	json.Unmarshal(rb, &jb)
	var out []string
	for k, v := range ja {
		if v != jb[k] {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// Preview calcula la paleta de unos colores sin guardarlos: la vista previa del panel usa la
// misma cuenta que la página (§15.1.2). Light viene completa, con los tokens de Kudawiki donde la
// marca no cambia nada, para que la vista previa no herede los colores ya guardados.
func Preview(c Colors) (Palette, error) {
	var ok bool
	if c.Primary, ok = NormalizeColor(c.Primary); !ok {
		return Palette{}, ErrInvalid
	}
	if c.Header, ok = NormalizeColor(c.Header); !ok {
		return Palette{}, ErrInvalid
	}
	if c.Sidebar != "" {
		if c.Sidebar, ok = NormalizeColor(c.Sidebar); !ok {
			return Palette{}, ErrInvalid
		}
	}
	p := Derive(c)
	full := maps.Clone(lightDefaults)
	maps.Copy(full, p.Light)
	p.Light = full
	return p, nil
}

// Upload guarda el logo, el logo oscuro o el favicon con sus variantes y devuelve los avisos.
func (s *Service) Upload(ctx context.Context, actorID, kind string, data []byte) ([]Warning, error) {
	if !slices.Contains(Kinds, kind) {
		return nil, repo.ErrNotFound
	}
	site, err := s.Site(ctx)
	if err != nil {
		return nil, err
	}
	// Va en la barra: el logo oscuro sobre una barra oscura, o el normal si no hay otro.
	dark := HeaderIsDark(site.ColorHeader)
	_, hasDark := site.Assets["logo_dark"]
	onBar := kind == "logo_dark" && dark || kind == "logo" && !(dark && hasDark)
	p, err := process(kind, data, site.ColorHeader, onBar)
	if err != nil {
		return nil, err
	}
	rows := make([]repo.SiteAsset, len(p.assets))
	for i, a := range p.assets {
		sum := sha256.Sum256(a.data)
		rows[i] = repo.SiteAsset{Kind: kind, Variant: a.variant, MimeType: a.mime, Data: a.data, SHA256: hex.EncodeToString(sum[:]), UploadedBy: actorID}
	}
	if err := s.store.ReplaceSiteAsset(ctx, kind, rows); err != nil {
		return nil, err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.SiteAssetUpload, TargetType: "site_asset", TargetID: kind,
		Meta: map[string]any{"sha256": rows[0].SHA256}})
	return p.warnings, nil
}

// DeleteAsset vuelve ese archivo al de Kudawiki (o, para el favicon, al que sale del logo).
func (s *Service) DeleteAsset(ctx context.Context, kind string) error {
	if !slices.Contains(Kinds, kind) {
		return repo.ErrNotFound
	}
	sum, err := s.store.DeleteSiteAsset(ctx, kind)
	if err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.SiteAssetDelete, TargetType: "site_asset", TargetID: kind, Meta: map[string]any{"sha256": sum}})
	return nil
}

// publicFiles son los archivos de /_site (§14.1): nombre público → (archivo, variante). Los
// íconos salen del favicon y, si no se subió, del logo (§15.1.1).
var publicFiles = map[string][2]string{
	"logo":         {"logo", "original"},
	"logo-dark":    {"logo_dark", "original"},
	"favicon.ico":  {"favicon", "ico"},
	"icon-32.png":  {"favicon", "png_32"},
	"icon-180.png": {"favicon", "png_180"},
	"icon-512.png": {"favicon", "png_512"},
}

// File devuelve un archivo público de la marca; repo.ErrNotFound si la empresa no lo subió.
func (s *Service) File(ctx context.Context, name string) (repo.SiteAsset, error) {
	f, ok := publicFiles[name]
	if !ok {
		return repo.SiteAsset{}, repo.ErrNotFound
	}
	a, err := s.store.SiteAsset(ctx, f[0], f[1])
	if errors.Is(err, repo.ErrNotFound) && f[0] == "favicon" {
		return s.store.SiteAsset(ctx, "logo", f[1])
	}
	return a, err
}
