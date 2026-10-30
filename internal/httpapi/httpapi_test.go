package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/auth"
	"github.com/athomo001/kudawiki/internal/branding"
	"github.com/athomo001/kudawiki/internal/database"
	"github.com/athomo001/kudawiki/internal/database/dbtest"
	"github.com/athomo001/kudawiki/internal/media"
	"github.com/athomo001/kudawiki/internal/middleware"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/athomo001/kudawiki/internal/wiki"
	"github.com/athomo001/kudawiki/internal/workspace"
	"github.com/oklog/ulid/v2"
)

const (
	origin   = "http://localhost:8080"
	password = "lluvia-sobre-temuco-42"
)

// fixture: dos áreas (ti, finanzas) y una persona por cada lugar de la matriz de contención.
type fixture struct {
	h       http.Handler
	db      *database.DB
	cookies map[string]*http.Cookie // por nombre: super, adminTI, editorTI, viewerTI, adminFin, outsider
}

// unlimited: los tests hacen miles de peticiones desde la misma IP; el límite tiene su propio test.
var unlimited = middleware.Limit{PerSecond: 1e9, Burst: 1e9}

func newFixture(t *testing.T, limit ...middleware.Limit) fixture {
	t.Helper()
	ctx := context.Background()
	db := dbtest.Open(t)
	store := repo.New(db)
	rec := audit.NewRecorder(store, time.Now, slog.New(slog.DiscardHandler))
	svc, err := auth.NewService(store, rec, time.Now, false)
	if err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse(origin)
	storage := t.TempDir()
	brand, err := branding.NewService(store, rec, branding.Patch{})
	if err != nil {
		t.Fatal(err)
	}
	h := New(Deps{Auth: svc, Workspaces: workspace.NewService(store, rec), Wiki: wiki.NewService(store, rec),
		Media: media.NewService(store, rec, storage, 1<<20), Branding: brand, Read: store, DB: db, StorageDir: storage,
		UI:      fstest.MapFS{"index.html": {Data: []byte("<!doctype html><head><!-- kw:brand --><title>Kudawiki</title><!-- /kw:brand --></head>app")}},
		BaseURL: base, TrashRetention: 30 * 24 * time.Hour, RateLimit: append(limit, unlimited)[0], Log: slog.New(slog.NewTextHandler(testLog{t}, &slog.HandlerOptions{Level: slog.LevelError}))})

	hash, _ := auth.HashPassword(password)
	users := map[string]string{}
	for _, name := range []string{"super", "adminTI", "editorTI", "viewerTI", "adminFin", "outsider"} {
		id := ulid.Make().String()
		users[name] = id
		if err := store.CreateUser(ctx, repo.User{ID: id, Email: name + "@empresa.cl", FullName: name,
			PasswordHash: hash, IsGlobalAdmin: name == "super", IsActive: true, Locale: "es"}); err != nil {
			t.Fatal(err)
		}
	}
	ws := workspace.NewService(store, rec)
	ti, _ := ws.Create(ctx, "ti", "TI")
	fin, _ := ws.Create(ctx, "finanzas", "Finanzas")
	for name, m := range map[string]struct {
		ws   repo.Workspace
		role string
	}{"adminTI": {ti, "admin"}, "editorTI": {ti, "editor"}, "viewerTI": {ti, "viewer"}, "adminFin": {fin, "admin"}} {
		if err := store.SetMember(ctx, repo.Scope{WsID: m.ws.ID}, users[name], m.role); err != nil {
			t.Fatal(err)
		}
	}

	f := fixture{h: h, db: db, cookies: map[string]*http.Cookie{}}
	for name := range users {
		w := f.do("", "POST", "/api/auth/login", `{"email":"`+name+`@empresa.cl","password":"`+password+`"}`)
		if w.Code != http.StatusNoContent {
			t.Fatalf("login %s: %d", name, w.Code)
		}
		f.cookies[name] = w.Result().Cookies()[0]
	}
	return f
}

// testLog lleva al test los errores que registra el servidor: un 500 dice por qué, en vez de
// perderse. Solo errores: el log de cada petición sería ruido.
type testLog struct{ t testing.TB }

func (l testLog) Write(p []byte) (int, error) {
	l.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}

func (f fixture) do(who, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Origin", origin)
	if c := f.cookies[who]; c != nil {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	return w
}

func TestLoginMeLogout(t *testing.T) {
	f := newFixture(t)
	w := f.do("adminTI", "GET", "/api/auth/me", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"slug":"ti"`) || strings.Contains(w.Body.String(), "finanzas") {
		t.Fatalf("me: %d %s", w.Code, w.Body)
	}
	c := f.cookies["adminTI"]
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode {
		t.Errorf("session cookie flags: %+v", c)
	}
	if w := f.do("adminTI", "POST", "/api/auth/logout", ""); w.Code != http.StatusNoContent {
		t.Fatalf("logout: %d", w.Code)
	}
	if w := f.do("adminTI", "GET", "/api/auth/me", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("me after logout: %d, want 401", w.Code)
	}
}

// §15: el idioma y las preferencias de lectura son de cada persona y viajan con /auth/me.
func TestPreferences(t *testing.T) {
	f := newFixture(t)
	if w := f.do("viewerTI", "PATCH", "/api/me", `{"locale":"en","reading_prefs":{"font":"dyslexic","wide":true}}`); w.Code != http.StatusNoContent {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}
	var me struct {
		Locale       string `json:"locale"`
		ReadingPrefs struct {
			Font string `json:"font"`
			Wide bool   `json:"wide"`
		} `json:"reading_prefs"`
	}
	json.Unmarshal(f.do("viewerTI", "GET", "/api/auth/me", "").Body.Bytes(), &me)
	if me.Locale != "en" || me.ReadingPrefs.Font != "dyslexic" || !me.ReadingPrefs.Wide {
		t.Errorf("me after patch: %+v", me)
	}
	// Solo el idioma: las preferencias quedan como estaban.
	f.do("viewerTI", "PATCH", "/api/me", `{"locale":"es"}`)
	json.Unmarshal(f.do("viewerTI", "GET", "/api/auth/me", "").Body.Bytes(), &me)
	if me.Locale != "es" || me.ReadingPrefs.Font != "dyslexic" {
		t.Errorf("partial patch: %+v", me)
	}
	// Otra persona no se entera.
	json.Unmarshal(f.do("editorTI", "GET", "/api/auth/me", "").Body.Bytes(), &me)
	if me.Locale != "es" || me.ReadingPrefs.Font != "default" || me.ReadingPrefs.Wide {
		t.Errorf("other user changed: %+v", me)
	}
	for body, want := range map[string]int{
		`{"locale":"fr"}`:                               http.StatusUnprocessableEntity,
		`{"reading_prefs":{"font":"comic"}}`:            http.StatusUnprocessableEntity,
		`{"reading_prefs":{"font":"default","size":3}}`: http.StatusBadRequest,
		`{"email":"otro@empresa.cl"}`:                   http.StatusBadRequest, // el correo no se cambia desde el perfil
	} {
		if w := f.do("viewerTI", "PATCH", "/api/me", body); w.Code != want {
			t.Errorf("%s: %d, want %d", body, w.Code, want)
		}
	}
	if w := f.do("", "PATCH", "/api/me", `{"locale":"en"}`); w.Code != http.StatusUnauthorized {
		t.Errorf("no session: %d, want 401", w.Code)
	}
}

// §4.1: el 409 trae lo necesario para resolver el conflicto sin perder lo escrito.
func TestSaveConflictCarriesDiff(t *testing.T) {
	f := newFixture(t)
	put := func(who, body string) *httptest.ResponseRecorder {
		return f.do(who, "PUT", "/api/w/ti/pages/conflicto", body)
	}
	if w := put("editorTI", `{"title":"C","markdown":"uno\ndos\n","base_revision":0}`); w.Code != http.StatusNoContent {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if w := put("adminTI", `{"title":"C","markdown":"uno\nDOS\n","base_revision":1}`); w.Code != http.StatusNoContent {
		t.Fatalf("admin save: %d %s", w.Code, w.Body)
	}
	w := put("editorTI", `{"title":"C","markdown":"uno\ntres\n","base_revision":1}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("stale save: %d, want 409", w.Code)
	}
	var res struct {
		Error struct {
			Code    string `json:"code"`
			Current struct {
				Revision  int    `json:"revision"`
				UpdatedBy string `json:"updated_by"`
				Diff      []struct {
					Op   string `json:"op"`
					Text string `json:"text"`
				} `json:"diff"`
			} `json:"current"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	c := res.Error.Current
	if res.Error.Code != "conflict" || c.Revision != 2 || c.UpdatedBy != "adminTI" {
		t.Errorf("conflict = %s", w.Body)
	}
	// De la versión guardada a la tuya: lo que se perdería si guardaras encima.
	var got []string
	for _, d := range c.Diff {
		got = append(got, d.Op+d.Text)
	}
	if strings.Join(got, "|") != "=uno|-DOS|+tres" {
		t.Errorf("diff = %v", got)
	}
}

func TestLoginRejectsBadInput(t *testing.T) {
	f := newFixture(t)
	for body, want := range map[string]int{
		`{"email":"adminTI@empresa.cl","password":"mala-clave-123"}`: http.StatusUnauthorized,
		`{"email":"adminTI@empresa.cl","password":"x","extra":true}`: http.StatusBadRequest,
		`no es json`: http.StatusBadRequest,
	} {
		if w := f.do("", "POST", "/api/auth/login", body); w.Code != want {
			t.Errorf("%s: %d, want %d", body, w.Code, want)
		}
	}
}

// §3.1 y §19.10: el admin de área gestiona lectores y editores, nunca el rol admin ni su propia fila.
func TestAreaAdminLimits(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct {
		body string
		want int
	}{
		{`{"email":"outsider@empresa.cl","role":"editor"}`, http.StatusNoContent},
		{`{"email":"viewerTI@empresa.cl","role":"admin"}`, http.StatusForbidden},
		{`{"email":"adminTI@empresa.cl","role":"viewer"}`, http.StatusForbidden},
		{`{"email":"nadie@empresa.cl","role":"viewer"}`, http.StatusUnprocessableEntity},
		{`{"email":"outsider@empresa.cl","role":"owner"}`, http.StatusUnprocessableEntity},
	} {
		if w := f.do("adminTI", "POST", "/api/w/ti/members", tc.body); w.Code != tc.want {
			t.Errorf("%s: %d, want %d (%s)", tc.body, w.Code, tc.want, w.Body)
		}
	}
	// El superadmin sí nombra admins, desde su panel.
	var areas []map[string]string
	json.Unmarshal(f.do("super", "GET", "/api/admin/workspaces", "").Body.Bytes(), &areas)
	id := areas[1]["id"] // ordenadas por nombre: Finanzas, TI
	if w := f.do("super", "POST", "/api/admin/workspaces/"+id+"/members", `{"email":"viewerTI@empresa.cl","role":"admin"}`); w.Code != http.StatusNoContent {
		t.Errorf("superadmin grants admin: %d %s", w.Code, w.Body)
	}
}

func TestInviteFlow(t *testing.T) {
	f := newFixture(t)
	w := f.do("super", "POST", "/api/admin/users", `{"email":"nueva@empresa.cl","full_name":"Nueva"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("invite: %d %s", w.Code, w.Body)
	}
	var out struct {
		InviteURL string `json:"invite_url"`
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	token := out.InviteURL[strings.Index(out.InviteURL, "#")+1:]
	if !strings.HasPrefix(out.InviteURL, origin+"/invite#") || token == "" {
		t.Fatalf("invite_url: %q", out.InviteURL)
	}

	accept := `{"token":"` + token + `","new_password":"` + password + `"}`
	if w := f.do("", "POST", "/api/auth/invite/accept", accept); w.Code != http.StatusNoContent {
		t.Fatalf("accept: %d %s", w.Code, w.Body)
	}
	if w := f.do("", "POST", "/api/auth/invite/accept", accept); w.Code != http.StatusGone {
		t.Errorf("second use: %d, want 410", w.Code)
	}
	if w := f.do("", "POST", "/api/auth/login", `{"email":"nueva@empresa.cl","password":"`+password+`"}`); w.Code != http.StatusNoContent {
		t.Errorf("login after accept: %d", w.Code)
	}
	if w := f.do("super", "POST", "/api/admin/users", `{"email":"nueva@empresa.cl","full_name":"Otra"}`); w.Code != http.StatusConflict {
		t.Errorf("duplicate email: %d", w.Code)
	}
}

// §8.5.3: sin correo, el enlace se entrega a mano y así queda en la auditoría; una invitación
// pendiente se puede volver a generar, y el enlace anterior deja de servir.
func TestInviteResend(t *testing.T) {
	f := newFixture(t)
	invite := func(path, body string) (int, string) {
		w := f.do("super", "POST", path, body)
		var out struct {
			InviteURL string `json:"invite_url"`
		}
		json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out.InviteURL[strings.Index(out.InviteURL, "#")+1:]
	}
	if code, _ := invite("/api/admin/users", `{"email":"nueva@empresa.cl","full_name":"Nueva"}`); code != http.StatusCreated {
		t.Fatalf("invite: %d", code)
	}
	var users []struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}
	json.Unmarshal(f.do("super", "GET", "/api/admin/users", "").Body.Bytes(), &users)
	ids := map[string]string{}
	for _, u := range users {
		ids[u.Email] = u.ID
	}

	_, oldToken := invite("/api/admin/users/"+ids["nueva@empresa.cl"]+"/invite", "")
	code, newToken := invite("/api/admin/users/"+ids["nueva@empresa.cl"]+"/invite", "")
	if code != http.StatusCreated || newToken == "" || newToken == oldToken {
		t.Fatalf("resend: %d %q", code, newToken)
	}
	accept := func(token string) int {
		return f.do("", "POST", "/api/auth/invite/accept", `{"token":"`+token+`","new_password":"`+password+`"}`).Code
	}
	if c := accept(oldToken); c != http.StatusGone {
		t.Errorf("replaced link still works: %d, want 410", c)
	}
	if c := accept(newToken); c != http.StatusNoContent {
		t.Errorf("new link: %d", c)
	}
	// Ya tiene contraseña: no hay invitación que reenviar.
	if w := f.do("super", "POST", "/api/admin/users/"+ids["nueva@empresa.cl"]+"/invite", ""); w.Code != http.StatusConflict {
		t.Errorf("resend to active user: %d, want 409", w.Code)
	}
	if w := f.do("super", "POST", "/api/admin/users/no-existe/invite", ""); w.Code != http.StatusNotFound {
		t.Errorf("resend to unknown user: %d, want 404", w.Code)
	}
	// Solo el superadmin; a los demás, el 404 uniforme.
	if w := f.do("adminTI", "POST", "/api/admin/users/"+ids["outsider@empresa.cl"]+"/invite", ""); w.Code != http.StatusNotFound {
		t.Errorf("area admin resends: %d, want 404", w.Code)
	}

	var audit struct {
		Events []struct {
			Meta map[string]any `json:"meta"`
		} `json:"events"`
	}
	json.Unmarshal(f.do("super", "GET", "/api/admin/audit?action=user.invite", "").Body.Bytes(), &audit)
	if len(audit.Events) != 3 {
		t.Fatalf("user.invite events = %d, want 3", len(audit.Events))
	}
	for i, e := range audit.Events {
		if e.Meta["delivery"] != "manual" {
			t.Errorf("event %d meta = %v, want delivery manual", i, e.Meta)
		}
	}
	if audit.Events[0].Meta["resend"] != true {
		t.Errorf("latest event is not marked as resend: %v", audit.Events[0].Meta)
	}
}

// §9.4: la sección donde está la coincidencia, y los filtros con conteos que solo cuentan lo
// que quien busca puede ver (§17: los conteos también filtran).
func TestSearchSectionsAndFacets(t *testing.T) {
	f := newFixture(t)
	page := func(who, slug, md, meta string) {
		t.Helper()
		if w := f.do(who, "PUT", "/api/w/ti/pages/"+slug, `{"title":"`+slug+`","markdown":"`+md+`","base_revision":0}`); w.Code != http.StatusNoContent {
			t.Fatalf("create %s: %d %s", slug, w.Code, w.Body)
		}
		if meta != "" {
			if w := f.do(who, "PATCH", "/api/w/ti/meta/"+slug, meta); w.Code != http.StatusNoContent {
				t.Fatalf("meta %s: %d %s", slug, w.Code, w.Body)
			}
		}
	}
	page("editorTI", "redes/vpn/conexion", `Intro.\n\n## Instalacion\n\nDescarga el cliente kwfacet.\n`, `{"tags":["red","mesa de ayuda"]}`)
	page("editorTI", "redes/firewall/reglas", `Reglas kwfacet.`, `{"tags":["red"]}`)
	page("editorTI", "manuales/correo", `El kwfacet del correo.`, "")
	page("adminTI", "secreto/plan", `Plan kwfacet.`, `{"tags":["kwsecretag"],"level":3}`)
	// Se crean en el mismo segundo: se separan las fechas para que el orden por fecha no empate.
	if _, err := f.db.Write.Exec(`UPDATE pages SET updated_at = datetime('now', '-1 day') WHERE slug <> 'manuales/correo'`); err != nil {
		t.Fatal(err)
	}

	type facet struct {
		Value string `json:"value"`
		Count int    `json:"count"`
	}
	var res struct {
		Results []struct {
			Slug    string `json:"slug"`
			Section *struct {
				ID   string `json:"id"`
				Text string `json:"text"`
			} `json:"section"`
		} `json:"results"`
		Facets struct {
			Tags, Folders, Authors []facet
			Dates                  []struct {
				Days  int `json:"days"`
				Count int `json:"count"`
			}
		} `json:"facets"`
	}
	get := func(who, query string) string {
		t.Helper()
		res.Results, res.Facets.Tags, res.Facets.Folders, res.Facets.Authors = nil, nil, nil, nil
		w := f.do(who, "GET", "/api/w/ti/search?"+query, "")
		if w.Code != http.StatusOK {
			t.Fatalf("search %s: %d %s", query, w.Code, w.Body)
		}
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		return w.Body.String()
	}
	count := func(fs []facet, v string) int {
		for _, x := range fs {
			if x.Value == v {
				return x.Count
			}
		}
		return 0
	}

	body := get("viewerTI", "q=kwfacet")
	if len(res.Results) != 3 || strings.Contains(body, "secret") || strings.Contains(body, "kwsecretag") {
		t.Fatalf("viewer sees the N3 page or its tag: %s", body)
	}
	for _, r := range res.Results {
		if r.Slug == "redes/vpn/conexion" && (r.Section == nil || r.Section.Text != "Instalacion" || r.Section.ID == "") {
			t.Errorf("section of %s = %+v", r.Slug, r.Section)
		}
		if r.Slug == "manuales/correo" && r.Section != nil {
			t.Errorf("page without headings has a section: %+v", r.Section)
		}
	}
	if count(res.Facets.Tags, "red") != 2 || count(res.Facets.Tags, "mesa de ayuda") != 1 {
		t.Errorf("tags = %+v", res.Facets.Tags)
	}
	if count(res.Facets.Folders, "redes") != 2 || count(res.Facets.Folders, "manuales") != 1 || count(res.Facets.Folders, "secreto") != 0 {
		t.Errorf("folders = %+v", res.Facets.Folders)
	}
	if count(res.Facets.Authors, "editorTI") != 3 || count(res.Facets.Authors, "adminTI") != 0 {
		t.Errorf("authors = %+v", res.Facets.Authors)
	}
	if len(res.Facets.Dates) == 0 || res.Facets.Dates[0].Count != 3 {
		t.Errorf("dates = %+v", res.Facets.Dates)
	}

	// El admin sí ve la página N3, y su etiqueta cuenta.
	get("adminTI", "q=kwfacet")
	if len(res.Results) != 4 || count(res.Facets.Tags, "kwsecretag") != 1 || count(res.Facets.Folders, "secreto") != 1 {
		t.Errorf("admin: %d results, facets %+v", len(res.Results), res.Facets)
	}

	// Dentro de una carpeta, las carpetas son las del nivel siguiente.
	get("viewerTI", "q="+url.QueryEscape("kwfacet path:redes"))
	if len(res.Results) != 2 || count(res.Facets.Folders, "redes/vpn") != 1 || count(res.Facets.Folders, "redes/firewall") != 1 {
		t.Errorf("path:redes: %d results, folders %+v", len(res.Results), res.Facets.Folders)
	}
	// Una etiqueta con espacios va entre comillas.
	get("viewerTI", "q="+url.QueryEscape(`kwfacet tag:"mesa de ayuda"`))
	if len(res.Results) != 1 || res.Results[0].Slug != "redes/vpn/conexion" {
		t.Errorf(`tag:"mesa de ayuda": %+v`, res.Results)
	}
	// Por fecha: la última modificada primero.
	get("viewerTI", "q=kwfacet&sort=date")
	if len(res.Results) != 3 || res.Results[0].Slug != "manuales/correo" {
		t.Errorf("sort=date: %+v", res.Results)
	}
}

// §3.1 (pedido original, pre-spec §2.1): el admin del área edita su nombre y su descripción;
// todos los miembros los ven, y el cambio queda en la auditoría sin el texto.
func TestAreaSettings(t *testing.T) {
	f := newFixture(t)
	if w := f.do("adminTI", "GET", "/api/w/ti/settings", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"name":"TI"`) {
		t.Fatalf("get: %d %s", w.Code, w.Body)
	}
	if w := f.do("adminTI", "PATCH", "/api/w/ti/settings", `{"name":"Tecnología","description":"Soporte, redes y servidores"}`); w.Code != http.StatusNoContent {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}
	// Lo ve cualquier miembro, en sus áreas.
	if body := f.do("viewerTI", "GET", "/api/auth/me", "").Body.String(); !strings.Contains(body, `"name":"Tecnología"`) || !strings.Contains(body, "Soporte, redes y servidores") {
		t.Errorf("me after patch: %s", body)
	}
	// Solo un campo: el otro queda como estaba.
	f.do("adminTI", "PATCH", "/api/w/ti/settings", `{"description":""}`)
	if body := f.do("adminTI", "GET", "/api/w/ti/settings", "").Body.String(); !strings.Contains(body, `"name":"Tecnología"`) || !strings.Contains(body, `"description":""`) {
		t.Errorf("partial patch: %s", body)
	}
	for body, want := range map[string]int{
		`{"name":"  "}`: http.StatusUnprocessableEntity,
		`{"description":"` + strings.Repeat("x", 501) + `"}`: http.StatusUnprocessableEntity,
		`{"slug":"otra"}`: http.StatusBadRequest,
	} {
		if w := f.do("adminTI", "PATCH", "/api/w/ti/settings", body); w.Code != want {
			t.Errorf("%.40s: %d, want %d", body, w.Code, want)
		}
	}
	// Otra área, otro rol: el mismo 404.
	if w := f.do("editorTI", "PATCH", "/api/w/ti/settings", `{"name":"x"}`); w.Code != http.StatusNotFound {
		t.Errorf("editor patches: %d, want 404", w.Code)
	}
	if w := f.do("adminFin", "GET", "/api/w/ti/settings", ""); w.Code != http.StatusNotFound {
		t.Errorf("other area admin: %d, want 404", w.Code)
	}
	var audit struct {
		Events []struct {
			Action string         `json:"action"`
			Meta   map[string]any `json:"meta"`
		} `json:"events"`
	}
	raw := f.do("adminTI", "GET", "/api/w/ti/audit?action=ws.update", "").Body
	json.Unmarshal(raw.Bytes(), &audit)
	if len(audit.Events) != 2 || strings.Contains(raw.String(), "Soporte") {
		t.Errorf("audit (2 events, no text): %s", raw)
	}
}

// §4.1: descripción corta e ícono por página. Se ven en la página, en el índice de hijas y en
// los listados, se encuentran al buscar, y no se pierden al volver a guardar ni al mover.
// La de una página que el lector no ve no aparece en ninguna parte (§17).
func TestPageInfo(t *testing.T) {
	f := newFixture(t)
	save := func(who, slug, md string, base int) {
		t.Helper()
		if w := f.do(who, "PUT", "/api/w/ti/pages/"+slug, fmt.Sprintf(`{"title":"%s","markdown":"%s","base_revision":%d}`, slug, md, base)); w.Code != http.StatusNoContent {
			t.Fatalf("save %s: %d %s", slug, w.Code, w.Body)
		}
	}
	meta := func(who, slug, body string) int { return f.do(who, "PATCH", "/api/w/ti/meta/"+slug, body).Code }
	save("editorTI", "manual", `Índice:\n\n[[children]]\n`, 0)
	save("editorTI", "manual/vpn", "Pasos.", 0)
	save("adminTI", "manual/secreto", "Plan.", 0)
	if c := meta("editorTI", "manual/vpn", `{"description":"Cómo conectarse desde la casa","icon":"🔐"}`); c != http.StatusNoContent {
		t.Fatalf("meta: %d", c)
	}
	if c := meta("adminTI", "manual/secreto", `{"description":"kwdescsecreta","level":3}`); c != http.StatusNoContent {
		t.Fatalf("meta N3: %d", c)
	}

	var page struct {
		Description string `json:"description"`
		Icon        string `json:"icon"`
		HTML        string `json:"html"`
	}
	json.Unmarshal(f.do("viewerTI", "GET", "/api/w/ti/pages/manual/vpn", "").Body.Bytes(), &page)
	if page.Description != "Cómo conectarse desde la casa" || page.Icon != "🔐" {
		t.Errorf("page = %+v", page)
	}
	// El índice de hijas lleva la descripción; la de la hija N3 no llega al lector.
	json.Unmarshal(f.do("viewerTI", "GET", "/api/w/ti/pages/manual", "").Body.Bytes(), &page)
	if !strings.Contains(page.HTML, "Cómo conectarse desde la casa") || strings.Contains(page.HTML, "kwdescsecreta") {
		t.Errorf("children index: %s", page.HTML)
	}
	if body := f.do("viewerTI", "GET", "/api/w/ti/tree", "").Body.String(); !strings.Contains(body, "🔐") || strings.Contains(body, "kwdescsecreta") {
		t.Errorf("tree: %s", body)
	}

	// Se busca por la descripción, también por la raíz (conectar → conectarse), y sigue
	// encontrándose después de volver a guardar el contenido y de mover la página.
	found := func(q string) bool {
		return strings.Contains(f.do("viewerTI", "GET", "/api/w/ti/search?q="+url.QueryEscape(q), "").Body.String(), `"slug":"`)
	}
	if !found("conectar") {
		t.Error("description is not searchable")
	}
	if found("kwdescsecreta") {
		t.Error("N3 description found by a viewer")
	}
	save("editorTI", "manual/vpn", "Pasos nuevos.", 1)
	if w := f.do("editorTI", "POST", "/api/w/ti/move/manual/vpn", `{"to":"guias/vpn"}`); w.Code != http.StatusNoContent {
		t.Fatalf("move: %d %s", w.Code, w.Body)
	}
	if !found("conectar") {
		t.Error("description lost from the index after saving and moving")
	}
	json.Unmarshal(f.do("viewerTI", "GET", "/api/w/ti/pages/guias/vpn", "").Body.Bytes(), &page)
	if page.Description != "Cómo conectarse desde la casa" || page.Icon != "🔐" {
		t.Errorf("after save and move: %+v", page)
	}

	for body, want := range map[string]int{
		`{"description":"` + strings.Repeat("x", 201) + `"}`: http.StatusUnprocessableEntity,
		`{"icon":"abc"}`: http.StatusUnprocessableEntity,
		`{"icon":"🔐 "}`:  http.StatusUnprocessableEntity,
		`{"icon":"🔐🔐🔐🔐🔐🔐🔐🔐🔐"}`: http.StatusUnprocessableEntity,
		`{"icon":""}`:    http.StatusNoContent, // quitarlo
		`{"icon":"👩‍💻"}`: http.StatusNoContent, // emoji compuesto
	} {
		if c := meta("editorTI", "guias/vpn", body); c != want {
			t.Errorf("%.40s: %d, want %d", body, c, want)
		}
	}
}

// §14.5: la API entera tiene límite, no solo el login; pasado el límite, 429 con Retry-After.
func TestAPIRateLimit(t *testing.T) {
	// Seis fichas, que el fixture gasta en iniciar sesión con sus seis personas: la próxima
	// petición, de cualquiera y a cualquier ruta de la API, ya no pasa.
	f := newFixture(t, middleware.Limit{PerSecond: 0.001, Burst: 6})
	w := f.do("adminTI", "GET", "/api/w/ti/tree", "")
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Errorf("over the limit: %d %q", w.Code, w.Header().Get("Retry-After"))
	}
}

// §13.7: la salud del contenido la ve el admin del área (la suya) y el superadmin (todas, y lo
// que no es de ningún área). A los demás, el 404 uniforme.
func TestHealthViews(t *testing.T) {
	f := newFixture(t)
	f.do("editorTI", "PUT", "/api/w/ti/pages/red", `{"title":"Red","markdown":"Ver [[no/existe]].","base_revision":0}`)
	f.do("adminFin", "PUT", "/api/w/finanzas/pages/plan", `{"title":"Plan","markdown":"Ver [[otra/rota]].","base_revision":0}`)

	area := f.do("adminTI", "GET", "/api/w/ti/health", "")
	if area.Code != http.StatusOK || !strings.Contains(area.Body.String(), `"links.broken"`) || !strings.Contains(area.Body.String(), "no/existe") {
		t.Fatalf("area health: %d %s", area.Code, area.Body)
	}
	if strings.Contains(area.Body.String(), "otra/rota") {
		t.Errorf("another area's problem leaked: %s", area.Body)
	}
	for _, who := range []string{"editorTI", "viewerTI", "adminFin", "outsider"} {
		if w := f.do(who, "GET", "/api/w/ti/health", ""); w.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", who, w.Code)
		}
	}
	global := f.do("super", "GET", "/api/admin/health", "")
	if global.Code != http.StatusOK || !strings.Contains(global.Body.String(), "no/existe") || !strings.Contains(global.Body.String(), "otra/rota") {
		t.Errorf("global health: %d %s", global.Code, global.Body)
	}
	if w := f.do("adminTI", "GET", "/api/admin/health", ""); w.Code != http.StatusNotFound {
		t.Errorf("area admin on global health: %d", w.Code)
	}
}

func TestCSRFRejectsCrossSite(t *testing.T) {
	f := newFixture(t)
	r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{}`))
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("cross-site POST: %d, want 403", w.Code)
	}
}

func TestRoutingFallbacks(t *testing.T) {
	f := newFixture(t)
	if w := f.do("", "GET", "/api/no-existe", ""); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), `"code":"not_found"`) {
		t.Errorf("unknown API route: %d %s", w.Code, w.Body)
	}
	w := f.do("", "GET", "/w/ti/procedimientos", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "app") {
		t.Errorf("SPA fallback: %d %s", w.Code, w.Body)
	}
	// Cada index.html trae su nonce, y la CSP lo autoriza solo para estilos (el editor lo necesita).
	csp := w.Header().Get("Content-Security-Policy")
	_, after, _ := strings.Cut(w.Body.String(), `name="csp-nonce" content="`)
	nonce, _, _ := strings.Cut(after, `"`)
	if !strings.Contains(csp, "style-src 'self' 'nonce-"+nonce+"'") || strings.Contains(csp, "unsafe-inline") ||
		!strings.Contains(csp, "frame-ancestors 'none'") || len(nonce) < 20 {
		t.Errorf("CSP %q with nonce %q", csp, nonce)
	}
	if again := f.do("", "GET", "/", ""); strings.Contains(again.Body.String(), nonce) {
		t.Error("nonce reused across responses")
	}
	if w := f.do("", "GET", "/readyz", ""); w.Code != http.StatusOK {
		t.Errorf("readyz: %d", w.Code)
	}
}

func TestPageEditing(t *testing.T) {
	f := newFixture(t)
	create := `{"title":"VPN","markdown":"<script>x</script>\n## Conectar","base_revision":0}`
	if w := f.do("viewerTI", "PUT", "/api/w/ti/pages/vpn", create); w.Code != http.StatusNotFound {
		t.Errorf("viewer writes: %d, want 404", w.Code)
	}
	if w := f.do("editorTI", "PUT", "/api/w/ti/pages/vpn", create); w.Code != http.StatusNoContent {
		t.Fatalf("editor creates: %d %s", w.Code, w.Body)
	}
	page := f.do("viewerTI", "GET", "/api/w/ti/pages/vpn", "").Body.String()
	if strings.Contains(page, "<script>") || !strings.Contains(page, `"revision":1`) {
		t.Errorf("page view: %s", page)
	}
	update := `{"title":"VPN","markdown":"v2","base_revision":1}`
	if w := f.do("editorTI", "PUT", "/api/w/ti/pages/vpn", update); w.Code != http.StatusNoContent {
		t.Fatalf("update: %d %s", w.Code, w.Body)
	}
	if w := f.do("adminTI", "PUT", "/api/w/ti/pages/vpn", update); w.Code != http.StatusConflict {
		t.Errorf("stale base_revision: %d, want 409", w.Code)
	}
	if w := f.do("editorTI", "PUT", "/api/w/ti/pages/Mal/Slug", create); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad slug: %d, want 422", w.Code)
	}
	diff := f.do("viewerTI", "GET", "/api/w/ti/diff/vpn?from=1&to=2", "").Body.String()
	if !strings.Contains(diff, `{"op":"+","text":"v2"}`) {
		t.Errorf("diff: %s", diff)
	}
	if w := f.do("editorTI", "POST", "/api/w/ti/revert/vpn", `{"revision":1,"base_revision":2}`); w.Code != http.StatusNoContent {
		t.Errorf("revert: %d %s", w.Code, w.Body)
	}
}

// upload sube un archivo como lo haría el editor al pegar una imagen.
func (f fixture) upload(who, area, name string, data []byte) *httptest.ResponseRecorder {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("file", name)
	part.Write(data)
	mw.Close()
	r := httptest.NewRequest("POST", "/api/w/"+area+"/media", &body)
	r.Header.Set("Origin", origin)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.AddCookie(f.cookies[who])
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	return w
}

func pngBytes(t *testing.T) []byte {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 800, 600))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// Matriz de contención de media (SPEC §3.2, §4.4): hereda el nivel de su página.
func TestMediaContainment(t *testing.T) {
	f := newFixture(t)
	w := f.upload("editorTI", "ti", "diagrama.png", pngBytes(t))
	if w.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	var up struct{ ID, URL string }
	json.Unmarshal(w.Body.Bytes(), &up)

	// Sin página todavía: solo los editores la ven.
	if got := f.do("viewerTI", "GET", up.URL, ""); got.Code != http.StatusNotFound {
		t.Errorf("viewer, unattached: %d", got.Code)
	}
	got := f.do("editorTI", "GET", up.URL, "")
	if got.Code != http.StatusOK || got.Header().Get("Content-Type") != "image/png" ||
		got.Header().Get("Content-Security-Policy") != "default-src 'none'; sandbox" ||
		!strings.HasPrefix(got.Header().Get("Content-Disposition"), "inline") {
		t.Errorf("editor GET: %d %v", got.Code, got.Header())
	}
	if thumb := f.do("editorTI", "GET", up.URL+"?thumb=1", ""); thumb.Code != http.StatusOK || thumb.Body.Len() >= got.Body.Len() {
		t.Errorf("thumbnail: %d, %d bytes vs %d", thumb.Code, thumb.Body.Len(), got.Body.Len())
	}

	// Al guardarla en una página toma su nivel: ahora la ve el lector.
	body := `{"title":"Red","markdown":"![d](` + up.URL + `)","base_revision":0}`
	if w := f.do("editorTI", "PUT", "/api/w/ti/pages/red", body); w.Code != http.StatusNoContent {
		t.Fatalf("save page: %d %s", w.Code, w.Body)
	}
	if got := f.do("viewerTI", "GET", up.URL, ""); got.Code != http.StatusOK {
		t.Errorf("viewer, attached to N1 page: %d", got.Code)
	}
	if _, err := f.db.Write.Exec(`UPDATE pages SET min_rank = 3 WHERE slug = 'red'`); err != nil {
		t.Fatal(err)
	}
	want := f.do("viewerTI", "GET", "/api/w/ti/media/01ARZ3NDEKTSV4RRFFQ69G5FAV", "")
	for _, who := range []string{"viewerTI", "editorTI", "adminFin", "outsider"} {
		if got := f.do(who, "GET", up.URL, ""); got.Code != want.Code || got.Body.String() != want.Body.String() {
			t.Errorf("%s GET media of an N3 page: %d, want the uniform 404", who, got.Code)
		}
	}
	// El mismo ID pedido desde otra área tampoco existe.
	if got := f.do("adminFin", "GET", strings.Replace(up.URL, "/w/ti/", "/w/finanzas/", 1), ""); got.Code != http.StatusNotFound {
		t.Errorf("other area: %d", got.Code)
	}
}

func TestMediaRejectsTypes(t *testing.T) {
	f := newFixture(t)
	for name, data := range map[string]string{
		"x.html": "<!doctype html><script>alert(1)</script>",
		"x.svg":  `<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"></svg>`,
	} {
		if w := f.upload("editorTI", "ti", name, []byte(data)); w.Code != http.StatusUnsupportedMediaType {
			t.Errorf("%s: %d, want 415", name, w.Code)
		}
	}
	if w := f.upload("viewerTI", "ti", "a.png", pngBytes(t)); w.Code != http.StatusNotFound {
		t.Errorf("viewer uploads: %d, want 404", w.Code)
	}
	if w := f.upload("editorTI", "ti", "grande.png", append(pngBytes(t), make([]byte, 1<<20)...)); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("too large: %d, want 413", w.Code)
	}
}

// Metadatos que delatan contenido (SPEC §4.6, §4.7, §17): etiquetas, conteos, backlinks y
// enlaces rotos solo cuentan lo que el lector puede ver.
func TestLinkAndTagContainment(t *testing.T) {
	f := newFixture(t)
	for slug, md := range map[string]string{
		"vpn":     "Cliente oficial.",
		"publica": "Ver [[vpn]] y [[secreta]].",
		"secreta": "Enlaza a [[vpn]].",
	} {
		if w := f.do("editorTI", "PUT", "/api/w/ti/pages/"+slug, `{"title":"`+slug+`","markdown":"`+md+`","base_revision":0}`); w.Code != http.StatusNoContent {
			t.Fatalf("create %s: %d %s", slug, w.Code, w.Body)
		}
	}
	for slug, tags := range map[string]string{"vpn": `["red","acceso"]`, "secreta": `["sueldos","red"]`} {
		if w := f.do("editorTI", "PATCH", "/api/w/ti/meta/"+slug, `{"tags":`+tags+`}`); w.Code != http.StatusNoContent {
			t.Fatalf("tags %s: %d %s", slug, w.Code, w.Body)
		}
	}
	f.db.Write.Exec(`UPDATE pages SET min_rank = 3 WHERE slug = 'secreta'`)

	tags := f.do("viewerTI", "GET", "/api/w/ti/tags", "").Body.String()
	if strings.Contains(tags, "sueldos") || !strings.Contains(tags, `{"count":1,"tag":"red"}`) {
		t.Errorf("viewer tag counts leak the N3 page: %s", tags)
	}
	if admin := f.do("adminTI", "GET", "/api/w/ti/tags", "").Body.String(); !strings.Contains(admin, `{"count":2,"tag":"red"}`) {
		t.Errorf("admin tag counts: %s", admin)
	}
	if got := f.do("viewerTI", "GET", "/api/w/ti/tags/sueldos", "").Body.String(); got != "[]\n" {
		t.Errorf("viewer pages by hidden tag: %s", got)
	}
	back := f.do("viewerTI", "GET", "/api/w/ti/backlinks/vpn", "").Body.String()
	if !strings.Contains(back, `"publica"`) || strings.Contains(back, "secreta") {
		t.Errorf("viewer backlinks: %s", back)
	}
	// Para el lector, el enlace a la página N3 sale roto, igual que uno a una inexistente.
	page := f.do("viewerTI", "GET", "/api/w/ti/pages/publica", "").Body.String()
	if !strings.Contains(page, `wikilink broken\" href=\"/w/ti/p/secreta`) || strings.Contains(page, `wikilink broken\" href=\"/w/ti/p/vpn`) {
		t.Errorf("broken link marks: %s", page)
	}
	if w := f.do("editorTI", "PATCH", "/api/w/ti/meta/vpn", `{"tags":["<script>"]}`); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad tag: %d", w.Code)
	}
}

func TestMovePages(t *testing.T) {
	f := newFixture(t)
	for slug, md := range map[string]string{
		"redes/vpn":         "Raíz.",
		"redes/vpn/cliente": "Hija.",
		"indice":            "Ver [[redes/vpn|la VPN]] y [[redes/vpn/cliente]].",
	} {
		if w := f.do("editorTI", "PUT", "/api/w/ti/pages/"+slug, `{"title":"x","markdown":"`+md+`","base_revision":0}`); w.Code != http.StatusNoContent {
			t.Fatalf("create %s: %d %s", slug, w.Code, w.Body)
		}
	}
	if w := f.do("editorTI", "POST", "/api/w/ti/move/redes/vpn", `{"to":"infra/vpn"}`); w.Code != http.StatusNoContent {
		t.Fatalf("move: %d %s", w.Code, w.Body)
	}
	tree := f.do("viewerTI", "GET", "/api/w/ti/tree", "").Body.String()
	if !strings.Contains(tree, "infra/vpn/cliente") || strings.Contains(tree, "redes/") {
		t.Errorf("tree after move: %s", tree)
	}
	// La ruta vieja redirige a la nueva.
	if w := f.do("viewerTI", "GET", "/api/w/ti/pages/redes/vpn/cliente", ""); w.Code != http.StatusPermanentRedirect || w.Header().Get("Location") != "/api/w/ti/pages/infra/vpn/cliente" {
		t.Errorf("alias: %d %q", w.Code, w.Header().Get("Location"))
	}
	// Los enlaces de otras páginas se reescribieron, con su revisión.
	idx := f.do("viewerTI", "GET", "/api/w/ti/pages/indice", "").Body.String()
	if !strings.Contains(idx, `[[infra/vpn|la VPN]] y [[infra/vpn/cliente]]`) || !strings.Contains(idx, `"revision":2`) {
		t.Errorf("rewritten links: %s", idx)
	}
	if back := f.do("viewerTI", "GET", "/api/w/ti/backlinks/infra/vpn", "").Body.String(); !strings.Contains(back, "indice") {
		t.Errorf("backlinks after move: %s", back)
	}
	for body, want := range map[string]int{
		`{"to":"indice"}`:              http.StatusConflict,            // ya existe
		`{"to":"infra/vpn/cliente/x"}`: http.StatusUnprocessableEntity, // dentro de sí misma
		`{"to":"Mal Slug"}`:            http.StatusUnprocessableEntity,
	} {
		if w := f.do("editorTI", "POST", "/api/w/ti/move/infra/vpn", body); w.Code != want {
			t.Errorf("move %s: %d, want %d", body, w.Code, want)
		}
	}
	if w := f.do("viewerTI", "POST", "/api/w/ti/move/indice", `{"to":"otro"}`); w.Code != http.StatusNotFound {
		t.Errorf("viewer moves: %d", w.Code)
	}
}

func searchQ(f fixture, who, q string) string {
	return f.do(who, "GET", "/api/w/ti/search?q="+url.QueryEscape(q), "").Body.String()
}

// Búsqueda (SPEC §9): pesos, raíces, tildes, prefijos y filtros, y contención por nivel: un
// término que solo está en una página N3 no aparece para un lector, ni en sugerencias.
func TestSearch(t *testing.T) {
	f := newFixture(t)
	for slug, body := range map[string]string{
		"redes/vpn":   `{"title":"Procedimiento de conexión VPN","markdown":"## Instalación\nUsa el cliente oficial. Texto <script>x</script>.\n\n` + "```\\nvpnclient --up\\n```" + `","base_revision":0}`,
		"redes/wifi":  `{"title":"Wifi de invitados","markdown":"Conectarse a la red de invitados.","base_revision":0}`,
		"rrhh/sueldo": `{"title":"Sueldos confidenciales","markdown":"La palabra zanahoria solo vive aquí.","base_revision":0}`,
	} {
		if w := f.do("editorTI", "PUT", "/api/w/ti/pages/"+slug, body); w.Code != http.StatusNoContent {
			t.Fatalf("create %s: %d %s", slug, w.Code, w.Body)
		}
	}
	f.do("editorTI", "PATCH", "/api/w/ti/meta/redes/wifi", `{"tags":["red"]}`)
	f.db.Write.Exec(`UPDATE pages SET min_rank = 3 WHERE slug = 'rrhh/sueldo'`)

	for q, want := range map[string]string{
		"procedimientos":     "redes/vpn",  // raíz: plural
		"conexion":           "redes/vpn",  // sin tilde
		"proced":             "redes/vpn",  // prefijo
		"instalación":        "redes/vpn",  // encabezado
		"vpnclient":          "redes/vpn",  // código
		"conectar":           "redes/wifi", // raíz: conectarse
		"red tag:red":        "redes/wifi",
		`"red de invitados"`: "redes/wifi",
		"path:redes":         "redes/",
		"cliente oficial":    "redes/vpn", // varias palabras: todas deben estar
		"cliente OR wifi":    "redes/wifi",
		"conexion instalar":  "redes/vpn", // una sin tilde y otra por su raíz
	} {
		if got := searchQ(f, "viewerTI", q); !strings.Contains(got, `"slug":"`+want) {
			t.Errorf("search %q: %s", q, got)
		}
	}
	if got := searchQ(f, "viewerTI", "invitados -wifi"); strings.Contains(got, "redes/wifi") {
		t.Errorf("exclusion: %s", got)
	}
	// El fragmento llega como texto con marcas, nunca como HTML.
	if got := searchQ(f, "viewerTI", "oficial"); !strings.Contains(got, `{"text":"oficial","hit":true}`) {
		t.Errorf("snippet: %s", got)
	}

	// Contención: la página N3 no existe para el lector, ni como sugerencia.
	for _, q := range []string{"zanahoria", "sueldos", "sueldo confidencial", "suledos"} {
		if got := searchQ(f, "viewerTI", q); strings.Contains(got, "rrhh") || strings.Contains(got, "Sueldos") {
			t.Errorf("viewer search %q leaks the N3 page: %s", q, got)
		}
	}
	if got := searchQ(f, "adminTI", "zanahoria"); !strings.Contains(got, "rrhh/sueldo") {
		t.Errorf("admin search: %s", got)
	}
	if got := searchQ(f, "viewerTI", "procedimeinto"); !strings.Contains(got, `"suggestion":"Procedimiento de conexión VPN"`) {
		t.Errorf("typo suggestion: %s", got)
	}
	if got := searchQ(f, "adminFin", "vpn"); !strings.Contains(got, `"code":"not_found"`) {
		t.Errorf("other area: %s", got)
	}
	for _, q := range []string{`vpn"`, `NEAR(a b)`, `*`, `"`, `{title}:x`} {
		if w := f.do("viewerTI", "GET", "/api/w/ti/search?q="+url.QueryEscape(q), ""); w.Code != http.StatusOK {
			t.Errorf("search %q: %d %s", q, w.Code, w.Body)
		}
	}

	// El índice es derivado: borrado y reconstruido, la búsqueda vuelve a funcionar (§9.7).
	store := repo.New(f.db)
	f.db.Write.Exec(`DELETE FROM pages_fts`)
	if stale, _ := store.IndexStale(context.Background()); !stale {
		t.Error("IndexStale = false after deleting the index")
	}
	if err := store.RebuildIndex(context.Background(), wiki.IndexDoc); err != nil {
		t.Fatal(err)
	}
	if got := searchQ(f, "viewerTI", "procedimientos"); !strings.Contains(got, "redes/vpn") {
		t.Errorf("after rebuild: %s", got)
	}

	// Las búsquedas sin resultados las ve solo el admin del área, sin quién las hizo (§9.6).
	searchQ(f, "viewerTI", "impresora")
	if got := f.do("adminTI", "GET", "/api/w/ti/search-misses", "").Body.String(); !strings.Contains(got, `"query":"impresora"`) {
		t.Errorf("misses: %s", got)
	}
	if w := f.do("viewerTI", "GET", "/api/w/ti/search-misses", ""); w.Code != http.StatusNotFound {
		t.Errorf("viewer misses: %d", w.Code)
	}
}
