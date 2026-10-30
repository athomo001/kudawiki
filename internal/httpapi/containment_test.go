package httpapi

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// Matriz de contención de SPEC §17: cada persona × cada endpoint del área × cada nivel de
// página. Es el criterio de salida de F2: ningún caso encuentra una fuga.

// rank es el nivel de cada persona en el área "ti"; 0 = no es miembro (incluido el
// superadmin, que no es miembro implícito, §3.1).
var rank = map[string]int{"viewerTI": 1, "editorTI": 2, "adminTI": 3, "adminFin": 0, "outsider": 0, "super": 0}

// leaks son marcas únicas puestas en el título, los encabezados, el contenido y las etiquetas
// de cada página: si una aparece en una respuesta para alguien de rango menor, hay fuga.
var leaks = map[int]string{1: "kwvisible", 2: "kwleakdos", 3: "kwleaktres"}

// trashed es la marca de una página en la papelera: solo puede aparecer en la papelera misma.
const trashed = "kwpapelera"

type seeded struct {
	slug       map[int]string // nivel → ruta; 0 = ruta inexistente
	media      map[int]string // nivel → URL de su media
	trashID    string         // la unidad de la papelera, borrada por editorTI
	trashMedia string         // URL de la media de la página borrada
}

func setLevel(t *testing.T, f fixture, slug string, rank int) {
	t.Helper()
	// La interfaz para elegir el nivel llega en F3. El nivel vive solo en pages: la búsqueda
	// filtra uniendo con ella, así que no hay copia que sincronizar.
	if _, err := f.db.Write.Exec(`UPDATE pages SET min_rank = ? WHERE slug = ?`, rank, slug); err != nil {
		t.Fatal(err)
	}
}

// seed arma dos áreas con contenido: en "ti", una página por nivel con su media, sus etiquetas
// y enlaces cruzados; en "finanzas", una página que nadie de "ti" debe ver.
func seed(t *testing.T, f fixture) seeded {
	t.Helper()
	s := seeded{slug: map[int]string{0: "nivel/no-existe"}, media: map[int]string{0: "/api/w/ti/media/01ARZ3NDEKTSV4RRFFQ69G5FAV"}}
	for lvl := 1; lvl <= 3; lvl++ {
		mark, slug := leaks[lvl], []string{"", "nivel/uno", "nivel/dos", "nivel/tres"}[lvl]
		w := f.upload("adminTI", "ti", mark+".png", pngBytes(t))
		var up struct{ URL string }
		json.Unmarshal(w.Body.Bytes(), &up)
		md := "## Sección " + mark + "\\nTexto " + mark + ". Ver [[nivel/uno]], [[nivel/dos]] y [[nivel/tres]].\\n\\n![" + mark + "](" + up.URL + ")"
		body := `{"title":"Plan ` + mark + `","markdown":"` + md + `","base_revision":0}`
		if w := f.do("adminTI", "PUT", "/api/w/ti/pages/"+slug, body); w.Code != http.StatusNoContent {
			t.Fatalf("seed %s: %d %s", slug, w.Code, w.Body)
		}
		if w := f.do("adminTI", "PATCH", "/api/w/ti/meta/"+slug, `{"tags":["`+mark+`","comun"]}`); w.Code != http.StatusNoContent {
			t.Fatalf("tags %s: %d %s", slug, w.Code, w.Body)
		}
		setLevel(t, f, slug, lvl)
		s.slug[lvl], s.media[lvl] = slug, up.URL
	}
	if w := f.do("adminFin", "PUT", "/api/w/finanzas/pages/plan", `{"title":"Plan kwleakfin","markdown":"kwleakfin","base_revision":0}`); w.Code != http.StatusNoContent {
		t.Fatalf("seed finanzas: %d %s", w.Code, w.Body)
	}
	s.trashID, s.trashMedia = seedTrash(t, f)
	return s
}

// seedTrash borra (editorTI) una página N1 con media, etiquetas y enlaces, y crea otra viva en
// su ruta: así restaurarla choca (409) y la matriz no cambia nada.
func seedTrash(t *testing.T, f fixture) (id, mediaURL string) {
	t.Helper()
	var up struct{ URL string }
	json.Unmarshal(f.upload("editorTI", "ti", trashed+".png", pngBytes(t)).Body.Bytes(), &up)
	md := "## " + trashed + "\\nVer [[nivel/uno]].\\n\\n![" + trashed + "](" + up.URL + ")"
	for _, step := range []struct{ who, method, path, body string }{
		{"editorTI", "PUT", "/api/w/ti/pages/papelera/borrada", `{"title":"Plan ` + trashed + `","markdown":"` + md + `","base_revision":0}`},
		{"editorTI", "PATCH", "/api/w/ti/meta/papelera/borrada", `{"tags":["` + trashed + `","comun"]}`},
		{"editorTI", "DELETE", "/api/w/ti/pages/papelera/borrada", ""},
		{"adminTI", "PUT", "/api/w/ti/pages/papelera/borrada", `{"title":"Ocupa","markdown":"x","base_revision":0}`},
	} {
		if w := f.do(step.who, step.method, step.path, step.body); w.Code != http.StatusNoContent {
			t.Fatalf("seed trash %s %s: %d %s", step.method, step.path, w.Code, w.Body)
		}
	}
	var items []struct{ ID string }
	json.Unmarshal(f.do("adminTI", "GET", "/api/w/ti/trash", "").Body.Bytes(), &items)
	if len(items) != 1 {
		t.Fatalf("seed trash: %d items", len(items))
	}
	return items[0].ID, up.URL
}

// probe es un endpoint del área. need es el rango mínimo de la ruta (4 = nadie); page dice si
// actúa sobre la página del nivel en prueba. ok es el código esperado cuando corresponde: las escrituras
// van con datos que se rechazan, así la matriz no cambia nada.
type probe struct {
	route, method, path, body string
	need                      int
	page                      bool
	ok                        int
}

func probes(s seeded, lvl int) []probe {
	p, m := s.slug[lvl], s.media[lvl]
	return []probe{
		{"/api/w/{ws}/pages/*", "GET", "/api/w/ti/pages/" + p, "", 1, true, 200},
		{"/api/w/{ws}/history/*", "GET", "/api/w/ti/history/" + p, "", 1, true, 200},
		{"/api/w/{ws}/revisions/{n}/*", "GET", "/api/w/ti/revisions/1/" + p, "", 1, true, 200},
		{"/api/w/{ws}/diff/*", "GET", "/api/w/ti/diff/" + p + "?from=1&to=1", "", 1, true, 200},
		{"/api/w/{ws}/backlinks/*", "GET", "/api/w/ti/backlinks/" + p, "", 1, true, 200},
		{"/api/w/{ws}/access/*", "GET", "/api/w/ti/access/" + p + "?simulate=3", "", 3, true, 200},
		{"/api/w/{ws}/media/{id}", "GET", m, "", 1, true, 200},
		{"/api/w/{ws}/pages/*", "PUT", "/api/w/ti/pages/" + p, `{"title":"x","markdown":"y","base_revision":999}`, 2, true, 409},
		{"/api/w/{ws}/revert/*", "POST", "/api/w/ti/revert/" + p, `{"revision":1,"base_revision":999}`, 2, true, 409},
		// meta y move validan antes de buscar la página: la respuesta es la misma exista o no.
		{"/api/w/{ws}/meta/*", "PATCH", "/api/w/ti/meta/" + p, `{"tags":["<x>"]}`, 2, false, 422},
		{"/api/w/{ws}/meta/*", "PATCH", "/api/w/ti/meta/" + p, `{"level":9}`, 2, false, 422},
		{"/api/w/{ws}/move/*", "POST", "/api/w/ti/move/" + p, `{"to":"Mal Slug"}`, 2, false, 422},
		{"/api/w/{ws}/tree", "GET", "/api/w/ti/tree", "", 1, false, 200},
		{"/api/w/{ws}/tags", "GET", "/api/w/ti/tags", "", 1, false, 200},
		{"/api/w/{ws}/tags/{tag}", "GET", "/api/w/ti/tags/comun", "", 1, false, 200},
		{"/api/w/{ws}/search", "GET", "/api/w/ti/search?q=plan", "", 1, false, 200},
		{"/api/w/{ws}/search", "GET", "/api/w/ti/search?q=kwleaktres", "", 1, false, 200},
		{"/api/w/{ws}/search", "GET", "/api/w/ti/search?q=kwleakdoss", "", 1, false, 200}, // tipeo: "¿quisiste decir?"
		{"/api/w/{ws}/render", "POST", "/api/w/ti/render", `{"markdown":"[[nivel/tres]] [[children]]"}`, 2, false, 200},
		{"/api/w/{ws}/media", "POST", "/api/w/ti/media", "not-multipart", 2, false, 400},
		{"/api/w/{ws}/search-misses", "GET", "/api/w/ti/search-misses", "", 3, false, 200},
		{"/api/w/{ws}/audit", "GET", "/api/w/ti/audit", "", 3, false, 200},
		{"/api/w/{ws}/members/", "GET", "/api/w/ti/members", "", 3, false, 200},
		{"/api/w/{ws}/settings", "GET", "/api/w/ti/settings", "", 3, false, 200},
		{"/api/w/{ws}/health", "GET", "/api/w/ti/health", "", 3, false, 200},
		// Con un nombre vacío valida y no cambia nada: la matriz no altera el área.
		{"/api/w/{ws}/settings", "PATCH", "/api/w/ti/settings", `{"name":""}`, 3, false, 422},
		{"/api/w/{ws}/members/", "POST", "/api/w/ti/members", `{"email":"x","role":"admin"}`, 3, false, 422},
		{"/api/w/{ws}/members/{user_id}", "DELETE", "/api/w/ti/members/no-existe", "", 3, false, 204},
		{"/api/w/{ws}/trash", "GET", "/api/w/ti/trash", "", 2, false, 200},
		// La ruta de la página borrada está ocupada: restaurar choca y no cambia nada.
		{"/api/w/{ws}/trash/{id}/restore", "POST", "/api/w/ti/trash/" + s.trashID + "/restore", "", 2, false, 409},
		{"/api/w/{ws}/media/{id}", "GET", s.trashMedia, "", 4, false, 0},
		// Inicio del área (§4.17). La visita sí suma, pero solo en las páginas que quien la
		// pide ve; el barrido de fugas revisa después que las más vistas no delaten las otras.
		{"/api/w/{ws}/views/*", "POST", "/api/w/ti/views/" + p, "", 1, true, 204},
		{"/api/w/{ws}/stats", "GET", "/api/w/ti/stats", "", 1, false, 200},
		{"/api/w/{ws}/announcements", "GET", "/api/w/ti/announcements", "", 1, false, 200},
		{"/api/w/{ws}/announcements", "POST", "/api/w/ti/announcements", `{"title":"","body":""}`, 2, false, 422},
		// Uno que no existe: para todos el 404 uniforme (quién puede borrar, en TestAnnouncements).
		{"/api/w/{ws}/announcements/{id}", "DELETE", "/api/w/ti/announcements/no-existe", "", 2, false, 404},
	}
}

func TestContainmentMatrix(t *testing.T) {
	f := newFixture(t)
	s := seed(t, f)
	uniform := f.do("adminTI", "GET", "/api/w/no-existe/tree", "")

	for who, r := range rank {
		var bodies, trashBodies []string
		for lvl := 0; lvl <= 3; lvl++ {
			for _, p := range probes(s, lvl) {
				w := f.do(who, p.method, p.path, p.body)
				allowed := r >= p.need && (!p.page || (lvl > 0 && r >= lvl))
				name := who + " " + p.method + " " + p.path
				switch {
				case allowed && w.Code != p.ok:
					t.Errorf("%s: %d, want %d (%s)", name, w.Code, p.ok, w.Body)
				case !allowed && (w.Code != uniform.Code || w.Body.String() != uniform.Body.String()):
					t.Errorf("%s: %d %s, want the uniform 404", name, w.Code, w.Body)
				}
				switch {
				case w.Code < 300 && p.route == "/api/w/{ws}/trash":
					trashBodies = append(trashBodies, w.Body.String())
				case w.Code < 300:
					bodies = append(bodies, w.Body.String())
				}
			}
		}
		// Barrido de fugas: ninguna respuesta lleva la marca de algo de rango mayor ni de otra área.
		all := strings.Join(bodies, "\n")
		for lvl, mark := range leaks {
			switch seen := strings.Contains(all, mark); {
			case r < lvl && seen:
				t.Errorf("%s (rank %d) received %q from a level-%d page", who, r, mark, lvl)
			case r >= lvl && !seen: // control: si lo permitido no llega, el barrido no prueba nada
				t.Errorf("%s (rank %d) never received %q: the sweep is not looking at real content", who, r, mark)
			}
		}
		if strings.Contains(all, "kwleakfin") {
			t.Errorf("%s received content from another area", who)
		}
		// Lo borrado solo aparece en la papelera, y ahí solo para el admin y para quien lo borró.
		if strings.Contains(all, trashed) {
			t.Errorf("%s received a page in the trash outside the trash", who)
		}
		if seen, want := strings.Contains(strings.Join(trashBodies, ""), trashed), who == "adminTI" || who == "editorTI"; seen != want {
			t.Errorf("%s sees the trashed page in the trash: %v, want %v", who, seen, want)
		}
	}
}

// Todo endpoint nuevo entra en la matriz (AGENTS.md §1): si una ruta del router no está aquí
// ni en la lista de las que se prueban aparte, este test falla.
func TestEveryRouteIsInTheMatrix(t *testing.T) {
	f := newFixture(t)
	covered := map[string]bool{}
	for _, p := range probes(seeded{slug: map[int]string{1: "x"}, media: map[int]string{1: "x"}}, 1) {
		covered[p.method+" "+p.route] = true
	}
	// Rutas fuera de un área: sin sesión, solo de superadmin o de la persona misma. Se prueban
	// en TestLoginMeLogout, TestPreferences, TestProfile, TestInviteFlow, TestInviteResend, TestHealthViews,
	// TestAreaAdminLimits, TestRoutingFallbacks, TestBranding y TestSMTPAdmin (la marca y el correo son de la instalación,
	// no de un área). Borrar
	// cambia los datos de verdad: tiene su propia matriz en TestDeleteContainment. La auditoría
	// del superadmin se prueba en TestAuditViews.
	elsewhere := []string{
		"DELETE /api/w/{ws}/pages/*", "GET /api/admin/audit",
		"GET /healthz", "GET /readyz", "GET /*",
		"POST /api/auth/login", "POST /api/auth/invite/accept", "POST /api/auth/logout", "GET /api/auth/me", "PATCH /api/me",
		"POST /api/me/password", "GET /api/me/avatar", "PUT /api/me/avatar", "DELETE /api/me/avatar",
		"GET /api/admin/users", "POST /api/admin/users", "GET /api/admin/health", "POST /api/admin/users/{id}/invite", "GET /api/admin/workspaces", "POST /api/admin/workspaces",
		"GET /api/admin/workspaces/{id}/members/", "POST /api/admin/workspaces/{id}/members/",
		"DELETE /api/admin/workspaces/{id}/members/{user_id}",
		"GET /api/admin/site", "PATCH /api/admin/site", "POST /api/admin/site/preview",
		"PUT /api/admin/site/assets/{kind}", "DELETE /api/admin/site/assets/{kind}", "GET /_site/{name}",
		"GET /api/admin/smtp", "PUT /api/admin/smtp", "POST /api/admin/smtp/test",
	}
	err := chi.Walk(f.h.(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		key := method + " " + route
		if method != "HEAD" && !covered[key] && !slices.Contains(elsewhere, key) {
			t.Errorf("route %s is not in the containment matrix", key)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
