package branding

import (
	"cmp"
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Colores por defecto: los del logo y del diseño aprobado (SPEC §15). Mientras la empresa no
// los cambie, la interfaz usa sus tokens tal cual y no se emite ninguna variable.
const (
	DefaultPrimary = "#1d4f91"
	DefaultHeader  = "#15171c"
)

// Fondos contra los que se mide el contraste: el del contenido en claro y en oscuro (app.css).
const (
	lightBG = "#ffffff"
	darkBG  = "#0d1117"
	// Texto sobre la barra y el menú: blanco o casi negro, el que más contraste dé (§15.1.2).
	onDark  = "#ffffff"
	onLight = "#1f2328"
)

// lightDefaults son los tokens de app.css (:root) que la marca puede cambiar. CSS() no los
// necesita, pero la vista previa del panel sí: si se vuelve a un color de Kudawiki no se emite
// nada, y la vista previa igual tiene que tapar el color guardado. TestDefaultsMatchCSS los
// compara con app.css.
var lightDefaults = map[string]string{
	"--kw-accent": "#1d4f91", "--kw-on-accent": "#fff", "--kw-link": "#1a5fb4", "--kw-link-hover": "#0f4384",
	"--kw-focus-ring": "#1a5fb4", "--kw-cm-selection": "#cfe0f7", "--kw-cm-active": "#eef4fc",
	"--kw-sidebar": "var(--kw-accent)", "--kw-on-sidebar": "var(--kw-on-accent)", "--kw-sidebar-muted": "#c9dbf3",
	"--kw-sidebar-hover": "rgb(255 255 255 / 0.1)", "--kw-sidebar-current": "rgb(255 255 255 / 0.16)",
	"--kw-sidebar-focus": "#fff", "--kw-level-on-sidebar": "#ffd8a8",
	"--kw-topbar-bg": "#15171c", "--kw-topbar-text": "#e8eaed", "--kw-topbar-muted": "#9aa0a6", "--kw-topbar-line": "#3a3f47",
	"--kw-topbar-field": "#23262d", "--kw-topbar-field-line": "#343941", "--kw-topbar-strong-line": "#454b54",
	"--kw-topbar-hover": "rgb(255 255 255 / 0.1)", "--kw-topbar-focus": "#8ab4f8", "--kw-topbar-brand": "#fff",
	"--kw-topbar-action": "#3b82f6", "--kw-topbar-action-hover": "#2f6fd8", "--kw-on-topbar-action": "#fff",
	"--kw-topbar-logo-back": "#3d5f8f", "--kw-topbar-logo-mid": "#3b82f6", "--kw-topbar-logo-front": "#6ea8fe",
	"--kw-topbar-logo-k": "#15171c",
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// NormalizeColor valida un color #RRGGBB y lo deja en minúsculas. Solo hex: nada de nombres,
// rgb() ni otra cosa que pudiera llevar CSS al <style> de la página (§15.1.5).
func NormalizeColor(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if !hexColor.MatchString(s) {
		return "", false
	}
	return strings.ToLower(s), true
}

// Colors son los tres colores que elige la empresa; Sidebar vacío es el principal.
type Colors struct {
	Primary, Header, Sidebar string
}

// Warning es un aviso para el panel; Code se traduce en la interfaz.
type Warning struct {
	Code  string `json:"code"`
	Color string `json:"color,omitempty"`
}

// Palette son las variables CSS que cambian respecto de los tokens por defecto (app.css), para
// el tema claro y para el oscuro, y los avisos.
type Palette struct {
	Light      map[string]string `json:"light"`
	Dark       map[string]string `json:"dark"`
	Warnings   []Warning         `json:"warnings"`
	HeaderDark bool              `json:"header_dark"` // sobre la barra va el logo para fondo oscuro
}

// Derive arma la paleta a partir de los colores de la empresa (§15.1.2). Los tonos se derivan en
// OKLCH, así cualquier color da una paleta pareja; el texto sobre la barra y el menú se elige por
// contraste WCAG; un principal que no llega a 4.5:1 sobre blanco no se usa para los enlaces, que
// pasan a una variante más oscura del mismo tono. Los niveles, avisos, diffs y estados no toman
// el color de la marca: tienen significado.
func Derive(c Colors) Palette {
	p := Palette{Light: map[string]string{}, Dark: map[string]string{}, Warnings: []Warning{}, HeaderDark: HeaderIsDark(c.Header)}
	if c.Primary != DefaultPrimary {
		p.primary(c.Primary)
	}
	sidebar := cmp.Or(c.Sidebar, c.Primary)
	if sidebar != DefaultPrimary {
		p.sidebar(sidebar)
	}
	if c.Header != DefaultHeader {
		p.header(c.Header)
	}
	return p
}

// primary: botones, pestañas, enlaces, selección y foco (§15.1.1).
func (p *Palette) primary(c string) {
	link := darkenUntil(c, lightBG, 4.5)
	if link != c {
		p.Warnings = append(p.Warnings, Warning{Code: "link_darkened", Color: link})
	}
	p.Light["--kw-accent"] = c
	p.Light["--kw-on-accent"] = textOn(c)
	p.Light["--kw-link"] = link
	p.Light["--kw-link-hover"] = shiftL(link, -0.08)
	p.Light["--kw-focus-ring"] = link
	p.Light["--kw-cm-selection"] = tone(c, 0.90, 0.35)
	p.Light["--kw-cm-active"] = tone(c, 0.975, 0.12)
	p.Light["--kw-topbar-action"] = c
	p.Light["--kw-topbar-action-hover"] = shiftL(c, -0.06)
	p.Light["--kw-on-topbar-action"] = textOn(c)
	darkLink := lightenUntil(c, darkBG, 4.5)
	p.Dark["--kw-link"] = darkLink
	p.Dark["--kw-link-hover"] = shiftL(darkLink, 0.08)
	p.Dark["--kw-focus-ring"] = darkLink
	p.Dark["--kw-cm-selection"] = tone(c, 0.40, 0.5)
	p.Dark["--kw-cm-active"] = tone(c, 0.22, 0.25)
}

// sidebar: el menú lateral. Sobre uno claro, los resaltes pasan de blanco a negro translúcido y
// N2/N3 usan un ámbar oscuro que se lea.
func (p *Palette) sidebar(c string) {
	on := textOn(c)
	p.Light["--kw-sidebar"] = c
	p.Light["--kw-on-sidebar"] = on
	p.Light["--kw-sidebar-muted"] = muted(on, c)
	if on == onLight {
		p.Light["--kw-sidebar-hover"] = "rgb(0 0 0 / 0.07)"
		p.Light["--kw-sidebar-current"] = "rgb(0 0 0 / 0.1)"
		p.Light["--kw-sidebar-focus"] = onLight
		p.Light["--kw-level-on-sidebar"] = "#7a3d00"
	}
}

// header: la barra superior y la cabecera del editor. Sobre una barra clara va el logo de
// Kudawiki para fondo claro (spec/logo/).
func (p *Palette) header(c string) {
	on := textOn(c)
	p.Light["--kw-topbar-bg"] = c
	p.Light["--kw-topbar-text"] = on
	p.Light["--kw-topbar-muted"] = muted(on, c)
	p.Light["--kw-topbar-line"] = mix(on, c, 0.78)
	p.Light["--kw-topbar-strong-line"] = mix(on, c, 0.70)
	p.Light["--kw-topbar-field"] = mix(on, c, 0.92)
	p.Light["--kw-topbar-field-line"] = mix(on, c, 0.82)
	if on == onLight {
		p.Light["--kw-topbar-brand"] = onLight
		p.Light["--kw-topbar-hover"] = "rgb(0 0 0 / 0.07)"
		p.Light["--kw-topbar-focus"] = "#1a5fb4"
		p.Light["--kw-topbar-logo-back"] = "#bcd6fb"
		p.Light["--kw-topbar-logo-mid"] = "#3b82f6"
		p.Light["--kw-topbar-logo-front"] = "#1d4f91"
		p.Light["--kw-topbar-logo-k"] = "#ffffff"
	}
}

// HeaderIsDark dice si sobre la barra va texto claro: ahí se usa el logo para fondo oscuro.
func HeaderIsDark(header string) bool { return textOn(header) == onDark }

// CSS arma el contenido del <style> de index.html. Los valores son hex validados o rgb() de
// valores fijos, y las claves son constantes: nada que venga de una persona llega como texto.
func (p Palette) CSS() string {
	var b strings.Builder
	block := func(vars map[string]string) {
		b.WriteString(":root{")
		for _, k := range slices.Sorted(maps.Keys(vars)) {
			fmt.Fprintf(&b, "%s:%s;", k, vars[k])
		}
		b.WriteString("}")
	}
	if len(p.Light) > 0 {
		block(p.Light)
	}
	if len(p.Dark) > 0 {
		b.WriteString("@media (prefers-color-scheme: dark){")
		block(p.Dark)
		b.WriteString("}")
	}
	return b.String()
}

// textOn elige blanco o casi negro, el que más contraste tenga contra bg.
func textOn(bg string) string {
	if contrast(onDark, bg) >= contrast(onLight, bg) {
		return onDark
	}
	return onLight
}

// muted es un texto secundario: el texto acercado al fondo, sin bajar de 4.5:1 (AA, §15).
func muted(text, bg string) string {
	for t := 0.35; t > 0; t -= 0.05 {
		if m := mix(text, bg, t); contrast(m, bg) >= 4.5 {
			return m
		}
	}
	return text
}

// darkenUntil baja la luminosidad (OKLCH) del color hasta alcanzar el contraste pedido contra bg.
func darkenUntil(c, bg string, ratio float64) string {
	return stepUntil(c, bg, ratio, -0.01)
}

func lightenUntil(c, bg string, ratio float64) string {
	return stepUntil(c, bg, ratio, 0.01)
}

func stepUntil(c, bg string, ratio, step float64) string {
	l, ch, h := toOKLCH(c)
	out := c
	for contrast(out, bg) < ratio && l > 0 && l < 1 {
		l += step
		out = fromOKLCH(l, ch, h)
	}
	return out
}

func shiftL(c string, d float64) string {
	l, ch, h := toOKLCH(c)
	return fromOKLCH(math.Max(0, math.Min(1, l+d)), ch, h)
}

// tone es el mismo matiz con otra luminosidad y menos croma: fondos suaves de selección.
func tone(c string, l, chroma float64) string {
	_, ch, h := toOKLCH(c)
	return fromOKLCH(l, ch*chroma, h)
}

// mix interpola en OKLab: t=0 es a, t=1 es b.
func mix(a, b string, t float64) string {
	la, aa, ba := toOKLab(rgbOf(a))
	lb, ab, bb := toOKLab(rgbOf(b))
	return hexOf(fromOKLab(la+(lb-la)*t, aa+(ab-aa)*t, ba+(bb-ba)*t))
}

// contrast es la razón de contraste WCAG 2.
func contrast(a, b string) float64 {
	la, lb := luminance(rgbOf(a)), luminance(rgbOf(b))
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

type rgb [3]float64 // sRGB, 0..1

func rgbOf(hex string) rgb {
	n, _ := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
	return rgb{float64(n>>16&0xff) / 255, float64(n>>8&0xff) / 255, float64(n&0xff) / 255}
}

func hexOf(c rgb) string {
	ch := func(v float64) int { return int(math.Round(math.Max(0, math.Min(1, v)) * 255)) }
	return fmt.Sprintf("#%02x%02x%02x", ch(c[0]), ch(c[1]), ch(c[2]))
}

func toLinear(v float64) float64 {
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func fromLinear(v float64) float64 {
	if v <= 0.0031308 {
		return v * 12.92
	}
	return 1.055*math.Pow(v, 1/2.4) - 0.055
}

func luminance(c rgb) float64 {
	return 0.2126*toLinear(c[0]) + 0.7152*toLinear(c[1]) + 0.0722*toLinear(c[2])
}

// OKLab de Björn Ottosson (https://bottosson.github.io/posts/oklab/).
func toOKLab(c rgb) (l, a, b float64) {
	r, g, bl := toLinear(c[0]), toLinear(c[1]), toLinear(c[2])
	lc := math.Cbrt(0.4122214708*r + 0.5363325363*g + 0.0514459929*bl)
	mc := math.Cbrt(0.2119034982*r + 0.6806995451*g + 0.1073969566*bl)
	sc := math.Cbrt(0.0883024619*r + 0.2817188376*g + 0.6299787005*bl)
	return 0.2104542553*lc + 0.7936177850*mc - 0.0040720468*sc,
		1.9779984951*lc - 2.4285922050*mc + 0.4505937099*sc,
		0.0259040371*lc + 0.7827717662*mc - 0.8086757660*sc
}

func fromOKLab(l, a, b float64) rgb {
	lc := l + 0.3963377774*a + 0.2158037573*b
	mc := l - 0.1055613458*a - 0.0638541728*b
	sc := l - 0.0894841775*a - 1.2914855480*b
	lc, mc, sc = lc*lc*lc, mc*mc*mc, sc*sc*sc
	return rgb{
		fromLinear(4.0767416621*lc - 3.3077115913*mc + 0.2309699292*sc),
		fromLinear(-1.2684380046*lc + 2.6097574011*mc - 0.3413193965*sc),
		fromLinear(-0.0041960863*lc - 0.7034186147*mc + 1.7076147010*sc),
	}
}

func toOKLCH(hex string) (l, c, h float64) {
	l, a, b := toOKLab(rgbOf(hex))
	return l, math.Hypot(a, b), math.Atan2(b, a)
}

// fromOKLCH baja el croma hasta que el color entra en sRGB, en vez de recortar cada canal: así
// el matiz no cambia.
func fromOKLCH(l, c, h float64) string {
	for {
		out := fromOKLab(l, c*math.Cos(h), c*math.Sin(h))
		if inGamut(out) || c < 0.001 {
			return hexOf(out)
		}
		c *= 0.95
	}
}

func inGamut(c rgb) bool {
	for _, v := range c {
		if v < -0.0005 || v > 1.0005 {
			return false
		}
	}
	return true
}
