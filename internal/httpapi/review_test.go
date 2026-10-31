package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

type reviewState struct {
	OwnerID          string  `json:"owner_id"`
	Owner            string  `json:"owner"`
	IntervalDays     int     `json:"interval_days"`
	AreaIntervalDays int     `json:"area_interval_days"`
	VerifiedBy       string  `json:"verified_by"`
	DueAt            *string `json:"due_at"`
	Overdue          bool    `json:"overdue"`
	CanReview        bool    `json:"can_review"`
}

func pageReview(t *testing.T, f fixture, who, slug string) reviewState {
	t.Helper()
	w := f.do(who, "GET", "/api/w/ti/pages/"+slug, "")
	var out struct{ Review reviewState }
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatalf("GET %s as %s: %d %s", slug, who, w.Code, w.Body)
	}
	return out.Review
}

// TestPageReview: vigencia de §4.8. El dueño es quien crea; la verifica él o un admin, y para
// los demás no existe la acción; el dueño lo cambia solo un admin y el intervalo, el dueño o un
// admin; una edición cuenta como verificación; la cola y el filtro de búsqueda.
func TestPageReview(t *testing.T) {
	f := newFixture(t)
	if w := f.do("editorTI", "PUT", "/api/w/ti/pages/vpn", `{"title":"VPN","markdown":"x","base_revision":0}`); w.Code != http.StatusNoContent {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	r := pageReview(t, f, "editorTI", "vpn")
	if r.OwnerID != f.users["editorTI"] || r.Owner != "editorTI" || r.VerifiedBy != "editorTI" || r.DueAt != nil || !r.CanReview {
		t.Fatalf("new page: %+v", r)
	}
	// Apagada por defecto (§19.11): sin intervalo en el área ni en la página, no vence.
	if r.AreaIntervalDays != 0 || r.IntervalDays != 0 || r.Overdue {
		t.Fatalf("review is on by default: %+v", r)
	}
	if r := pageReview(t, f, "viewerTI", "vpn"); r.CanReview {
		t.Fatal("a reader can review someone else's page")
	}

	// El área fija 30 días y la página se verificó hace 40: vencida.
	if w := f.do("adminTI", "PATCH", "/api/w/ti/settings", `{"review_days":30}`); w.Code != http.StatusNoContent {
		t.Fatalf("settings: %d %s", w.Code, w.Body)
	}
	if _, err := f.db.Write.Exec(`UPDATE pages SET verified_at = datetime('now', '-40 days') WHERE slug = 'vpn'`); err != nil {
		t.Fatal(err)
	}
	if r := pageReview(t, f, "viewerTI", "vpn"); !r.Overdue || r.AreaIntervalDays != 30 || r.DueAt == nil {
		t.Fatalf("overdue page: %+v", r)
	}
	queue := func(who string) string {
		t.Helper()
		w := f.do(who, "GET", "/api/w/ti/review-queue", "")
		if w.Code != http.StatusOK {
			t.Fatalf("queue %s: %d", who, w.Code)
		}
		return strings.TrimSpace(w.Body.String())
	}
	if !strings.Contains(queue("editorTI"), `"slug":"vpn"`) || !strings.Contains(queue("adminTI"), `"slug":"vpn"`) {
		t.Fatal("the owner and the admin do not see the overdue page in the queue")
	}
	if q := queue("viewerTI"); q != "[]" {
		t.Fatalf("a reader's queue has someone else's page: %s", q)
	}
	for _, q := range []string{"estado:vencida", "status:overdue"} {
		w := f.do("viewerTI", "GET", "/api/w/ti/search?q="+strings.ReplaceAll(q, ":", "%3A")+"&facets=1", "")
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"slug":"vpn"`) || !strings.Contains(w.Body.String(), `"overdue":1`) ||
			!strings.Contains(w.Body.String(), `"snippet":[]`) { // solo filtros: sin fragmento, pero nunca null
			t.Fatalf("search %s: %d %s", q, w.Code, w.Body)
		}
	}

	// Verificar: un lector o un editor que no es el dueño no tiene la acción (404 uniforme).
	uniform := f.do("viewerTI", "GET", "/api/w/no-existe/tree", "")
	for _, who := range []string{"viewerTI", "outsider"} {
		if w := f.do(who, "POST", "/api/w/ti/verify/vpn", ""); w.Code != uniform.Code || w.Body.String() != uniform.Body.String() {
			t.Errorf("%s verifies: %d %s", who, w.Code, w.Body)
		}
	}
	if w := f.do("editorTI", "POST", "/api/w/ti/verify/vpn", ""); w.Code != http.StatusNoContent {
		t.Fatalf("owner verifies: %d %s", w.Code, w.Body)
	}
	if r := pageReview(t, f, "viewerTI", "vpn"); r.Overdue || r.VerifiedBy != "editorTI" {
		t.Fatalf("after verifying: %+v", r)
	}
	if q := queue("editorTI"); q != "[]" {
		t.Fatalf("verified page still in the queue: %s", q)
	}

	// Una edición también verifica, y la hace quien edita.
	f.db.Write.Exec(`UPDATE pages SET verified_at = datetime('now', '-40 days') WHERE slug = 'vpn'`)
	if w := f.do("adminTI", "PUT", "/api/w/ti/pages/vpn", `{"title":"VPN","markdown":"y","base_revision":1}`); w.Code != http.StatusNoContent {
		t.Fatalf("edit: %d %s", w.Code, w.Body)
	}
	if r := pageReview(t, f, "editorTI", "vpn"); r.Overdue || r.VerifiedBy != "adminTI" || r.OwnerID != f.users["editorTI"] {
		t.Fatalf("an edit did not verify (or changed the owner): %+v", r)
	}

	// El intervalo: el dueño sí; otro editor no; fuera de rango, 422 antes de buscar la página.
	if w := f.do("editorTI", "PATCH", "/api/w/ti/meta/vpn", `{"review_interval_days":90}`); w.Code != http.StatusNoContent {
		t.Fatalf("owner sets interval: %d %s", w.Code, w.Body)
	}
	if r := pageReview(t, f, "editorTI", "vpn"); r.IntervalDays != 90 {
		t.Fatalf("interval: %+v", r)
	}
	for _, body := range []string{`{"review_interval_days":-1}`, `{"review_interval_days":3651}`} {
		if w := f.do("editorTI", "PATCH", "/api/w/ti/meta/no-existe", body); w.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d", body, w.Code)
		}
	}
	// El dueño no se reasigna a sí mismo la página de otro, ni cambia el dueño: solo un admin.
	if w := f.do("editorTI", "PATCH", "/api/w/ti/meta/vpn", `{"owner_id":"`+f.users["adminTI"]+`"}`); w.Code != uniform.Code {
		t.Fatalf("an editor changes the owner: %d", w.Code)
	}
	// Un lector no puede ser dueño (no puede editarla); alguien de otra área tampoco.
	for _, who := range []string{"viewerTI", "adminFin"} {
		if w := f.do("adminTI", "PATCH", "/api/w/ti/meta/vpn", `{"owner_id":"`+f.users[who]+`"}`); w.Code != http.StatusUnprocessableEntity {
			t.Errorf("owner %s: %d %s", who, w.Code, w.Body)
		}
	}
	if w := f.do("adminTI", "PATCH", "/api/w/ti/meta/vpn", `{"owner_id":"`+f.users["adminTI"]+`","review_interval_days":0}`); w.Code != http.StatusNoContent {
		t.Fatalf("admin reassigns: %d %s", w.Code, w.Body)
	}
	if r := pageReview(t, f, "editorTI", "vpn"); r.OwnerID != f.users["adminTI"] || r.IntervalDays != 0 || r.CanReview {
		t.Fatalf("after reassigning: %+v", r)
	}

	// Una página nueva puede nacer con el intervalo de su plantilla.
	if w := f.do("editorTI", "PUT", "/api/w/ti/pages/acta", `{"title":"Acta","markdown":"x","base_revision":0,"review_interval_days":180}`); w.Code != http.StatusNoContent {
		t.Fatalf("create with interval: %d %s", w.Code, w.Body)
	}
	if r := pageReview(t, f, "editorTI", "acta"); r.IntervalDays != 180 {
		t.Fatalf("created interval: %+v", r)
	}
	if w := f.do("adminTI", "PATCH", "/api/w/ti/settings", `{"review_days":-5}`); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("negative area interval: %d", w.Code)
	}

	// Un dueño que ya no alcanza el nivel de su página no la ve en su cola: ni el título (§17).
	f.db.Write.Exec(`UPDATE pages SET min_rank = 3, verified_at = datetime('now', '-400 days') WHERE slug = 'acta'`)
	if q := queue("editorTI"); strings.Contains(q, "acta") {
		t.Fatalf("the queue leaks a page above the owner's level: %s", q)
	}
	if q := queue("adminTI"); !strings.Contains(q, `"slug":"acta"`) || !strings.Contains(q, `"owner":"editorTI"`) {
		t.Fatalf("the admin's queue: %s", q)
	}
}
