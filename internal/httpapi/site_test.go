package httpapi

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Un logo que oksvg sabe dibujar (el de Kudawiki, simplificado).
const testLogoSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48" onload="alert(1)">` +
	`<rect x="3" y="15" width="30" height="30" rx="4" fill="#C8102E"/><script>alert(2)</script></svg>`

func (f fixture) uploadSite(who, kind string, data []byte) *httptest.ResponseRecorder {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("file", "logo")
	part.Write(data)
	mw.Close()
	r := httptest.NewRequest("PUT", "/api/admin/site/assets/"+kind, &body)
	r.Header.Set("Origin", origin)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	if c := f.cookies[who]; c != nil {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	return w
}

// §15.1: la marca la cambia solo el superadmin; a cualquier otra persona el panel le responde el
// 404 uniforme. Los archivos de /_site y el index.html son públicos: el login los necesita.
func TestBranding(t *testing.T) {
	f := newFixture(t)
	for _, who := range []string{"adminTI", "editorTI", "viewerTI", "outsider", ""} {
		for _, req := range [][3]string{
			{"GET", "/api/admin/site", ""}, {"PATCH", "/api/admin/site", `{"title":"X"}`},
			{"POST", "/api/admin/site/preview", `{"color_primary":"#c8102e","color_header":"#15171c"}`},
			{"DELETE", "/api/admin/site/assets/logo", ""},
		} {
			want := http.StatusNotFound
			if who == "" {
				want = http.StatusUnauthorized
			}
			if w := f.do(who, req[0], req[1], req[2]); w.Code != want {
				t.Errorf("%s %s %s: %d, want %d", who, req[0], req[1], w.Code, want)
			}
		}
		if w := f.uploadSite(who, "logo", []byte(testLogoSVG)); w.Code != http.StatusNotFound && w.Code != http.StatusUnauthorized {
			t.Errorf("%s uploads: %d", who, w.Code)
		}
	}

	// Sin marca propia: el index.html de Kudawiki, sin <style> y con sus íconos.
	page := f.do("", "GET", "/", "").Body.String()
	if !strings.Contains(page, "<title>Kudawiki</title>") || strings.Contains(page, "<style") || !strings.Contains(page, `href="/favicon.svg"`) ||
		strings.Count(page, "<title>") != 1 {
		t.Errorf("default index: %s", page)
	}

	w := f.do("super", "PATCH", "/api/admin/site", `{"title":"Wiki </title><script>alert(1)</script> de ACME S.A."}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("title over 40 characters: %d", w.Code)
	}
	w = f.do("super", "PATCH", "/api/admin/site", `{"title":"Wiki <ACME>","color_primary":"#F5C400","login_message":"</script><b>hola</b>"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"code":"link_darkened"`) {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}
	if w := f.do("super", "PATCH", "/api/admin/site", `{"color_header":"rojo"}`); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad color: %d", w.Code)
	}
	if w := f.do("super", "PATCH", "/api/admin/site", `{"css":"body{}"}`); w.Code != http.StatusBadRequest {
		t.Errorf("unknown key: %d", w.Code) // no hay CSS personalizado (§15.1.5)
	}

	// Con marca: título escapado, colores en un <style> con el nonce de la CSP, datos en JSON.
	w = f.do("", "GET", "/w/ti/cualquier/ruta", "")
	page = w.Body.String()
	_, after, _ := strings.Cut(page, `name="csp-nonce" content="`)
	nonce, _, _ := strings.Cut(after, `"`)
	for _, want := range []string{"<title>Wiki &lt;ACME&gt;</title>", `<style nonce="` + nonce + `">:root{`, "--kw-accent:#f5c400;",
		`<meta name="theme-color" content="#15171c" />`, `"title":"Wiki \u003cACME\u003e"`, `\u003c/script\u003e\u003cb\u003ehola`} {
		if !strings.Contains(page, want) {
			t.Errorf("branded index lacks %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "</script><b>") || strings.Index(page, "<style") > strings.Index(page, "</head>") {
		t.Errorf("branded index: %s", page)
	}

	if w := f.do("super", "POST", "/api/admin/site/preview", `{"color_primary":"#7cc6fe","color_header":"#ffffff"}`); w.Code != http.StatusOK ||
		!strings.Contains(w.Body.String(), `"--kw-topbar-text":"#1f2328"`) {
		t.Errorf("preview: %d %s", w.Code, w.Body)
	}

	// Logo: se sanitiza, se generan los íconos y se sirve sin sesión, con CSP sandbox y ETag.
	w = f.uploadSite("super", "logo", []byte(testLogoSVG))
	if w.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	var up struct {
		Site struct{ Assets map[string]string } `json:"site"`
	}
	json.Unmarshal(w.Body.Bytes(), &up)
	if up.Site.Assets["logo"] == "" || up.Site.Assets["icons"] == "" {
		t.Fatalf("assets after upload: %s", w.Body)
	}
	logo := f.do("", "GET", "/_site/logo", "")
	if logo.Code != http.StatusOK || logo.Header().Get("Content-Type") != "image/svg+xml" || strings.Contains(logo.Body.String(), "alert") ||
		!strings.Contains(logo.Header().Get("Content-Security-Policy"), "sandbox") || logo.Header().Get("X-Content-Type-Options") != "nosniff" ||
		logo.Header().Get("Cache-Control") != "public, no-cache" {
		t.Errorf("logo: %d %v %s", logo.Code, logo.Header(), logo.Body)
	}
	r := httptest.NewRequest("GET", "/_site/logo", nil)
	r.Header.Set("If-None-Match", logo.Header().Get("ETag"))
	again := httptest.NewRecorder()
	f.h.ServeHTTP(again, r)
	if again.Code != http.StatusNotModified {
		t.Errorf("same logo again: %d, want 304", again.Code)
	}
	if w := f.do("", "GET", "/_site/icon-180.png", ""); w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/png" {
		t.Errorf("icon from logo: %d %v", w.Code, w.Header())
	}
	if w := f.do("", "GET", "/_site/logo-dark", ""); w.Code != http.StatusNotFound {
		t.Errorf("missing logo-dark: %d", w.Code)
	}
	if w := f.do("", "GET", "/_site/../../data/wiki.db", ""); w.Code == http.StatusOK && strings.Contains(w.Body.String(), "SQLite") {
		t.Error("path traversal through /_site")
	}
	page = f.do("", "GET", "/", "").Body.String()
	if !strings.Contains(page, `href="/_site/favicon.ico?v=`+up.Site.Assets["icons"]) || strings.Contains(page, `href="/favicon.svg"`) {
		t.Errorf("index icons: %s", page)
	}
	manifest := f.do("", "GET", "/_site/manifest.webmanifest", "")
	if manifest.Code != http.StatusOK || !strings.Contains(manifest.Body.String(), `"name":"Wiki \u003cACME\u003e"`) ||
		!strings.Contains(manifest.Body.String(), "/_site/icon-512.png") {
		t.Errorf("manifest: %d %s", manifest.Code, manifest.Body)
	}

	if w := f.uploadSite("super", "fondo", []byte(testLogoSVG)); w.Code != http.StatusNotFound {
		t.Errorf("unknown kind: %d", w.Code)
	}
	if w := f.uploadSite("super", "favicon", []byte("<html><script>alert(1)</script></html>")); w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("html as favicon: %d", w.Code)
	}
	if w := f.uploadSite("super", "favicon", bytes.Repeat([]byte("x"), 1<<20+1)); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("too large: %d", w.Code)
	}
	if w := f.do("super", "DELETE", "/api/admin/site/assets/logo", ""); w.Code != http.StatusNoContent {
		t.Errorf("delete: %d", w.Code)
	}
	if w := f.do("", "GET", "/_site/favicon.ico", ""); w.Code != http.StatusNotFound {
		t.Errorf("icons after delete: %d", w.Code)
	}

	// Todo queda en la auditoría del superadmin.
	var events []struct{ Action string }
	json.Unmarshal(f.do("super", "GET", "/api/admin/audit?action=site.asset_upload", "").Body.Bytes(), &struct{ Events *[]struct{ Action string } }{&events})
	if len(events) != 1 {
		t.Errorf("audited uploads = %d, want 1", len(events))
	}
}
