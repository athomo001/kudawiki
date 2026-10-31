package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/athomo001/kudawiki/internal/repo"
)

// El borrador es de quien lo escribe (§4.11): nadie más lo ve, sigue a la página si se mueve,
// se va al publicar o al proponer, y deja de existir para quien ya no ve la página.
func TestDrafts(t *testing.T) {
	f := newFixture(t)
	if w := f.do("adminTI", "PUT", "/api/w/ti/pages/redes/vpn", `{"title":"VPN","markdown":"v1","base_revision":0}`); w.Code != http.StatusNoContent {
		t.Fatalf("seed: %d %s", w.Code, w.Body)
	}
	draft := func(who, path string) (int, map[string]any) {
		t.Helper()
		w := f.do(who, "GET", "/api/w/ti/drafts/"+path, "")
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}

	if w := f.do("editorTI", "PUT", "/api/w/ti/drafts/redes/vpn", `{"title":"VPN","markdown":"a medio escribir","base_revision":1}`); w.Code != http.StatusNoContent {
		t.Fatalf("save draft: %d %s", w.Code, w.Body)
	}
	if code, d := draft("editorTI", "redes/vpn"); code != http.StatusOK || d["markdown"] != "a medio escribir" || d["base_revision"] != 1.0 || d["new_page"] != false {
		t.Errorf("own draft: %d %v", code, d)
	}
	if code, _ := draft("adminTI", "redes/vpn"); code != http.StatusNotFound {
		t.Errorf("another person's draft: %d, want 404", code)
	}
	// Guardar otra vez lo reemplaza: uno por persona y página.
	f.do("editorTI", "PUT", "/api/w/ti/drafts/redes/vpn", `{"title":"VPN","markdown":"segunda","base_revision":1}`)
	if n := count(t, f, `SELECT COUNT(*) FROM page_drafts`); n != 1 {
		t.Errorf("%d drafts, want 1", n)
	}

	// Sigue a la página movida.
	if w := f.do("adminTI", "POST", "/api/w/ti/move/redes/vpn", `{"to":"redes/acceso"}`); w.Code != http.StatusNoContent {
		t.Fatalf("move: %d %s", w.Code, w.Body)
	}
	if code, d := draft("editorTI", "redes/acceso"); code != http.StatusOK || d["markdown"] != "segunda" {
		t.Errorf("after move: %d %v", code, d)
	}
	// Con la página fuera de su nivel, el borrador no existe para él.
	setLevel(t, f, "redes/acceso", 3)
	if code, _ := draft("editorTI", "redes/acceso"); code != http.StatusNotFound {
		t.Errorf("page above the editor's level: %d, want 404", code)
	}
	setLevel(t, f, "redes/acceso", 1)
	// Publicar lo borra.
	// Mover creó la revisión 2.
	if w := f.do("editorTI", "PUT", "/api/w/ti/pages/redes/acceso", `{"title":"VPN","markdown":"segunda","base_revision":2}`); w.Code != http.StatusNoContent {
		t.Fatalf("publish: %d %s", w.Code, w.Body)
	}
	if code, _ := draft("editorTI", "redes/acceso"); code != http.StatusNotFound {
		t.Errorf("draft left after publishing: %d", code)
	}

	// Página nueva: se ofrece en la lista y se va al proponerla.
	if w := f.do("editorTI", "PUT", "/api/w/ti/drafts/redes/nueva", `{"title":"","markdown":"idea","base_revision":0}`); w.Code != http.StatusNoContent {
		t.Fatalf("new page draft: %d %s", w.Code, w.Body)
	}
	var list []map[string]any
	json.Unmarshal(f.do("editorTI", "GET", "/api/w/ti/drafts", "").Body.Bytes(), &list)
	if len(list) != 1 || list[0]["slug"] != "redes/nueva" || list[0]["markdown"] != nil {
		t.Errorf("new page drafts: %v", list)
	}
	if body := f.do("adminTI", "GET", "/api/w/ti/drafts", "").Body.String(); body != "[]\n" {
		t.Errorf("another person's list: %s", body)
	}
	if w := f.do("editorTI", "POST", "/api/w/ti/proposals", `{"slug":"redes/nueva","title":"Nueva","markdown":"idea","base_revision":0}`); w.Code != http.StatusCreated {
		t.Fatalf("propose: %d %s", w.Code, w.Body)
	}
	if code, _ := draft("editorTI", "redes/nueva"); code != http.StatusNotFound {
		t.Errorf("draft left after proposing: %d", code)
	}

	// Descartar.
	f.do("editorTI", "PUT", "/api/w/ti/drafts/otra", `{"title":"x","markdown":"y","base_revision":0}`)
	if w := f.do("editorTI", "DELETE", "/api/w/ti/drafts/otra", ""); w.Code != http.StatusNoContent {
		t.Errorf("discard: %d", w.Code)
	}
	if code, _ := draft("editorTI", "otra"); code != http.StatusNotFound {
		t.Errorf("draft left after discarding: %d", code)
	}

	// Los mismos topes de una página; sin título vale (todavía se escribe).
	for _, body := range []string{
		`{"title":"` + strings.Repeat("x", 201) + `","markdown":"y","base_revision":0}`,
		`{"title":"x","markdown":"` + strings.Repeat("y", 512<<10+1) + `","base_revision":0}`,
		`{"title":"x","markdown":"y","base_revision":-1}`,
	} {
		if w := f.do("editorTI", "PUT", "/api/w/ti/drafts/mala", body); w.Code != http.StatusUnprocessableEntity {
			t.Errorf("invalid draft: %d", w.Code)
		}
	}
	if w := f.do("editorTI", "PUT", "/api/w/ti/drafts/Mal%20Slug", `{"title":"x","markdown":"y","base_revision":0}`); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("invalid slug: %d", w.Code)
	}
}

func count(t *testing.T, f fixture, q string) (n int) {
	t.Helper()
	if err := f.db.Read.QueryRow(q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// presenceStream abre el aviso de edición de una página como lo hace el navegador.
func presenceStream(t *testing.T, f fixture, srv *httptest.Server, who, path string) (*http.Response, *bufio.Reader) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/w/ti/presence/"+path, nil)
	req.AddCookie(f.cookies[who])
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res, bufio.NewReader(res.Body)
}

// editors lee el próximo evento y devuelve los nombres; io.EOF si el servidor cortó.
func editors(br *bufio.Reader) ([]string, error) {
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			var ev struct{ Editors []struct{ Name string } }
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				return nil, err
			}
			var names []string
			for _, e := range ev.Editors {
				names = append(names, e.Name)
			}
			return names, nil
		}
	}
}

// Quien edita una página ve a los demás que la tienen abierta, y los ve irse. Al que ya no
// puede escribirla se le corta en el próximo latido. Para el resto, el 404 uniforme (§3.3).
func TestPresence(t *testing.T) {
	f := newFixture(t)
	s := seed(t, f)
	// Antes de levantar el servidor, y se restaura en una limpieza registrada antes que Close: las
	// limpiezas corren al revés, así que vuelve a su valor cuando Close ya esperó a los flujos que
	// lo leen. Con defer se restauraba con un flujo todavía abierto (carrera en CI con -race).
	old := presenceBeat
	presenceBeat = 50 * time.Millisecond
	t.Cleanup(func() { presenceBeat = old })
	srv := httptest.NewServer(f.h)
	t.Cleanup(srv.Close) // corre antes que la de arriba, con los flujos ya cerrados

	uniform := f.do("adminTI", "GET", "/api/w/no-existe/tree", "").Body.String()
	for _, c := range []struct{ who, path string }{
		{"viewerTI", s.slug[1]},     // el área no deja proponer a los lectores
		{"outsider", s.slug[1]},     // no es miembro
		{"adminFin", s.slug[1]},     // de otra área
		{"editorTI", s.slug[3]},     // la página está sobre su nivel
		{"editorTI", s.slug[0]},     // no existe
		{"editorTI", "redes/nueva"}, // una página nueva no tiene aviso
	} {
		res, br := presenceStream(t, f, srv, c.who, c.path)
		body, _ := io.ReadAll(br)
		if res.StatusCode != http.StatusNotFound || string(body) != uniform {
			t.Errorf("%s %s: %d %s, want the uniform 404", c.who, c.path, res.StatusCode, body)
		}
	}

	res, admin := presenceStream(t, f, srv, "adminTI", s.slug[1])
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("presence: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	if got, _ := editors(admin); !slices.Equal(got, []string{"adminTI"}) {
		t.Errorf("alone: %v", got)
	}
	_, editor := presenceStream(t, f, srv, "editorTI", s.slug[1])
	if got, _ := editors(editor); !slices.Equal(got, []string{"adminTI", "editorTI"}) {
		t.Errorf("editor sees %v", got)
	}
	if got, _ := editors(admin); !slices.Equal(got, []string{"adminTI", "editorTI"}) {
		t.Errorf("admin sees %v", got)
	}

	// Sale del área: su conexión se corta y el admin deja de verlo.
	if err := repo.New(f.db).RemoveMember(context.Background(), repo.Scope{WsID: wsID(t, f, "ti")}, f.users["editorTI"]); err != nil {
		t.Fatal(err)
	}
	if _, err := editors(editor); err != io.EOF {
		t.Errorf("removed member still connected: %v", err)
	}
	if got, _ := editors(admin); !slices.Equal(got, []string{"adminTI"}) {
		t.Errorf("after the editor left, admin sees %v", got)
	}
}

func wsID(t *testing.T, f fixture, slug string) (id string) {
	t.Helper()
	if err := f.db.Read.QueryRow(`SELECT id FROM workspaces WHERE slug = ?`, slug).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
