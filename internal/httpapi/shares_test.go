package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestShares recorre los artículos compartidos de §4.19 con personas reales: el pedido de un
// editor y la revisión del admin del origen, la lectura en el destino (solo el artículo, sus
// enlaces como texto y sus imágenes por el vínculo), la búsqueda, las propuestas obligatorias
// mientras se comparte, y cada forma de terminar: subir de nivel, quitarlo, retirarlo y borrarlo.
func TestShares(t *testing.T) {
	f := newFixture(t)
	var ti, fin string
	f.db.Read.QueryRow(`SELECT id FROM workspaces WHERE slug = 'ti'`).Scan(&ti)
	f.db.Read.QueryRow(`SELECT id FROM workspaces WHERE slug = 'finanzas'`).Scan(&fin)
	upload := func(name string) string {
		var up struct{ URL string }
		json.Unmarshal(f.upload("adminTI", "ti", name, pngBytes(t)).Body.Bytes(), &up)
		return up.URL
	}
	img, other := upload("vpn.png"), upload("wifi.png")
	f.expect(t, "adminTI", "PUT", "/api/w/ti/pages/redes/vpn", `{"title":"VPN","markdown":"Conectarse. Ver [[redes/wifi]].\n\n![vpn](`+img+`)","base_revision":0}`, http.StatusNoContent)
	f.expect(t, "adminTI", "PUT", "/api/w/ti/pages/redes/wifi", `{"title":"Wifi","markdown":"![wifi](`+other+`)","base_revision":0}`, http.StatusNoContent)
	f.expect(t, "adminTI", "PUT", "/api/w/ti/pages/secreta", `{"title":"Secreta","markdown":"x","base_revision":0}`, http.StatusNoContent)
	f.expect(t, "adminTI", "PATCH", "/api/w/ti/meta/secreta", `{"level":2}`, http.StatusNoContent)
	request := func(who, slug string, code int) bool {
		t.Helper()
		var out struct{ Approved bool }
		json.Unmarshal([]byte(f.expect(t, who, "POST", "/api/w/ti/shares", `{"page":"`+slug+`","targets":["`+fin+`"]}`, code)), &out)
		return out.Approved
	}
	pending := func() string {
		t.Helper()
		var list []struct{ ID, Status string }
		json.Unmarshal([]byte(f.expect(t, "adminTI", "GET", "/api/w/ti/shares", "", http.StatusOK)), &list)
		if len(list) != 1 {
			t.Fatalf("source list: %+v", list)
		}
		return list[0].ID
	}
	sharedIn := func() string {
		t.Helper()
		return strings.TrimSpace(f.expect(t, "adminFin", "GET", "/api/w/finanzas/shared", "", http.StatusOK))
	}

	// Pedir: editores y admins ven los nombres de las demás áreas; los lectores no piden.
	if got := f.expect(t, "editorTI", "GET", "/api/w/ti/share-targets", "", http.StatusOK); !strings.Contains(got, `"Finanzas"`) || strings.Contains(got, `"TI"`) {
		t.Errorf("targets: %s", got)
	}
	f.expect(t, "viewerTI", "GET", "/api/w/ti/share-targets", "", http.StatusNotFound)
	f.expect(t, "editorTI", "POST", "/api/w/ti/shares", `{"page":"redes/vpn","targets":["`+ti+`"]}`, http.StatusUnprocessableEntity)
	request("editorTI", "secreta", http.StatusUnprocessableEntity) // solo lo N1 se comparte

	// El pedido de un editor espera al admin del origen: el destino todavía no lo ve.
	if request("editorTI", "redes/vpn", http.StatusOK) {
		t.Fatal("an editor's request was approved without review")
	}
	if ns, _ := f.inbox(t, "adminTI"); kinds(ns) != "share_pending" {
		t.Fatalf("admin inbox: %+v", ns)
	}
	if got := sharedIn(); got != "[]" {
		t.Fatalf("a pending share reached the target: %s", got)
	}
	id := pending()
	f.expect(t, "adminTI", "POST", "/api/w/ti/shares/"+id+"/reject", `{"comment":" "}`, http.StatusUnprocessableEntity)
	f.expect(t, "adminTI", "POST", "/api/w/ti/shares/"+id+"/reject", `{"comment":"Falta revisar"}`, http.StatusNoContent)
	f.expect(t, "adminTI", "POST", "/api/w/ti/shares/"+id+"/approve", "", http.StatusConflict)
	if ns, _ := f.inbox(t, "editorTI"); kinds(ns) != "share_rejected" || ns[0].Target != "Finanzas" {
		t.Fatalf("editor inbox after reject: %+v", ns)
	}

	// Aprobado, aparece directo en el Compartido del destino (§19.40 b) y su admin se entera.
	request("editorTI", "redes/vpn", http.StatusOK)
	id = pending()
	f.expect(t, "adminTI", "POST", "/api/w/ti/shares/"+id+"/approve", "", http.StatusNoContent)
	if ns, _ := f.inbox(t, "editorTI"); !strings.HasPrefix(kinds(ns), "share_approved") {
		t.Fatalf("editor inbox after approve: %+v", ns)
	}
	if ns, _ := f.inbox(t, "adminFin"); kinds(ns) != "shared_received" || ns[0].From != "TI" || ns[0].Share != id || ns[0].Title != "VPN" {
		t.Fatalf("target inbox: %+v", ns)
	}
	if got := sharedIn(); !strings.Contains(got, `"id":"`+id+`"`) || !strings.Contains(got, `"name":"TI"`) {
		t.Fatalf("target shared list: %s", got)
	}
	for _, who := range []string{"adminTI", "adminFin"} {
		area := map[string]string{"adminTI": "ti", "adminFin": "finanzas"}[who]
		if got := f.expect(t, who, "GET", "/api/w/"+area+"/audit", "", http.StatusOK); !strings.Contains(got, `"share.approve"`) {
			t.Errorf("%s audit without the approval: %s", area, got)
		}
	}

	// El destino lee solo el artículo: sin su ruta, con los enlaces como texto y la imagen por el vínculo.
	article := f.expect(t, "adminFin", "GET", "/api/w/finanzas/shared/"+id, "", http.StatusOK)
	mediaID := img[strings.LastIndex(img, "/")+1:]
	if strings.Contains(article, "/w/ti") || strings.Contains(article, "href=") || !strings.Contains(article, "/api/w/finanzas/shared/"+id+"/media/"+mediaID) {
		t.Fatalf("shared article: %s", article)
	}
	if w := f.do("adminFin", "GET", "/api/w/finanzas/shared/"+id+"/media/"+mediaID, ""); w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/png" {
		t.Errorf("shared media: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	// Lo que rodea al artículo sigue cerrado: otra imagen del origen, la página enlazada y el área.
	f.expect(t, "adminFin", "GET", "/api/w/finanzas/shared/"+id+"/media/"+other[strings.LastIndex(other, "/")+1:], "", http.StatusNotFound)
	f.expect(t, "adminFin", "GET", img, "", http.StatusNotFound)
	f.expect(t, "adminFin", "GET", "/api/w/ti/pages/redes/wifi", "", http.StatusNotFound)
	f.expect(t, "adminFin", "GET", "/api/w/ti/shared/"+id, "", http.StatusNotFound)
	f.expect(t, "editorTI", "DELETE", "/api/w/ti/shares/"+id, "", http.StatusNotFound) // retirar es del admin
	if got := f.expect(t, "adminFin", "GET", "/api/w/finanzas/search?q=conectarse", "", http.StatusOK); !strings.Contains(got, `"shared":[{"area":{"name":"TI"},"id":"`+id+`"`) {
		t.Errorf("search in the target: %s", got)
	}
	if got := f.expect(t, "adminTI", "GET", "/api/w/ti/access/redes/vpn", "", http.StatusOK); !strings.Contains(got, `"shared_with":[{"area":"Finanzas","members":1}]`) {
		t.Errorf("access: %s", got)
	}

	// Mientras se comparte, el editor cambia el artículo solo por propuesta; el resto del área, igual que antes.
	if got := f.expect(t, "editorTI", "GET", "/api/w/ti/approval/redes/vpn", "", http.StatusOK); !strings.Contains(got, `"must_propose":true`) {
		t.Errorf("approval on a shared page: %s", got)
	}
	f.expect(t, "editorTI", "PUT", "/api/w/ti/pages/redes/vpn", `{"title":"VPN","markdown":"y","base_revision":1}`, http.StatusConflict)
	if got := f.expect(t, "editorTI", "GET", "/api/w/ti/approval/redes/wifi", "", http.StatusOK); !strings.Contains(got, `"must_propose":false`) {
		t.Errorf("approval on another page: %s", got)
	}

	// Subir de nivel la deja de compartir, y volver a N1 no la trae de vuelta.
	f.expect(t, "adminTI", "PATCH", "/api/w/ti/meta/redes/vpn", `{"level":2}`, http.StatusNoContent)
	f.expect(t, "adminFin", "GET", "/api/w/finanzas/shared/"+id, "", http.StatusNotFound)
	f.expect(t, "adminTI", "PATCH", "/api/w/ti/meta/redes/vpn", `{"level":1}`, http.StatusNoContent)
	if got := sharedIn(); got != "[]" {
		t.Fatalf("shared after a level change: %s", got)
	}
	if got := f.expect(t, "adminTI", "GET", "/api/w/ti/audit", "", http.StatusOK); !strings.Contains(got, `"share.end"`) {
		t.Errorf("audit without the end: %s", got)
	}

	// Un admin del origen comparte sin revisión; el admin del destino la quita de su Compartido.
	if !request("adminTI", "redes/vpn", http.StatusOK) {
		t.Fatal("an admin's request was not approved")
	}
	id = pending()
	f.expect(t, "adminFin", "DELETE", "/api/w/finanzas/shared/"+id, "", http.StatusNoContent)
	f.expect(t, "adminFin", "DELETE", "/api/w/finanzas/shared/"+id, "", http.StatusNotFound)
	if got := sharedIn(); got != "[]" {
		t.Fatalf("shared after removal: %s", got)
	}

	// El admin del origen la retira.
	request("adminTI", "redes/vpn", http.StatusOK)
	id = pending()
	f.expect(t, "adminTI", "DELETE", "/api/w/ti/shares/"+id, "", http.StatusNoContent)
	f.expect(t, "adminTI", "DELETE", "/api/w/ti/shares/"+id, "", http.StatusConflict)
	if got := sharedIn(); got != "[]" {
		t.Fatalf("shared after unsharing: %s", got)
	}

	// Borrada, deja de compartirse y no vuelve al restaurarla.
	request("adminTI", "redes/vpn", http.StatusOK)
	f.expect(t, "adminTI", "DELETE", "/api/w/ti/pages/redes/vpn", "", http.StatusNoContent)
	var trash []struct{ ID string }
	json.Unmarshal([]byte(f.expect(t, "adminTI", "GET", "/api/w/ti/trash", "", http.StatusOK)), &trash)
	f.expect(t, "adminTI", "POST", "/api/w/ti/trash/"+trash[0].ID+"/restore", "", http.StatusNoContent)
	if got := sharedIn(); got != "[]" {
		t.Fatalf("shared after delete and restore: %s", got)
	}
	var status string
	f.db.Read.QueryRow(`SELECT end_reason FROM page_shares ORDER BY created_at DESC, id DESC LIMIT 1`).Scan(&status)
	if status != "deleted" {
		t.Errorf("end reason after delete: %q", status)
	}
}
