package httpapi

import (
	"bytes"
	"encoding/json"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/athomo001/kudawiki/internal/branding"
	"github.com/go-chi/chi/v5"
)

// Marca de la empresa (SPEC §15.1): el panel del superadmin, los archivos públicos de /_site y
// el index.html que ya sale con los colores de la empresa.

func (a api) siteAdminRoutes(r chi.Router) {
	r.Get("/site", a.getSite)
	r.Patch("/site", a.patchSite)
	r.Post("/site/preview", a.previewSite)
	r.Put("/site/assets/{kind}", a.uploadSiteAsset)
	r.Delete("/site/assets/{kind}", a.deleteSiteAsset)
}

func (a api) getSite(w http.ResponseWriter, r *http.Request) {
	site, err := a.Branding.Site(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, site)
}

// patchSite cambia las claves que vienen; "" vuelve una al valor de Kudawiki.
func (a api) patchSite(w http.ResponseWriter, r *http.Request) {
	var p branding.Patch
	if !a.decode(w, r, &p) {
		return
	}
	site, err := a.Branding.Update(r.Context(), currentUser(r).ID, p)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, site)
}

// previewSite calcula la paleta sin guardar, para la vista previa en vivo del panel (§15.1.2):
// la misma cuenta que arma el index.html, en un solo lugar.
func (a api) previewSite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Primary string `json:"color_primary"`
		Header  string `json:"color_header"`
		Sidebar string `json:"color_sidebar"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	p, err := branding.Preview(branding.Colors{Primary: in.Primary, Header: in.Header, Sidebar: in.Sidebar})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, p)
}

// uploadSiteAsset recibe el archivo en el campo "file" y devuelve los avisos (§15.1.3) y la
// marca con la versión nueva.
func (a api) uploadSiteAsset(w http.ResponseWriter, r *http.Request) {
	data, ok := a.uploadedFile(w, r, branding.MaxAssetBytes)
	if !ok {
		return
	}
	warnings, err := a.Branding.Upload(r.Context(), currentUser(r).ID, chi.URLParam(r, "kind"), data)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	site, err := a.Branding.Site(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, map[string]any{"warnings": warnings, "site": site})
}

func (a api) deleteSiteAsset(w http.ResponseWriter, r *http.Request) {
	if err := a.Branding.DeleteAsset(r.Context(), chi.URLParam(r, "kind")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// siteFile sirve el logo, el favicon y sus variantes sin sesión: la pantalla de login los
// necesita (§14.1). Un SVG abierto directo no ejecuta nada (CSP sandbox, nosniff); no-cache con
// ETag = sha256: uno nuevo se ve al instante y uno igual no se vuelve a bajar (§15.1.3).
func (a api) siteFile(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "manifest.webmanifest" {
		a.manifest(w, r)
		return
	}
	f, err := a.Branding.File(r.Context(), name)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", f.MimeType)
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	h.Set("Cache-Control", "public, no-cache")
	h.Set("ETag", `"`+f.SHA256+`"`)
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(f.Data))
}

// manifest es el de la app instalada o el acceso directo, con el título y los íconos de la
// empresa (§15.1.4).
func (a api) manifest(w http.ResponseWriter, r *http.Request) {
	site, err := a.Branding.Site(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	icons := []map[string]string{
		{"src": "/favicon.svg", "sizes": "any", "type": "image/svg+xml"},
		{"src": "/apple-touch-icon.png", "sizes": "180x180", "type": "image/png"},
	}
	if v := site.Assets["icons"]; v != "" {
		icons = []map[string]string{
			{"src": "/_site/icon-180.png?v=" + v, "sizes": "180x180", "type": "image/png"},
			{"src": "/_site/icon-512.png?v=" + v, "sizes": "512x512", "type": "image/png"},
		}
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "application/manifest+json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"name": site.Title, "short_name": site.Title, "start_url": "/", "display": "standalone",
		"background_color": "#ffffff", "theme_color": site.ColorHeader, "icons": icons,
	})
}

// Marcadores de ui/index.html: lo que hay entre ellos (título e íconos de Kudawiki, que sirven
// en desarrollo con Vite) lo reemplaza la marca de la empresa.
const (
	brandStart = "<!-- kw:brand -->"
	brandEnd   = "<!-- /kw:brand -->"
)

// brandPage pone la marca en index.html, así la primera pintura ya sale con los colores de la
// empresa, sin un parpadeo azul (§15.1.2). El <style> va al final del <head>, después de la hoja
// de la interfaz, para ganarle a sus valores por defecto; la CSP lo deja pasar por su nonce.
// resetAvailable dice si el login muestra "¿Olvidaste tu contraseña?" (§8.5.1).
func brandPage(page []byte, site branding.Site, resetAvailable bool, nonce string) []byte {
	head := brandHead(site)
	if i, j := bytes.Index(page, []byte(brandStart)), bytes.Index(page, []byte(brandEnd)); i >= 0 && j > i {
		page = append(page[:i:i], append([]byte(head), page[j+len(brandEnd):]...)...)
	} else {
		page = bytes.Replace(page, []byte("<head>"), []byte("<head>"+head), 1)
	}
	// Los datos van como JSON, que no se ejecuta; json.Marshal escapa <, > y &, así el texto de
	// la empresa no puede cerrar la etiqueta.
	public, _ := json.Marshal(map[string]any{
		"title": site.Title, "company": site.Company, "login_message": site.LoginMessage, "show_powered_by": site.ShowPoweredBy,
		"header_dark": site.Palette.HeaderDark,
		"logo":        site.Assets["logo"], "logo_dark": site.Assets["logo_dark"],
		"password_reset": resetAvailable,
	})
	tail := `<script type="application/json" id="kw-site">` + string(public) + `</script>`
	if css := site.Palette.CSS(); css != "" {
		tail += `<style nonce="` + nonce + `">` + css + `</style>`
	}
	return bytes.Replace(page, []byte("</head>"), []byte(tail+"</head>"), 1)
}

func brandHead(site branding.Site) string {
	var b strings.Builder
	b.WriteString("<title>" + html.EscapeString(site.Title) + "</title>")
	if v := site.Assets["icons"]; v != "" {
		b.WriteString(`<link rel="icon" href="/_site/favicon.ico?v=` + v + `" sizes="32x32" />` +
			`<link rel="icon" type="image/png" href="/_site/icon-32.png?v=` + v + `" />` +
			`<link rel="apple-touch-icon" href="/_site/icon-180.png?v=` + v + `" />`)
	} else {
		b.WriteString(`<link rel="icon" href="/favicon.ico" sizes="32x32" />` +
			`<link rel="icon" type="image/svg+xml" href="/favicon.svg" />` +
			`<link rel="apple-touch-icon" href="/apple-touch-icon.png" />`)
	}
	b.WriteString(`<meta name="theme-color" content="` + site.ColorHeader + `" />`)
	b.WriteString(`<link rel="manifest" href="/_site/manifest.webmanifest" />`)
	return b.String()
}
