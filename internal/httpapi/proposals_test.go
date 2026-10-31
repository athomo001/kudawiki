package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// expect hace el pedido y falla si el código no es el esperado; devuelve el cuerpo.
func (f fixture) expect(t *testing.T, who, method, path, body string, code int) string {
	t.Helper()
	w := f.do(who, method, path, body)
	if w.Code != code {
		t.Fatalf("%s %s %s: %d, want %d (%s)", who, method, path, w.Code, code, w.Body)
	}
	return w.Body.String()
}

func idOf(t *testing.T, body string) string {
	t.Helper()
	var out struct{ ID string }
	if err := json.Unmarshal([]byte(body), &out); err != nil || out.ID == "" {
		t.Fatalf("no id in %s", body)
	}
	return out.ID
}

type proposalOut struct {
	Status         string
	Author         string
	Reviewer       string
	Comment        string
	PageChanged    bool `json:"page_changed"`
	Conflict       bool
	MergedRevision int `json:"merged_revision"`
}

func (f fixture) proposal(t *testing.T, who, id string) proposalOut {
	t.Helper()
	var p proposalOut
	json.Unmarshal([]byte(f.expect(t, who, "GET", "/api/w/ti/proposals/"+id, "", http.StatusOK)), &p)
	return p
}

const vpn = `# VPN\n\nPaso uno.\nPaso dos.\nPaso tres.\n\n## Si falla\n\nLlama a soporte.\n`

// TestProposals recorre el ciclo de §5.2 con dos personas reales: el área en modo required, la
// propuesta de un editor que se mezcla sola con un cambio del admin, la de un lector que choca
// y se rehace, el rechazo con comentario, el reenvío, el retiro y una página nueva.
func TestProposals(t *testing.T) {
	f := newFixture(t)
	f.expect(t, "adminTI", "PUT", "/api/w/ti/pages/redes/vpn", `{"title":"VPN","markdown":"`+vpn+`","base_revision":0}`, http.StatusNoContent)

	// Modo required (§5.1): el editor ya no publica directo, ni guarda, ni mueve, ni etiqueta.
	f.expect(t, "adminTI", "PATCH", "/api/w/ti/settings", `{"approval_mode":"required"}`, http.StatusNoContent)
	f.expect(t, "editorTI", "PUT", "/api/w/ti/pages/redes/vpn", `{"title":"VPN","markdown":"x","base_revision":1}`, http.StatusConflict)
	f.expect(t, "editorTI", "PATCH", "/api/w/ti/meta/redes/vpn", `{"tags":["vpn"]}`, http.StatusConflict)
	f.expect(t, "editorTI", "POST", "/api/w/ti/move/redes/vpn", `{"to":"redes/otra"}`, http.StatusConflict)
	if got := f.expect(t, "editorTI", "GET", "/api/w/ti/approval/redes/vpn", "", http.StatusOK); !strings.Contains(got, `"must_propose":true`) {
		t.Errorf("approval in a required area: %s", got)
	}
	// Una excepción por subárbol lo libera; la más larga gana (§5.2.1).
	f.expect(t, "adminTI", "PUT", "/api/w/ti/approval-overrides/redes", `{"approval_mode":"direct"}`, http.StatusNoContent)
	f.expect(t, "adminTI", "PUT", "/api/w/ti/approval-overrides/redes/vpn", `{"approval_mode":"required"}`, http.StatusNoContent)
	for slug, want := range map[string]string{"redes/wifi": "false", "redes/vpn": "true", "redes/vpn/anexo": "true", "otra": "true"} {
		if got := f.expect(t, "editorTI", "GET", "/api/w/ti/approval/"+slug, "", http.StatusOK); !strings.Contains(got, `"must_propose":`+want) {
			t.Errorf("approval %s: %s, want %s", slug, got, want)
		}
	}
	// El admin siempre publica directo.
	if got := f.expect(t, "adminTI", "GET", "/api/w/ti/approval/redes/vpn", "", http.StatusOK); !strings.Contains(got, `"must_propose":false`) {
		t.Errorf("admin: %s", got)
	}

	// El editor propone un cambio al final; el lector no puede hasta que el área lo permita (§19.5).
	editorChange := strings.Replace(vpn, `Llama a soporte.`, `Llama a soporte (anexo 200).`, 1)
	byEditor := idOf(t, f.expect(t, "editorTI", "POST", "/api/w/ti/proposals",
		`{"slug":"redes/vpn","title":"VPN","markdown":"`+editorChange+`","base_revision":1,"change_summary":"Anexo"}`, http.StatusCreated))
	viewerChange := strings.Replace(vpn, `# VPN`, `# VPN remota`, 1)
	viewerBody := `{"slug":"redes/vpn","title":"VPN","markdown":"` + viewerChange + `","base_revision":1}`
	f.expect(t, "viewerTI", "POST", "/api/w/ti/proposals", viewerBody, http.StatusNotFound)
	f.expect(t, "adminTI", "PATCH", "/api/w/ti/settings", `{"viewers_can_propose":true}`, http.StatusNoContent)
	byViewer := idOf(t, f.expect(t, "viewerTI", "POST", "/api/w/ti/proposals", viewerBody, http.StatusCreated))
	// Un lector propone cambios a lo que lee, no páginas nuevas.
	f.expect(t, "viewerTI", "POST", "/api/w/ti/proposals", `{"slug":"redes/nueva","title":"N","markdown":"n","base_revision":0}`, http.StatusNotFound)
	// Una propuesta sin cambios no se guarda.
	f.expect(t, "editorTI", "POST", "/api/w/ti/proposals", `{"slug":"redes/vpn","title":"VPN","markdown":"`+vpn+`","base_revision":1}`, http.StatusUnprocessableEntity)

	// Cada uno ve las suyas; el admin, las del área. La ajena no existe para el editor.
	if got := f.expect(t, "editorTI", "GET", "/api/w/ti/proposals?view=pending", "", http.StatusOK); strings.Count(got, `"id"`) != 1 {
		t.Errorf("editor pending: %s", got)
	}
	if got := f.expect(t, "adminTI", "GET", "/api/w/ti/proposals?view=pending", "", http.StatusOK); strings.Count(got, `"id"`) != 2 {
		t.Errorf("admin pending: %s", got)
	}
	f.expect(t, "editorTI", "GET", "/api/w/ti/proposals/"+byViewer, "", http.StatusNotFound)
	// Aprobar es del admin.
	f.expect(t, "editorTI", "POST", "/api/w/ti/proposals/"+byViewer+"/approve", `{}`, http.StatusNotFound)

	// El admin cambia el título en la página: la del editor se mezcla sola (§5.2).
	adminChange := strings.Replace(vpn, `# VPN`, `# VPN corporativa`, 1)
	f.expect(t, "adminTI", "PUT", "/api/w/ti/pages/redes/vpn", `{"title":"VPN corporativa","markdown":"`+adminChange+`","base_revision":1}`, http.StatusNoContent)
	if p := f.proposal(t, "adminTI", byEditor); !p.PageChanged || p.Conflict {
		t.Errorf("editor proposal after the admin's edit: %+v", p)
	}
	f.expect(t, "adminTI", "POST", "/api/w/ti/proposals/"+byEditor+"/approve", `{"comment":"Gracias"}`, http.StatusOK)
	var page struct{ Title, Markdown string }
	json.Unmarshal([]byte(f.expect(t, "viewerTI", "GET", "/api/w/ti/pages/redes/vpn", "", http.StatusOK)), &page)
	if page.Title != "VPN corporativa" || !strings.Contains(page.Markdown, "# VPN corporativa") || !strings.Contains(page.Markdown, "anexo 200") {
		t.Errorf("merged page: %+v", page)
	}
	// La revisión es del autor, con el admin como aprobador.
	if got := f.expect(t, "adminTI", "GET", "/api/w/ti/history/redes/vpn", "", http.StatusOK); !strings.Contains(got, `"approved_by":"adminTI","author":"editorTI"`) {
		t.Errorf("history: %s", got)
	}
	if p := f.proposal(t, "editorTI", byEditor); p.Status != "merged" || p.Reviewer != "adminTI" || p.Comment != "Gracias" || p.MergedRevision != 3 {
		t.Errorf("approved proposal: %+v", p)
	}
	// Aprobarla otra vez no hace nada.
	f.expect(t, "adminTI", "POST", "/api/w/ti/proposals/"+byEditor+"/approve", `{}`, http.StatusConflict)

	// El lector también cambió el título, a otro texto: conflicto, no se publica nada.
	if p := f.proposal(t, "viewerTI", byViewer); !p.Conflict {
		t.Errorf("viewer proposal should conflict: %+v", p)
	}
	f.expect(t, "adminTI", "POST", "/api/w/ti/proposals/"+byViewer+"/approve", `{}`, http.StatusConflict)
	// El lector la rehace sobre la revisión actual y ya se puede aprobar.
	json.Unmarshal([]byte(f.expect(t, "viewerTI", "GET", "/api/w/ti/pages/redes/vpn", "", http.StatusOK)), &page)
	rebased := strings.Replace(page.Markdown, "# VPN corporativa", "# VPN remota", 1)
	rebasedBody, _ := json.Marshal(map[string]any{"title": page.Title, "markdown": rebased, "base_revision": 3})
	f.expect(t, "viewerTI", "PATCH", "/api/w/ti/proposals/"+byViewer, string(rebasedBody), http.StatusNoContent)
	if p := f.proposal(t, "adminTI", byViewer); p.PageChanged || p.Conflict {
		t.Errorf("rebased proposal: %+v", p)
	}
	f.expect(t, "adminTI", "POST", "/api/w/ti/proposals/"+byViewer+"/approve", `{}`, http.StatusOK)

	// Rechazo: el comentario es obligatorio; el autor corrige y reenvía, o la retira.
	second := idOf(t, f.expect(t, "editorTI", "POST", "/api/w/ti/proposals",
		`{"slug":"redes/vpn","title":"VPN","markdown":"Otra cosa","base_revision":4}`, http.StatusCreated))
	f.expect(t, "adminTI", "POST", "/api/w/ti/proposals/"+second+"/reject", `{"comment":" "}`, http.StatusUnprocessableEntity)
	f.expect(t, "adminTI", "POST", "/api/w/ti/proposals/"+second+"/reject", `{"comment":"Falta el paso dos"}`, http.StatusNoContent)
	if p := f.proposal(t, "editorTI", second); p.Status != "rejected" || p.Comment != "Falta el paso dos" {
		t.Errorf("rejected: %+v", p)
	}
	f.expect(t, "adminTI", "POST", "/api/w/ti/proposals/"+second+"/approve", `{}`, http.StatusConflict)
	f.expect(t, "editorTI", "PATCH", "/api/w/ti/proposals/"+second, `{"title":"VPN","markdown":"Otra cosa, con el paso dos","base_revision":4}`, http.StatusNoContent)
	f.expect(t, "editorTI", "POST", "/api/w/ti/proposals/"+second+"/submit", "", http.StatusNoContent)
	if p := f.proposal(t, "adminTI", second); p.Status != "pending" || p.Comment != "" {
		t.Errorf("resubmitted: %+v", p)
	}
	f.expect(t, "adminTI", "POST", "/api/w/ti/proposals/"+second+"/withdraw", "", http.StatusNotFound) // retira solo el autor
	f.expect(t, "editorTI", "POST", "/api/w/ti/proposals/"+second+"/withdraw", "", http.StatusNoContent)
	f.expect(t, "adminTI", "POST", "/api/w/ti/proposals/"+second+"/approve", `{}`, http.StatusConflict)

	// Una página nueva: el admin la aprueba con cambios y queda a nombre del editor.
	newPage := idOf(t, f.expect(t, "editorTI", "POST", "/api/w/ti/proposals",
		`{"slug":"redes/wifi","title":"Wifi","markdown":"Clave en la recepcion.","base_revision":0}`, http.StatusCreated))
	f.expect(t, "adminTI", "POST", "/api/w/ti/proposals/"+newPage+"/approve",
		`{"edit":{"title":"Wifi de visitas","markdown":"Pide la clave en la recepción."}}`, http.StatusOK)
	json.Unmarshal([]byte(f.expect(t, "viewerTI", "GET", "/api/w/ti/pages/redes/wifi", "", http.StatusOK)), &page)
	if page.Title != "Wifi de visitas" || page.Markdown != "Pide la clave en la recepción." {
		t.Errorf("new page: %+v", page)
	}
	if got := f.expect(t, "adminTI", "GET", "/api/w/ti/history/redes/wifi", "", http.StatusOK); !strings.Contains(got, `"approved_by":"adminTI","author":"editorTI"`) {
		t.Errorf("new page history: %s", got)
	}
	// Una página nueva en una ruta ocupada: la de siempre (§19.35).
	f.expect(t, "editorTI", "POST", "/api/w/ti/proposals", `{"slug":"redes/wifi","title":"W","markdown":"w","base_revision":0}`, http.StatusConflict)

	// Resueltas para el admin; la auditoría registra cada paso.
	if got := f.expect(t, "adminTI", "GET", "/api/w/ti/proposals?view=resolved", "", http.StatusOK); strings.Count(got, `"id"`) != 3 {
		t.Errorf("resolved: %s", got)
	}
	audit := f.expect(t, "adminTI", "GET", "/api/w/ti/audit", "", http.StatusOK)
	for _, a := range []string{"proposal.submit", "proposal.approve", "proposal.reject", "proposal.withdraw"} {
		if !strings.Contains(audit, a) {
			t.Errorf("audit lacks %s", a)
		}
	}
}

// Un admin no aprueba su propia propuesta si hay otro admin en el área (§5.2).
func TestSelfApproval(t *testing.T) {
	f := newFixture(t)
	f.expect(t, "adminTI", "PUT", "/api/w/ti/pages/a", `{"title":"A","markdown":"a","base_revision":0}`, http.StatusNoContent)
	own := idOf(t, f.expect(t, "adminTI", "POST", "/api/w/ti/proposals", `{"slug":"a","title":"A","markdown":"b","base_revision":1}`, http.StatusCreated))
	f.expect(t, "super", "POST", "/api/admin/workspaces/"+f.wsID(t, "ti")+"/members", `{"email":"editorTI@empresa.cl","role":"admin"}`, http.StatusNoContent)
	f.expect(t, "adminTI", "POST", "/api/w/ti/proposals/"+own+"/approve", `{}`, http.StatusConflict)
	f.expect(t, "editorTI", "POST", "/api/w/ti/proposals/"+own+"/approve", `{}`, http.StatusOK)
}

// wsID busca el id de un área en el panel del superadmin.
func (f fixture) wsID(t *testing.T, slug string) string {
	t.Helper()
	var areas []struct{ ID, Slug string }
	json.Unmarshal([]byte(f.expect(t, "super", "GET", "/api/admin/workspaces", "", http.StatusOK)), &areas)
	for _, a := range areas {
		if a.Slug == slug {
			return a.ID
		}
	}
	t.Fatalf("no area %s", slug)
	return ""
}
