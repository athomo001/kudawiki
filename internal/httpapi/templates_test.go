package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

type templateOut struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Title    string   `json:"title"`
	Markdown string   `json:"markdown"`
	Level    int      `json:"level"`
	Tags     []string `json:"tags"`
	Review   int      `json:"review_interval_days"`
}

func (f fixture) createTemplate(t *testing.T, who, path, body string) string {
	t.Helper()
	w := f.do(who, "POST", path, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("%s create template: %d %s", who, w.Code, w.Body)
	}
	var out struct{ ID string }
	json.Unmarshal(w.Body.Bytes(), &out)
	return out.ID
}

func (f fixture) templateList(t *testing.T, who, path string) []templateOut {
	t.Helper()
	w := f.do(who, "GET", path, "")
	if w.Code != http.StatusOK {
		t.Fatalf("%s templates: %d %s", who, w.Code, w.Body)
	}
	var out []templateOut
	json.Unmarshal(w.Body.Bytes(), &out)
	return out
}

// §4.9: el admin del área crea y edita plantillas; quien crea páginas las elige y las usa con las
// variables llenas. Cada una tiene un nivel: quien no lo alcanza no la ve.
func TestTemplates(t *testing.T) {
	f := newFixture(t)
	acta := f.createTemplate(t, "adminTI", "/api/w/ti/templates",
		`{"name":"Acta de reunión","description":"Para las reuniones semanales","title":"Acta {{fecha}}","markdown":"# {{titulo}}\nRedacta: {{autor}} el {{fecha}}.","level":1,"tags":["acta"," Acta ","reunión"],"review_interval_days":90}`)
	f.createTemplate(t, "adminTI", "/api/w/ti/templates", `{"name":"Post-mortem","markdown":"x","level":3}`)

	// El editor ve la de N1 y no la de N3; la lista no trae el contenido.
	list := f.templateList(t, "editorTI", "/api/w/ti/templates")
	if len(list) != 1 || list[0].Name != "Acta de reunión" || list[0].Markdown != "" || strings.Join(list[0].Tags, ",") != "acta,reunión" {
		t.Errorf("editor templates = %+v", list)
	}
	if n := len(f.templateList(t, "adminTI", "/api/w/ti/templates")); n != 2 {
		t.Errorf("admin sees %d templates, want 2", n)
	}

	// Usarla: sin título toma el de la plantilla; con título, el que escribió quien la usa.
	var used templateOut
	w := f.do("editorTI", "GET", "/api/w/ti/templates/"+acta+"/use?today=2026-10-30", "")
	json.Unmarshal(w.Body.Bytes(), &used)
	if w.Code != http.StatusOK || used.Title != "Acta 2026-10-30" || used.Markdown != "# Acta 2026-10-30\nRedacta: editorTI el 2026-10-30." || used.Level != 1 || used.Review != 90 {
		t.Errorf("use without title: %d %+v", w.Code, used)
	}
	w = f.do("editorTI", "GET", "/api/w/ti/templates/"+acta+"/use?today=2026-10-30&title=Comit%C3%A9%20TI", "")
	json.Unmarshal(w.Body.Bytes(), &used)
	if used.Title != "Comité TI" || !strings.HasPrefix(used.Markdown, "# Comité TI\n") {
		t.Errorf("use with title: %+v", used)
	}

	for _, c := range []struct{ name, method, path, body string }{
		{"no name", "POST", "/api/w/ti/templates", `{"name":"  ","level":1}`},
		{"level out of range", "POST", "/api/w/ti/templates", `{"name":"x","level":4}`},
		{"bad tag", "POST", "/api/w/ti/templates", `{"name":"x","level":1,"tags":["<b>"]}`},
		{"review interval out of range", "POST", "/api/w/ti/templates", `{"name":"x","level":1,"review_interval_days":3651}`},
		{"open secret", "PUT", "/api/w/ti/templates/" + acta, `{"name":"x","level":1,"markdown":"[[secret:sin cerrar"}`},
	} {
		if w := f.do("adminTI", c.method, c.path, c.body); w.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s, want 422", c.name, w.Code, w.Body)
		}
	}

	// Editar y borrar quedan en la auditoría del área, sin el contenido.
	if w := f.do("adminTI", "PUT", "/api/w/ti/templates/"+acta, `{"name":"Acta","markdown":"Nueva","level":2}`); w.Code != http.StatusNoContent {
		t.Fatalf("update: %d %s", w.Code, w.Body)
	}
	var got templateOut
	json.Unmarshal(f.do("adminTI", "GET", "/api/w/ti/templates/"+acta, "").Body.Bytes(), &got)
	if got.Name != "Acta" || got.Markdown != "Nueva" || got.Level != 2 || len(got.Tags) != 0 {
		t.Errorf("after update: %+v", got)
	}
	if w := f.do("adminTI", "DELETE", "/api/w/ti/templates/"+acta, ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", w.Code)
	}
	if w := f.do("editorTI", "GET", "/api/w/ti/templates/"+acta+"/use", ""); w.Code != http.StatusNotFound {
		t.Errorf("use a deleted template: %d", w.Code)
	}
	audit := f.do("adminTI", "GET", "/api/w/ti/audit", "").Body.String()
	for _, action := range []string{"template.create", "template.update", "template.delete"} {
		if !strings.Contains(audit, action) {
			t.Errorf("audit lacks %s", action)
		}
	}
	if strings.Contains(audit, "Redacta") {
		t.Error("the audit log carries template content")
	}
}

// §4.9: las plantillas globales son del superadmin. Se copian a cada área al crearla y desde ahí
// son del área: cambiar la global no toca la copia, y las áreas que ya existían no la reciben.
func TestGlobalTemplates(t *testing.T) {
	f := newFixture(t)
	if w := f.do("adminTI", "GET", "/api/admin/templates", ""); w.Code != http.StatusNotFound {
		t.Errorf("an area admin reaches the global templates: %d", w.Code)
	}
	id := f.createTemplate(t, "super", "/api/admin/templates", `{"name":"Ficha de sistema","markdown":"Dueño: {{autor}}","level":2}`)
	if list := f.templateList(t, "super", "/api/admin/templates"); len(list) != 1 || list[0].Name != "Ficha de sistema" {
		t.Errorf("global templates = %+v", list)
	}
	// Las globales no son de ningún área.
	if list := f.templateList(t, "adminTI", "/api/w/ti/templates"); len(list) != 0 {
		t.Errorf("an existing area received a global template: %+v", list)
	}

	if w := f.do("super", "POST", "/api/admin/workspaces", `{"slug":"ops","name":"Operaciones"}`); w.Code != http.StatusCreated {
		t.Fatalf("create area: %d %s", w.Code, w.Body)
	}
	if w := f.do("super", "PUT", "/api/admin/templates/"+id, `{"name":"Ficha cambiada","markdown":"y","level":1}`); w.Code != http.StatusNoContent {
		t.Fatalf("update global: %d %s", w.Code, w.Body)
	}
	if w := f.do("super", "POST", "/api/admin/workspaces/"+f.wsID(t, "ops")+"/members", `{"email":"adminTI@empresa.cl","role":"admin"}`); w.Code != http.StatusNoContent {
		t.Fatalf("add admin: %d %s", w.Code, w.Body)
	}
	copied := f.templateList(t, "adminTI", "/api/w/ops/templates")
	if len(copied) != 1 || copied[0].Name != "Ficha de sistema" || copied[0].Level != 2 || copied[0].ID == id {
		t.Errorf("copy in the new area = %+v", copied)
	}
	if w := f.do("super", "DELETE", "/api/admin/templates/"+id, ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete global: %d", w.Code)
	}
	if list := f.templateList(t, "adminTI", "/api/w/ops/templates"); len(list) != 1 {
		t.Errorf("deleting the global removed the area copy: %+v", list)
	}
	// La copia es del área: el panel global no la alcanza.
	if w := f.do("super", "GET", "/api/admin/templates/"+copied[0].ID, ""); w.Code != http.StatusNotFound {
		t.Errorf("the global panel reaches an area template: %d", w.Code)
	}
}
