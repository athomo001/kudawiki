package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

// Las tablas de Personas y Áreas de la administración (SPEC §15, diseño "Kudawiki —
// Administración"): las áreas de cada persona con su rol, y por área sus miembros, admins,
// páginas vivas y última edición.
func TestAdminPanelTables(t *testing.T) {
	f := newFixture(t)
	for _, slug := range []string{"uno", "dos"} {
		f.expect(t, "editorTI", "PUT", "/api/w/ti/pages/"+slug, `{"title":"`+slug+`","markdown":"x","base_revision":0}`, http.StatusNoContent)
	}
	f.expect(t, "editorTI", "DELETE", "/api/w/ti/pages/dos", "", http.StatusNoContent)

	var users []struct {
		Email    string `json:"email"`
		Passkeys int    `json:"passkeys"`
		Areas    []struct {
			Name string `json:"name"`
			Role string `json:"role"`
		} `json:"areas"`
	}
	if err := json.Unmarshal([]byte(f.expect(t, "super", "GET", "/api/admin/users", "", http.StatusOK)), &users); err != nil {
		t.Fatal(err)
	}
	byEmail := map[string]int{}
	for i, u := range users {
		byEmail[u.Email] = i
		if u.Areas == nil {
			t.Errorf("%s: areas is null, want a list", u.Email)
		}
	}
	if a := users[byEmail["adminTI@empresa.cl"]].Areas; len(a) != 1 || a[0].Name != "TI" || a[0].Role != "admin" {
		t.Errorf("adminTI areas = %+v", a)
	}
	if a := users[byEmail["outsider@empresa.cl"]].Areas; len(a) != 0 {
		t.Errorf("outsider areas = %+v", a)
	}

	var areas []struct {
		Name     string   `json:"name"`
		Members  int      `json:"members"`
		Pages    int      `json:"pages"`
		Admins   []string `json:"admins"`
		LastEdit string   `json:"last_edit"`
	}
	if err := json.Unmarshal([]byte(f.expect(t, "super", "GET", "/api/admin/workspaces", "", http.StatusOK)), &areas); err != nil {
		t.Fatal(err)
	}
	if len(areas) != 2 {
		t.Fatalf("areas = %+v", areas)
	}
	fin, ti := areas[0], areas[1] // por nombre
	// La página en la papelera no cuenta.
	if ti.Members != 3 || ti.Pages != 1 || len(ti.Admins) != 1 || ti.Admins[0] != "adminTI" || ti.LastEdit == "" {
		t.Errorf("TI = %+v", ti)
	}
	if fin.Members != 1 || fin.Pages != 0 || len(fin.Admins) != 1 || fin.LastEdit != "" {
		t.Errorf("Finanzas = %+v", fin)
	}
}
