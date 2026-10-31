package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

type readPerson struct {
	Name, Status      string
	Opens             int
	ConfirmedRevision int `json:"confirmed_revision"`
}

func (f fixture) readPeople(t *testing.T, who, id string) map[string]readPerson {
	t.Helper()
	var d struct{ People []readPerson }
	json.Unmarshal([]byte(f.expect(t, who, "GET", "/api/w/ti/read-requests/"+id, "", http.StatusOK)), &d)
	out := map[string]readPerson{}
	for _, p := range d.People {
		out[p.Name] = p
	}
	return out
}

// TestReadRequests es el acuse de lectura (§4.14): quién pide a quién, las aperturas, confirmar
// con la revisión exacta, cerrar y los avisos.
func TestReadRequests(t *testing.T) {
	f := newFixture(t)
	uniform := f.do("adminTI", "GET", "/api/w/no-existe/tree", "")
	f.expect(t, "adminTI", "PUT", "/api/w/ti/pages/politica", `{"title":"Política","markdown":"Uno","base_revision":0}`, http.StatusNoContent)
	f.expect(t, "adminTI", "PUT", "/api/w/ti/pages/interna", `{"title":"Interna","markdown":"Uno","base_revision":0}`, http.StatusNoContent)
	f.expect(t, "adminTI", "PATCH", "/api/w/ti/meta/interna", `{"level":2}`, http.StatusNoContent)
	future := time.Now().Add(72 * time.Hour).UTC().Format(time.RFC3339)

	// Siempre de arriba hacia abajo (§19.25).
	if w := f.do("viewerTI", "POST", "/api/w/ti/read-requests", `{"page":"politica","audience":{"all":true}}`); w.Code != uniform.Code || w.Body.String() != uniform.Body.String() {
		t.Errorf("a viewer asking: %d %s", w.Code, w.Body)
	}
	for _, c := range []struct{ who, body string }{
		{"editorTI", `{"page":"interna","audience":{"all":true}}`},                                 // N2: ningún lector la lee
		{"editorTI", `{"page":"politica","audience":{"roles":["editor"]}}`},                        // a su mismo rol
		{"editorTI", `{"page":"politica","audience":{"users":["` + f.users["adminTI"] + `"]}}`},    // hacia arriba
		{"adminTI", `{"page":"interna","audience":{"users":["` + f.users["viewerTI"] + `"]}}`},     // sin el nivel
		{"adminTI", `{"page":"politica","audience":{"users":["` + f.users["adminFin"] + `"]}}`},    // de otra área
		{"adminTI", `{"page":"politica","audience":{"all":true,"roles":["viewer"]}}`},              // dos audiencias
		{"adminTI", `{"page":"politica","audience":{"roles":["jefe"]}}`},                           // rol que no existe
		{"adminTI", `{"page":"politica","audience":{"all":true},"due_at":"2001-01-01T00:00:00Z"}`}, // plazo pasado
	} {
		f.expect(t, c.who, "POST", "/api/w/ti/read-requests", c.body, http.StatusUnprocessableEntity)
	}
	// A quiénes puede elegir cada uno, una a una.
	if body := f.expect(t, "editorTI", "GET", "/api/w/ti/read-requests/candidates?page=politica", "", http.StatusOK); !strings.Contains(body, "viewerTI") || strings.Contains(body, "adminTI") {
		t.Errorf("editor candidates: %s", body)
	}
	if body := f.expect(t, "adminTI", "GET", "/api/w/ti/read-requests/candidates?page=interna", "", http.StatusOK); !strings.Contains(body, "editorTI") || strings.Contains(body, "viewerTI") {
		t.Errorf("admin candidates for an N2 page: %s", body)
	}
	byEditor := idOf(t, f.expect(t, "editorTI", "POST", "/api/w/ti/read-requests",
		`{"page":"politica","statement":"Confirmo que leí la política","audience":{"all":true},"due_at":"`+future+`"}`, http.StatusCreated))
	byAdmin := idOf(t, f.expect(t, "adminTI", "POST", "/api/w/ti/read-requests", `{"page":"interna","audience":{"roles":["editor","viewer"]}}`, http.StatusCreated))

	// El editor ve las suyas; el admin, todas. Quien no la pidió (ni es admin) no ve el panel.
	if body := f.expect(t, "editorTI", "GET", "/api/w/ti/read-requests", "", http.StatusOK); strings.Contains(body, byAdmin) || !strings.Contains(body, byEditor) {
		t.Errorf("editor list: %s", body)
	}
	if body := f.expect(t, "adminTI", "GET", "/api/w/ti/read-requests", "", http.StatusOK); !strings.Contains(body, byAdmin) || !strings.Contains(body, byEditor) {
		t.Errorf("admin list: %s", body)
	}
	for _, who := range []string{"viewerTI", "adminFin", "outsider"} {
		if w := f.do(who, "GET", "/api/w/ti/read-requests/"+byEditor, ""); w.Code != uniform.Code {
			t.Errorf("%s sees the panel: %d", who, w.Code)
		}
	}
	f.expect(t, "editorTI", "GET", "/api/w/ti/read-requests/"+byAdmin, "", http.StatusNotFound)

	// La editora pidió a los lectores: solo viewerTI. El admin, a editores y lectores de una N2:
	// solo editorTI (el lector no la lee).
	people := f.readPeople(t, "editorTI", byEditor)
	if len(people) != 1 || people["viewerTI"].Status != "unopened" {
		t.Fatalf("recipients: %+v", people)
	}
	if p := f.readPeople(t, "adminTI", byAdmin); len(p) != 1 || p["editorTI"].Status != "unopened" {
		t.Fatalf("admin recipients: %+v", p)
	}

	// Abrir la página queda anotado, y la página trae el aviso para confirmar.
	var page struct {
		Revision int
		Reading  []struct {
			ID, Statement string
			ConfirmedAt   *time.Time `json:"confirmed_at"`
		}
	}
	for range 2 {
		json.Unmarshal([]byte(f.expect(t, "viewerTI", "GET", "/api/w/ti/pages/politica", "", http.StatusOK)), &page)
	}
	if len(page.Reading) != 1 || page.Reading[0].ID != byEditor || page.Reading[0].Statement != "Confirmo que leí la política" {
		t.Fatalf("reading on the page: %+v", page.Reading)
	}
	if p := f.readPeople(t, "adminTI", byEditor)["viewerTI"]; p.Status != "opened" || p.Opens != 2 {
		t.Errorf("after opening: %+v", p)
	}
	if body := f.expect(t, "editorTI", "GET", "/api/w/ti/pages/politica", "", http.StatusOK); !strings.Contains(body, `"reading":[]`) {
		t.Errorf("the requester is not a recipient: %s", body)
	}
	if body := f.expect(t, "viewerTI", "GET", "/api/me/read-requests", "", http.StatusOK); !strings.Contains(body, byEditor) {
		t.Errorf("pending reads: %s", body)
	}

	// Confirmar vale para la revisión que se tenía en pantalla, si sigue siendo la actual.
	f.expect(t, "adminTI", "PUT", "/api/w/ti/pages/politica", `{"title":"Política","markdown":"Dos","base_revision":1}`, http.StatusNoContent)
	f.expect(t, "viewerTI", "POST", "/api/w/ti/read-requests/"+byEditor+"/ack", `{"revision":1}`, http.StatusConflict)
	f.expect(t, "editorTI", "POST", "/api/w/ti/read-requests/"+byEditor+"/ack", `{"revision":2}`, http.StatusNotFound)
	f.expect(t, "viewerTI", "POST", "/api/w/ti/read-requests/"+byAdmin+"/ack", `{"revision":1}`, http.StatusNotFound)
	f.expect(t, "viewerTI", "POST", "/api/w/ti/read-requests/"+byEditor+"/ack", `{"revision":2}`, http.StatusNoContent)
	f.expect(t, "viewerTI", "POST", "/api/w/ti/read-requests/"+byEditor+"/ack", `{"revision":2}`, http.StatusNoContent)
	// Volver a abrirla después de confirmar no suma aperturas.
	f.expect(t, "viewerTI", "GET", "/api/w/ti/pages/politica", "", http.StatusOK)
	if p := f.readPeople(t, "editorTI", byEditor)["viewerTI"]; p.Status != "confirmed" || p.ConfirmedRevision != 2 || p.Opens != 2 {
		t.Errorf("after confirming: %+v", p)
	}
	if body := f.expect(t, "viewerTI", "GET", "/api/me/read-requests", "", http.StatusOK); strings.Contains(body, byEditor) {
		t.Errorf("a confirmed read is still pending: %s", body)
	}
	csv := f.expect(t, "editorTI", "GET", "/api/w/ti/read-requests/"+byEditor+"?format=csv", "", http.StatusOK)
	if !strings.HasPrefix(csv, "name,email,role,status") || !strings.Contains(csv, "viewerTI@empresa.cl,viewer,confirmed") {
		t.Errorf("csv: %s", csv)
	}

	// Avisos: la solicitud nueva y, cerca del plazo, el recordatorio a quien no confirmó.
	f.db.Write.Exec(`UPDATE read_requests SET due_at = datetime('now', '+1 day'), notified_at = datetime('now', '-3 days') WHERE id = ?`, byAdmin)
	if err := f.notify.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if items, _ := f.inbox(t, "editorTI"); len(items) != 1 || items[0].Kind != "read_reminder" {
		t.Errorf("editor inbox: %+v", items)
	}
	if items, _ := f.inbox(t, "viewerTI"); len(items) != 0 {
		t.Errorf("a confirmed recipient got a notice: %+v", items)
	}

	// Cerrar: solo quien la pidió o un admin; después ya no se pide ni se confirma.
	f.expect(t, "viewerTI", "POST", "/api/w/ti/read-requests/"+byAdmin+"/close", "", http.StatusNotFound)
	f.expect(t, "editorTI", "POST", "/api/w/ti/read-requests/"+byAdmin+"/close", "", http.StatusNotFound)
	f.expect(t, "adminTI", "POST", "/api/w/ti/read-requests/"+byAdmin+"/close", "", http.StatusNoContent)
	f.expect(t, "editorTI", "POST", "/api/w/ti/read-requests/"+byAdmin+"/ack", `{"revision":1}`, http.StatusNotFound)
	if body := f.expect(t, "editorTI", "GET", "/api/w/ti/pages/interna", "", http.StatusOK); !strings.Contains(body, `"reading":[]`) {
		t.Errorf("a closed request still shows: %s", body)
	}
	if e := find(auditOf(t, f, "adminTI", "/api/w/ti/audit"), "read.ack", "viewerTI"); e == nil {
		t.Error("confirming left no audit trail")
	}
}
