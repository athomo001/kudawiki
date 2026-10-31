package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type notificationOut struct {
	ID       string
	Kind     string
	Area     struct{ Slug string }
	Page     string
	Proposal string
	Title    string
	Actor    string
	Count    int
	Read     bool
}

// inbox corre el worker (lo que en el servidor hace cada 10 s) y devuelve la bandeja de who.
func (f fixture) inbox(t *testing.T, who string) ([]notificationOut, int) {
	t.Helper()
	if err := f.notify.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Items  []notificationOut
		Unread int
	}
	json.Unmarshal([]byte(f.expect(t, who, "GET", "/api/me/notifications", "", http.StatusOK)), &out)
	return out.Items, out.Unread
}

func kinds(ns []notificationOut) string {
	var k []string
	for _, n := range ns {
		if !n.Read {
			k = append(k, n.Kind)
		}
	}
	return strings.Join(k, ",")
}

// TestNotifications recorre §12.1 con personas reales: seguir una página y su subárbol, el
// seguimiento automático de quien crea, la agrupación de varios guardados en un aviso, marcar
// como leído al abrir, el borrado, y los avisos de propuestas de §5.3.
func TestNotifications(t *testing.T) {
	f := newFixture(t)
	f.expect(t, "adminTI", "PUT", "/api/w/ti/pages/guia", `{"title":"Guía","markdown":"Uno.\n\nDos.\n","base_revision":0}`, http.StatusNoContent)
	f.inbox(t, "adminTI") // reparte la creación: nadie más la seguía todavía
	f.expect(t, "viewerTI", "PUT", "/api/w/ti/watch/guia", `{"subtree":true}`, http.StatusNoContent)
	if got := f.expect(t, "viewerTI", "GET", "/api/w/ti/pages/guia", "", http.StatusOK); !strings.Contains(got, `"watch":"subtree"`) {
		t.Fatalf("watch state: %s", got)
	}

	// Dos guardados del editor: un solo aviso con count 2, para el lector y para la autora, que
	// sigue lo que creó. El editor no se avisa a sí mismo, y ahora sigue lo que editó.
	f.expect(t, "editorTI", "PUT", "/api/w/ti/pages/guia", `{"title":"Guía","markdown":"Uno.\n\nDos y medio.\n","base_revision":1}`, http.StatusNoContent)
	f.expect(t, "editorTI", "PUT", "/api/w/ti/pages/guia", `{"title":"Guía 2","markdown":"Uno.\n\nTres.\n","base_revision":2}`, http.StatusNoContent)
	ns, unread := f.inbox(t, "viewerTI")
	if unread != 1 || len(ns) != 1 || ns[0].Kind != "page_changed" || ns[0].Count != 2 || ns[0].Title != "Guía 2" || ns[0].Actor != "editorTI" || ns[0].Page != "guia" {
		t.Fatalf("viewer inbox: %d %+v", unread, ns)
	}
	if ns, _ := f.inbox(t, "adminTI"); kinds(ns) != "page_changed" {
		t.Fatalf("author inbox: %+v", ns)
	}
	if ns, _ := f.inbox(t, "editorTI"); len(ns) != 0 {
		t.Fatalf("the editor was notified of their own edit: %+v", ns)
	}
	if got := f.expect(t, "editorTI", "GET", "/api/w/ti/pages/guia", "", http.StatusOK); !strings.Contains(got, `"watch":"page"`) {
		t.Fatalf("auto-watch on edit: %s", got)
	}

	// Abrir la página marca su aviso como leído; el próximo cambio abre un aviso nuevo.
	f.expect(t, "viewerTI", "GET", "/api/w/ti/pages/guia", "", http.StatusOK)
	if _, unread := f.inbox(t, "viewerTI"); unread != 0 {
		t.Fatalf("opening the page did not mark it read: %d", unread)
	}

	// El subárbol: una subpágina nueva avisa a quien sigue "guia" con sus subpáginas, y su
	// borrado también.
	f.expect(t, "editorTI", "PUT", "/api/w/ti/pages/guia/anexo", `{"title":"Anexo","markdown":"x","base_revision":0}`, http.StatusNoContent)
	if ns, _ := f.inbox(t, "viewerTI"); kinds(ns) != "page_changed" || ns[0].Page != "guia/anexo" {
		t.Fatalf("subtree watch: %+v", ns)
	}
	if got := f.expect(t, "viewerTI", "GET", "/api/w/ti/pages/guia/anexo", "", http.StatusOK); !strings.Contains(got, `"watch_via":"guia"`) {
		t.Fatalf("watch via the parent: %s", got)
	}
	f.expect(t, "editorTI", "DELETE", "/api/w/ti/pages/guia/anexo", "", http.StatusNoContent)
	if ns, _ := f.inbox(t, "viewerTI"); kinds(ns) != "page_deleted" || ns[0].Title != "Anexo" || ns[0].Page != "" {
		t.Fatalf("delete: %+v", ns)
	}
	f.expect(t, "viewerTI", "POST", "/api/me/notifications/read", `{}`, http.StatusNoContent)

	// Dejar de seguir: ya no llega nada.
	f.expect(t, "viewerTI", "DELETE", "/api/w/ti/watch/guia", "", http.StatusNoContent)
	f.expect(t, "editorTI", "PUT", "/api/w/ti/pages/guia", `{"title":"Guía 2","markdown":"Cuatro.\n","base_revision":3}`, http.StatusNoContent)
	if _, unread := f.inbox(t, "viewerTI"); unread != 0 {
		t.Fatalf("unwatched page still notifies: %d", unread)
	}

	// Propuestas (§5.3): la nueva va a los admins del área, no a su autor; el rechazo, al autor.
	f.expect(t, "adminTI", "POST", "/api/me/notifications/read", `{}`, http.StatusNoContent)
	id := idOf(t, f.expect(t, "editorTI", "POST", "/api/w/ti/proposals", `{"slug":"guia","title":"Guía 2","markdown":"Cinco.\n","base_revision":4}`, http.StatusCreated))
	if ns, _ := f.inbox(t, "adminTI"); kinds(ns) != "proposal_pending" || ns[0].Title != "Guía 2" || ns[0].Actor != "editorTI" {
		t.Fatalf("pending: %+v", ns)
	}
	if ns, _ := f.inbox(t, "adminFin"); len(ns) != 0 {
		t.Fatalf("an admin of another area was notified: %+v", ns)
	}
	f.expect(t, "adminTI", "POST", "/api/w/ti/proposals/seen", "", http.StatusNoContent)
	if _, unread := f.inbox(t, "adminTI"); unread != 0 {
		t.Fatalf("seen did not mark the pending notice read: %d", unread)
	}
	f.expect(t, "adminTI", "POST", "/api/w/ti/proposals/"+id+"/reject", `{"comment":"Falta el paso dos"}`, http.StatusNoContent)
	ns, _ = f.inbox(t, "editorTI")
	if kinds(ns) != "proposal_rejected" || ns[0].Proposal != id || ns[0].Actor != "adminTI" {
		t.Fatalf("rejected: %+v", ns)
	}
	// Abrir la propuesta marca su aviso como leído.
	f.proposal(t, "editorTI", id)
	if _, unread := f.inbox(t, "editorTI"); unread != 0 {
		t.Fatalf("opening the proposal did not mark it read: %d", unread)
	}

	// Rebase: el admin publica algo que choca con la propuesta reenviada; el autor se entera.
	f.expect(t, "editorTI", "POST", "/api/w/ti/proposals/"+id+"/submit", "", http.StatusNoContent)
	f.expect(t, "adminTI", "PUT", "/api/w/ti/pages/guia", `{"title":"Guía 2","markdown":"Seis.\n","base_revision":4}`, http.StatusNoContent)
	if ns, _ := f.inbox(t, "editorTI"); !strings.Contains(kinds(ns), "proposal_rebase") {
		t.Fatalf("rebase: %+v", ns)
	}

	// Preferencias: el correo por categoría, con valores cerrados.
	f.expect(t, "editorTI", "PUT", "/api/me/notification-prefs", `{"auto_watch":false,"email":{"watched":"daily"}}`, http.StatusNoContent)
	if got := f.expect(t, "editorTI", "GET", "/api/me/notification-prefs", "", http.StatusOK); !strings.Contains(got, `"watched":"daily"`) ||
		!strings.Contains(got, `"auto_watch":false`) || !strings.Contains(got, `"proposals":"immediate"`) || !strings.Contains(got, `"email_enabled":false`) {
		t.Fatalf("prefs: %s", got)
	}
	f.expect(t, "editorTI", "PUT", "/api/me/notification-prefs", `{"auto_watch":true,"email":{"comments":"daily"}}`, http.StatusUnprocessableEntity)
	f.expect(t, "editorTI", "PUT", "/api/me/notification-prefs", `{"auto_watch":true,"email":{"watched":"weekly"}}`, http.StatusUnprocessableEntity)
}

// TestNotificationLevels: un aviso no delata una página que su destinatario ya no puede leer
// (§12.1, §17). Ni al repartir (le subieron el nivel antes) ni después (se lo subieron con el
// aviso ya en la bandeja). Tampoco le llega a quien dejó de ser miembro.
func TestNotificationLevels(t *testing.T) {
	f := newFixture(t)
	f.expect(t, "adminTI", "PUT", "/api/w/ti/pages/plan", `{"title":"Plan kwsecreto","markdown":"x","base_revision":0}`, http.StatusNoContent)
	f.expect(t, "viewerTI", "PUT", "/api/w/ti/watch/plan", `{"subtree":false}`, http.StatusNoContent)
	f.expect(t, "editorTI", "PUT", "/api/w/ti/watch/plan", `{"subtree":false}`, http.StatusNoContent)

	// El aviso llega, y después la página sube a N2: el lector ya no ve ni el aviso ni el título.
	f.expect(t, "adminTI", "PUT", "/api/w/ti/pages/plan", `{"title":"Plan kwsecreto","markdown":"y","base_revision":1}`, http.StatusNoContent)
	if _, unread := f.inbox(t, "viewerTI"); unread != 1 {
		t.Fatalf("control: the viewer should have the notice before the level change, got %d", unread)
	}
	f.expect(t, "adminTI", "PATCH", "/api/w/ti/meta/plan", `{"level":2}`, http.StatusNoContent)
	if got := f.expect(t, "viewerTI", "GET", "/api/me/notifications", "", http.StatusOK); strings.Contains(got, "kwsecreto") || !strings.Contains(got, `"unread":0`) {
		t.Fatalf("a notice leaked a page above the viewer's level: %s", got)
	}

	// Ya en N2 (cambiar el nivel deja la revisión 3): el cambio siguiente no se le reparte,
	// aunque la siga; al editor sí.
	f.expect(t, "adminTI", "PUT", "/api/w/ti/pages/plan", `{"title":"Plan kwsecreto","markdown":"z","base_revision":3}`, http.StatusNoContent)
	if ns, unread := f.inbox(t, "viewerTI"); len(ns) != 0 || unread != 0 {
		t.Fatalf("fan-out ignored the level: %+v", ns)
	}
	// Al bajarlo de nuevo vuelve el aviso que ya tenía (la creación y el primer cambio, agrupados),
	// pero no el cambio que se repartió mientras no podía leerla.
	f.expect(t, "adminTI", "PATCH", "/api/w/ti/meta/plan", `{"level":1}`, http.StatusNoContent)
	if ns, _ := f.inbox(t, "viewerTI"); len(ns) != 1 || ns[0].Count != 2 {
		t.Fatalf("after lowering the level only the earlier notice should be back: %+v", ns)
	}
	if ns, _ := f.inbox(t, "editorTI"); kinds(ns) != "page_changed" || ns[0].Count != 3 {
		t.Fatalf("editor inbox: %+v", ns)
	}

	// Sin membresía, nada: ni el aviso que ya tenía.
	f.expect(t, "adminTI", "DELETE", "/api/w/ti/members/"+f.users["editorTI"], "", http.StatusNoContent)
	if got := f.expect(t, "editorTI", "GET", "/api/me/notifications", "", http.StatusOK); strings.Contains(got, "kwsecreto") {
		t.Fatalf("a former member still sees the notice: %s", got)
	}

	// Seguir una página que no ve es el 404 uniforme, igual que una que no existe.
	f.expect(t, "adminTI", "PATCH", "/api/w/ti/meta/plan", `{"level":3}`, http.StatusNoContent)
	hidden := f.do("viewerTI", "PUT", "/api/w/ti/watch/plan", `{"subtree":false}`)
	missing := f.do("viewerTI", "PUT", "/api/w/ti/watch/no-existe", `{"subtree":false}`)
	if hidden.Code != http.StatusNotFound || hidden.Body.String() != missing.Body.String() {
		t.Fatalf("watch a hidden page: %d %s / %s", hidden.Code, hidden.Body, missing.Body)
	}
}

// TestUnsubscribeRoute: la baja en un clic va sin sesión ni Origin, porque la manda el servidor
// de correo (RFC 8058); un enlace adulterado es el 404 uniforme, no un 403 de CSRF. La firma
// válida se prueba en el paquete notify.
func TestUnsubscribeRoute(t *testing.T) {
	f := newFixture(t)
	r := httptest.NewRequest("POST", "/unsubscribe?t="+f.users["viewerTI"]+".watched.firma-falsa", strings.NewReader("List-Unsubscribe=One-Click"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "not_found") {
		t.Fatalf("forged unsubscribe: %d %s", w.Code, w.Body)
	}
	// Abrir el enlace muestra la interfaz (la confirmación), sin dejar el token en el Referer.
	w = f.do("", "GET", "/unsubscribe?t=x", "")
	if w.Code != http.StatusOK || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("unsubscribe page: %d %v", w.Code, w.Header())
	}
}
