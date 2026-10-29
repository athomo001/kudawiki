package branding

import (
	"maps"
	"math"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestContrastAndRoundTrip(t *testing.T) {
	if c := contrast("#ffffff", "#000000"); math.Abs(c-21) > 0.01 {
		t.Errorf("contrast white/black = %.2f, want 21", c)
	}
	// Pasar a OKLCH y volver no mueve un color dentro de sRGB.
	for _, c := range []string{"#1d4f91", "#15171c", "#f5c400", "#ffffff", "#9e1c3f", "#00a86b"} {
		if got := fromOKLCH(toOKLCH(c)); got != c {
			t.Errorf("round trip %s = %s", c, got)
		}
	}
}

// Con los colores de Kudawiki no se emite nada: rigen los tokens del diseño aprobado.
func TestDefaultsEmitNothing(t *testing.T) {
	p := Derive(Colors{Primary: DefaultPrimary, Header: DefaultHeader})
	if len(p.Light)+len(p.Dark)+len(p.Warnings) != 0 || p.CSS() != "" {
		t.Errorf("defaults = %+v, css %q", p, p.CSS())
	}
}

// La vista previa del panel completa con lightDefaults: si app.css cambia un token y Go no, el
// panel mostraría un color que la página no usa. Y toda clave que Derive emite tiene que tener
// su valor por defecto, o "Restablecer a Kudawiki" no se vería en la vista previa.
func TestDefaultsMatchCSS(t *testing.T) {
	css, err := os.ReadFile("../../ui/src/app.css")
	if err != nil {
		t.Fatal(err)
	}
	_, root, _ := strings.Cut(string(css), ":root {")
	root, _, _ = strings.Cut(root, "\n}")
	tokens := map[string]string{}
	for _, m := range regexp.MustCompile(`(--kw-[\w-]+):\s*([^;]+);`).FindAllStringSubmatch(root, -1) {
		tokens[m[1]] = m[2]
	}
	for k, v := range lightDefaults {
		if tokens[k] != v {
			t.Errorf("%s = %q in Go, %q in app.css", k, v, tokens[k])
		}
	}
	// Barra y menú claros: así Derive emite también las claves que solo van sobre fondo claro.
	for k := range Derive(Colors{Primary: "#f5c400", Header: "#f0f0f0"}).Light {
		if _, ok := lightDefaults[k]; !ok {
			t.Errorf("Derive emits %s without a default", k)
		}
	}
	p, err := Preview(Colors{Primary: DefaultPrimary, Header: DefaultHeader})
	if err != nil || !maps.Equal(p.Light, lightDefaults) {
		t.Errorf("preview with Kudawiki colors = %v, %v", p.Light, err)
	}
}

func TestDeriveContrast(t *testing.T) {
	for _, tc := range []struct {
		name       string
		c          Colors
		wantDarken bool
	}{
		{"amarillo claro", Colors{Primary: "#f5c400", Header: "#f0f0f0"}, true},
		{"verde oscuro", Colors{Primary: "#0b3d2e", Header: "#0b3d2e", Sidebar: "#e8f5e9"}, false},
		{"rojo", Colors{Primary: "#c8102e", Header: "#15171c"}, false},
		{"celeste", Colors{Primary: "#7cc6fe", Header: "#003366"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := Derive(tc.c)
			link := p.Light["--kw-link"]
			if contrast(link, lightBG) < 4.5 {
				t.Errorf("link %s on white = %.2f", link, contrast(link, lightBG))
			}
			if dl := p.Dark["--kw-link"]; contrast(dl, darkBG) < 4.5 {
				t.Errorf("dark link %s = %.2f", dl, contrast(dl, darkBG))
			}
			if darkened := len(p.Warnings) == 1 && p.Warnings[0].Code == "link_darkened" && p.Warnings[0].Color == link; darkened != tc.wantDarken {
				t.Errorf("warnings = %+v, want darkened %v", p.Warnings, tc.wantDarken)
			}
			// Texto sobre el acento, la barra y el menú: AA para texto chico.
			for bg, fg := range map[string]string{
				"--kw-accent": "--kw-on-accent", "--kw-topbar-bg": "--kw-topbar-text", "--kw-sidebar": "--kw-on-sidebar",
			} {
				if b, f := p.Light[bg], p.Light[fg]; b != "" && contrast(f, b) < 4.5 {
					t.Errorf("%s %s on %s = %.2f", fg, f, b, contrast(f, b))
				}
			}
			for _, k := range []string{"--kw-topbar-muted", "--kw-sidebar-muted"} {
				bg := map[string]string{"--kw-topbar-muted": "--kw-topbar-bg", "--kw-sidebar-muted": "--kw-sidebar"}[k]
				if m := p.Light[k]; m != "" && contrast(m, p.Light[bg]) < 4.5 {
					t.Errorf("%s %s = %.2f", k, m, contrast(m, p.Light[bg]))
				}
			}
		})
	}
}

func TestSidebarFollowsPrimary(t *testing.T) {
	p := Derive(Colors{Primary: "#c8102e", Header: DefaultHeader})
	if p.Light["--kw-sidebar"] != "#c8102e" {
		t.Errorf("sidebar = %q, want the primary", p.Light["--kw-sidebar"])
	}
	// Menú propio con el principal de siempre: solo cambia el menú.
	p = Derive(Colors{Primary: DefaultPrimary, Header: DefaultHeader, Sidebar: "#f2f2f2"})
	if p.Light["--kw-accent"] != "" || p.Light["--kw-sidebar"] != "#f2f2f2" || p.Light["--kw-on-sidebar"] != onLight || p.Light["--kw-level-on-sidebar"] == "" {
		t.Errorf("light sidebar = %+v", p.Light)
	}
	// Una barra clara lleva texto oscuro y el logo para fondo claro.
	p = Derive(Colors{Primary: DefaultPrimary, Header: "#f7f7f7"})
	if p.Light["--kw-topbar-text"] != onLight || p.Light["--kw-topbar-logo-front"] == "" || HeaderIsDark("#f7f7f7") {
		t.Errorf("light header = %+v", p.Light)
	}
}

// El <style> de index.html solo puede llevar variables con valores de colores: nada que cierre
// la etiqueta ni inyecte otra regla.
func TestCSSIsOnlyVariables(t *testing.T) {
	css := Derive(Colors{Primary: "#f5c400", Header: "#f0f0f0", Sidebar: "#123456"}).CSS()
	if !regexp.MustCompile(`^(:root\{(--kw-[a-z-]+:(#[0-9a-f]{6}|rgb\([0-9 ./]+\));)+\}|@media \(prefers-color-scheme: dark\)\{|\})+$`).MatchString(css) {
		t.Errorf("unexpected css: %s", css)
	}
}

func TestNormalizeColor(t *testing.T) {
	for in, want := range map[string]string{"#1D4F91": "#1d4f91", " #abcdef ": "#abcdef", "red": "", "#abc": "", "#1d4f91;}": "", "url(x)": ""} {
		got, ok := NormalizeColor(in)
		if got != want || ok != (want != "") {
			t.Errorf("NormalizeColor(%q) = %q, %v", in, got, ok)
		}
	}
}
