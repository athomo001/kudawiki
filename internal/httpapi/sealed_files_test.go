package httpapi

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/athomo001/kudawiki/internal/backup"
	"github.com/klauspost/compress/zstd"
)

// newSealed sella un documento como editorTI y devuelve su ruta en la API.
func (f fixture) newSealed(t *testing.T, slug, title, markdown string) string {
	t.Helper()
	var out struct{ ID string }
	json.Unmarshal([]byte(f.expect(t, "editorTI", "POST", "/api/w/ti/sealed",
		`{"slug":"`+slug+`","title":"`+title+`","markdown":"`+markdown+`","password":"una clave bien larga"}`, http.StatusCreated)), &out)
	return "/api/sealed/" + out.ID
}

type sealedFile struct{ ID, Name, Mime, URL string }

func (f fixture) attach(t *testing.T, who, doc, name string, data []byte) sealedFile {
	t.Helper()
	w := f.uploadTo(who, doc+"/files", name, data)
	if w.Code != http.StatusCreated {
		t.Fatalf("attach %s: %d %s", name, w.Code, w.Body)
	}
	var out sealedFile
	json.Unmarshal(w.Body.Bytes(), &out)
	return out
}

// encFiles son los archivos de sellados que hay en el disco.
func (f fixture) encFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	filepath.WalkDir(f.storage, func(p string, d fs.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(p, ".enc") {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// TestSealedFiles: los archivos de un sellado (§6.1, F7.6) se suben y se ven solo con el documento
// abierto, sin cache, y en el disco y en la base van cifrados, también su nombre.
func TestSealedFiles(t *testing.T) {
	f := newFixture(t)
	doc := f.newSealed(t, "legal/acta", "Acta", "texto")
	txt := f.attach(t, "editorTI", doc, "kwnombre-secreto.txt", []byte("kwadjunto contenido secreto\n"))
	if txt.URL != doc+"/files/"+txt.ID || txt.Name != "kwnombre-secreto.txt" || txt.Mime != "text/plain; charset=utf-8" {
		t.Errorf("upload: %+v", txt)
	}
	img := f.attach(t, "editorTI", doc, "plano.png", pngBytes(t))

	// Con el documento abierto: el contenido, sin cache y sin ejecutar nada.
	w := f.do("editorTI", "GET", txt.URL, "")
	if w.Code != http.StatusOK || w.Body.String() != "kwadjunto contenido secreto\n" {
		t.Fatalf("get: %d %q", w.Code, w.Body)
	}
	h := w.Header()
	if h.Get("Cache-Control") != "no-store" || !strings.HasPrefix(h.Get("Content-Disposition"), "attachment") ||
		!strings.Contains(h.Get("Content-Security-Policy"), "sandbox") || h.Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Errorf("headers: %v", h)
	}
	if w := f.do("editorTI", "GET", img.URL, ""); w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "inline") ||
		!bytes.Equal(w.Body.Bytes(), pngBytes(t)) {
		t.Errorf("image: %d %v", w.Code, w.Header())
	}

	// Cerrado en otra sesión, 423; quien no lo ve, el 404 de siempre.
	f.expect(t, "adminTI", "GET", txt.URL, "", http.StatusLocked)
	f.expect(t, "viewerTI", "GET", txt.URL, "", http.StatusNotFound)
	f.expect(t, "outsider", "GET", txt.URL, "", http.StatusNotFound)
	if w := f.uploadTo("viewerTI", doc+"/files", "x.txt", []byte("x")); w.Code != http.StatusNotFound {
		t.Errorf("viewer upload: %d", w.Code)
	}
	// El admin que lo abre con la llave maestra lo lee, archivos incluidos, pero no adjunta.
	f.reauth(t, "adminTI")
	f.expect(t, "adminTI", "POST", doc+"/unlock", `{"as_admin":true}`, http.StatusNoContent)
	f.expect(t, "adminTI", "GET", txt.URL, "", http.StatusOK)
	if w := f.uploadTo("adminTI", doc+"/files", "x.txt", []byte("x")); w.Code != http.StatusConflict {
		t.Errorf("read-only upload: %d", w.Code)
	}
	// El archivo de un sellado no se ve por la ruta de otro, aunque esté abierto.
	other := f.newSealed(t, "legal/otro", "Otro", "otro")
	f.expect(t, "editorTI", "GET", other+"/files/"+txt.ID, "", http.StatusNotFound)
	// Las reglas de tipo de la media del área: nada de HTML ni SVG.
	if w := f.uploadTo("editorTI", doc+"/files", "x.html", []byte("<html><script>alert(1)</script></html>")); w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("html: %d", w.Code)
	}

	// En el disco, cifrados; en la base, ni el nombre.
	encs := f.encFiles(t)
	if len(encs) != 2 {
		t.Fatalf("enc files: %v", encs)
	}
	for _, p := range encs {
		if b, _ := os.ReadFile(p); bytes.Contains(b, []byte("kwadjunto")) {
			t.Errorf("plaintext in %s", p)
		}
	}
	var file string
	f.db.Read.QueryRow(`SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&file)
	for _, p := range []string{file, file + "-wal"} {
		if b, err := os.ReadFile(p); err == nil && bytes.Contains(b, []byte("kwnombre")) {
			t.Errorf("file name in clear in %s", p)
		}
	}
	// Ni el navegador de archivos ni doctor los confunden con media del área.
	if got := f.expect(t, "editorTI", "GET", "/api/w/ti/media", "", http.StatusOK); strings.TrimSpace(got) != "[]" {
		t.Errorf("area media lists sealed files: %s", got)
	}
	if got := f.expect(t, "super", "GET", "/api/admin/health", "", http.StatusOK); strings.Contains(got, "stray_file") || strings.Contains(got, "missing_file") {
		t.Errorf("doctor: %s", got)
	}

	// Un sellado sin su DEK envuelta con la llave maestra: doctor lo marca como error (§13.7).
	f.db.Write.Exec(`DELETE FROM sealed_keys WHERE holder_type = 'master' AND page_id = ?`, strings.TrimPrefix(other, "/api/sealed/"))
	if got := f.expect(t, "super", "GET", "/api/admin/health", "", http.StatusOK); !strings.Contains(got, `"sealed.no_master_key"`) || !strings.Contains(got, "legal/otro") {
		t.Errorf("doctor no master key: %s", got)
	}

	// Borrar el documento se lleva sus archivos del disco.
	f.expect(t, "editorTI", "DELETE", doc, `{"confirm":"BORRAR"}`, http.StatusNoContent)
	if encs := f.encFiles(t); len(encs) != 0 {
		t.Errorf("enc files after purge: %v", encs)
	}
	var rows int
	f.db.Read.QueryRow(`SELECT COUNT(*) FROM sealed_media`).Scan(&rows)
	if rows != 0 {
		t.Errorf("%d sealed_media rows after purge", rows)
	}
	// Un .enc que quedó de un borrado cortado: doctor lo muestra como resto de un sellado (§13.7).
	os.WriteFile(filepath.Join(filepath.Dir(encs[0]), "01J00000000000000000000000.enc"), []byte("x"), 0o600)
	if got := f.expect(t, "super", "GET", "/api/admin/health", "", http.StatusOK); !strings.Contains(got, `"sealed.stray_file"`) || strings.Contains(got, "media.stray_file") {
		t.Errorf("doctor stray enc: %s", got)
	}
}

// TestSealedDeclassifyFiles: al desclasificar, los archivos que usa la versión vigente pasan a ser
// media del área y el Markdown apunta a ella; los demás se borran con el sellado.
func TestSealedDeclassifyFiles(t *testing.T) {
	f := newFixture(t)
	doc := f.newSealed(t, "legal/acta", "Acta", "texto")
	img := f.attach(t, "editorTI", doc, "plano.png", pngBytes(t))
	f.attach(t, "editorTI", doc, "viejo.txt", []byte("kwviejo"))
	f.expect(t, "editorTI", "PUT", doc, `{"title":"Acta","markdown":"Ver ![plano](`+img.URL+`) y otra vez ![plano](`+img.URL+`)","base_revision":1}`, http.StatusOK)
	f.reauth(t, "super")
	f.expect(t, "super", "POST", doc+"/declassify", "", http.StatusOK)

	var page struct{ Markdown string }
	json.Unmarshal([]byte(f.expect(t, "editorTI", "GET", "/api/w/ti/pages/legal/acta", "", http.StatusOK)), &page)
	if strings.Contains(page.Markdown, "/api/sealed/") || strings.Count(page.Markdown, "/api/w/ti/media/") != 2 {
		t.Fatalf("markdown: %s", page.Markdown)
	}
	url := page.Markdown[strings.Index(page.Markdown, "/api/w/ti/media/"):]
	url = url[:strings.Index(url, ")")]
	if w := f.do("editorTI", "GET", url, ""); w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), pngBytes(t)) {
		t.Errorf("media: %d", w.Code)
	}
	// Una sola copia, y de esta página: el navegador de archivos la muestra con ella.
	if got := f.expect(t, "editorTI", "GET", "/api/w/ti/media", "", http.StatusOK); strings.Count(got, `"id"`) != 1 || !strings.Contains(got, `"slug":"legal/acta"`) {
		t.Errorf("area media: %s", got)
	}
	if encs := f.encFiles(t); len(encs) != 0 {
		t.Errorf("enc files left: %v", encs)
	}
}

// TestSealedNothingInClear es el criterio de salida de F7: con la base, su WAL, storage/ y un
// respaldo, sin la MASTER_KEY, no aparece texto de ningún sellado: ni el título, ni el contenido
// de ninguna revisión, ni sus archivos ni el nombre de ellos. Una página normal sí aparece: así se
// sabe que la búsqueda encontraría el texto si estuviera.
func TestSealedNothingInClear(t *testing.T) {
	f := newFixture(t)
	f.expect(t, "editorTI", "PUT", "/api/w/ti/pages/control", `{"title":"kwcontrolvisible","markdown":"kwcontrolvisible","base_revision":0}`, http.StatusNoContent)
	f.upload("editorTI", "ti", "control.txt", []byte("kwcontrolarchivo"))
	doc := f.newSealed(t, "legal/acta", "kwtitulosecreto", "kwcuerpouno")
	f.attach(t, "editorTI", doc, "kwnombrearchivo.txt", []byte("kwcontenidoarchivo"))
	f.expect(t, "editorTI", "PUT", doc, `{"title":"kwtitulosecreto","markdown":"kwcuerpodos","base_revision":1}`, http.StatusOK)
	gone := f.newSealed(t, "legal/borrado", "kwtituloborrado", "kwcuerpoborrado")
	f.attach(t, "editorTI", gone, "kwnombreborrado.txt", []byte("kwarchivoborrado"))
	f.expect(t, "editorTI", "DELETE", gone, `{"confirm":"BORRAR"}`, http.StatusNoContent)

	dir := t.TempDir()
	path, err := backup.Create(context.Background(), f.db.Read, f.storage, dir, backup.Manual, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var file string
	f.db.Read.QueryRow(`SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&file)

	// Lo que hay en el disco, más cada archivo de adentro del respaldo, ya descomprimido.
	blobs := map[string][]byte{}
	for _, p := range []string{file, file + "-wal", path} {
		blobs[p], _ = os.ReadFile(p)
	}
	filepath.WalkDir(f.storage, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			blobs[p], _ = os.ReadFile(p)
		}
		return nil
	})
	in, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	zr, err := zstd.NewReader(in)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	entries := 0
	for {
		hd, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		blobs["backup:"+hd.Name], _ = io.ReadAll(tr)
		entries++
	}
	if entries < 3 {
		t.Fatalf("backup has %d entries", entries)
	}

	found := func(s string) []string {
		var where []string
		for name, b := range blobs {
			if bytes.Contains(b, []byte(s)) {
				where = append(where, name)
			}
		}
		return where
	}
	for _, s := range []string{"kwcontrolvisible", "kwcontrolarchivo"} {
		if len(found(s)) == 0 {
			t.Errorf("control %q not found: the search would not see plaintext", s)
		}
	}
	for _, s := range []string{"kwtitulosecreto", "kwcuerpouno", "kwcuerpodos", "kwnombrearchivo", "kwcontenidoarchivo",
		"kwtituloborrado", "kwcuerpoborrado", "kwnombreborrado", "kwarchivoborrado"} {
		if where := found(s); len(where) > 0 {
			t.Errorf("%q in clear in %v", s, where)
		}
	}
}
