package httpapi

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/athomo001/kudawiki/internal/backup"
	"github.com/athomo001/kudawiki/internal/backups"
)

type backupsOut struct {
	Settings backups.Settings
	Backups  []backups.Entry
	Running  string
	Last     *backups.Result
}

// backupsState espera a que no corra nada y devuelve el estado de la pantalla.
func (f fixture) backupsState(t *testing.T) backupsOut {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); ; {
		var out backupsOut
		w := f.do("super", "GET", "/api/admin/backups", "")
		if w.Code != http.StatusOK {
			t.Fatalf("backups: %d %s", w.Code, w.Body)
		}
		json.Unmarshal(w.Body.Bytes(), &out)
		if out.Running == "" {
			return out
		}
		if time.Now().After(deadline) {
			t.Fatalf("still running: %s", w.Body)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestBackupsAdmin(t *testing.T) {
	f := newFixture(t)
	for _, who := range []string{"adminTI", "editorTI", "viewerTI", "outsider", ""} {
		for _, req := range [][3]string{
			{"GET", "/api/admin/backups", ""}, {"PUT", "/api/admin/backups/settings", `{"time":"04:00"}`},
			{"POST", "/api/admin/backups", ""}, {"POST", "/api/admin/reset", `{"confirm":"BORRAR TODO"}`},
			{"DELETE", "/api/admin/backups/kudawiki-daily-20261103-030000.tar.zst", ""},
			{"GET", "/api/admin/backups/kudawiki-daily-20261103-030000.tar.zst", ""},
			{"GET", "/api/admin/backups/kudawiki-daily-20261103-030000.tar.zst/download", ""},
		} {
			want := http.StatusNotFound
			if who == "" {
				want = http.StatusUnauthorized
			}
			if w := f.do(who, req[0], req[1], req[2]); w.Code != want {
				t.Errorf("%s %s %s: %d, want %d", who, req[0], req[1], w.Code, want)
			}
		}
	}

	w := f.do("super", "GET", "/api/admin/backups", "")
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), `"locked":[]`) ||
		!strings.Contains(w.Body.String(), `"backups":[]`) || !strings.Contains(w.Body.String(), `"time":"03:00"`) {
		t.Fatalf("get: %d %q %s", w.Code, w.Header().Get("Cache-Control"), w.Body)
	}

	for _, body := range []string{`{"time":"25:00"}`, `{"time":"3:00"}`, `{"keep_daily":0}`, `{"keep_monthly":-1}`,
		`{"remote":{"endpoint":"https://s3 .acme.cl"}}`, `{"remote":{"endpoint":"ftp://s3.acme.cl","bucket":"kw"}}`} {
		if w := f.do("super", "PUT", "/api/admin/backups/settings", body); w.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	w = f.do("super", "PUT", "/api/admin/backups/settings", `{"time":"04:30","keep_daily":10}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"time":"04:30"`) || !strings.Contains(w.Body.String(), `"keep_daily":10`) {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = 'backup.settings_update'`); n != 1 {
		t.Errorf("audit: %d events", n)
	}

	// Un destino externo cifra con la llave de respaldo: sin ella, no se guarda (§13.3).
	remote := `{"remote":{"endpoint":"https://s3.acme.cl","bucket":"kw","access_key_id":"AKIA","secret":"clave-del-bucket"}}`
	if w := f.do("super", "PUT", "/api/admin/backups/settings", remote); w.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(w.Body.String(), "backup_key_missing") {
		t.Errorf("remote without key: %d %s", w.Code, w.Body)
	}
	if err := backup.GenerateKey(filepath.Join(f.backupDir, "backup.key")); err != nil {
		t.Fatal(err)
	}
	w = f.do("super", "PUT", "/api/admin/backups/settings", remote)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "clave-del-bucket") || !strings.Contains(w.Body.String(), `"secret":"••••••"`) ||
		!strings.Contains(w.Body.String(), `"has_key":true`) {
		t.Fatalf("remote: %d %s", w.Code, w.Body)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM site_settings WHERE key = 'backup.remote.secret' AND secret_enc IS NOT NULL AND value_json IS NULL`); n != 1 {
		t.Errorf("the secret is not sealed: %d rows", n)
	}
	// Reenviar la máscara no cambia la clave; borrar el destino la deja.
	if w := f.do("super", "PUT", "/api/admin/backups/settings", `{"remote":{"endpoint":"","secret":"••••••"}}`); w.Code != http.StatusOK ||
		!strings.Contains(w.Body.String(), `"endpoint":""`) {
		t.Errorf("clear remote: %d %s", w.Code, w.Body)
	}

	// Respaldar ahora: un respaldo manual completo en la carpeta, que la lista muestra.
	if w := f.do("super", "POST", "/api/admin/backups", ""); w.Code != http.StatusAccepted {
		t.Fatalf("run: %d %s", w.Code, w.Body)
	}
	st := f.backupsState(t)
	if st.Last == nil || !st.Last.OK || st.Last.Kind != "backup" || len(st.Backups) != 1 || st.Backups[0].Kind != backup.Manual ||
		st.Backups[0].Name != st.Last.File || st.Backups[0].Size == 0 {
		t.Fatalf("after run: %+v", st)
	}
	// La prueba semanal corre con el primero: queda marcado como restaurado.
	if !st.Backups[0].Verified {
		t.Errorf("first backup not verified: %+v", st.Backups[0])
	}
	if n := f.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = 'backup.run' AND actor_id = ?`, f.users["super"]); n != 1 {
		t.Errorf("manual backup audited as the superadmin: %d", n)
	}
}

// Borrar un respaldo desde el panel (F8-B.6): pide reautenticarse, solo borra lo que está en la
// lista y nunca el último probado ni el de antes de "Borrar todo".
func TestBackupDelete(t *testing.T) {
	f := newFixture(t)
	if w := f.do("super", "POST", "/api/admin/backups", ""); w.Code != http.StatusAccepted {
		t.Fatalf("run: %d %s", w.Code, w.Body)
	}
	verified := f.backupsState(t).Backups[0]
	at := time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC)
	daily, preReset := backup.Name(backup.Daily, at), backup.Name(backup.PreReset, at)
	for _, name := range []string{daily, preReset} {
		if err := os.WriteFile(filepath.Join(f.backupDir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(f.backupDir, "..", "otro.tar.zst"), []byte("x"), 0o600)

	if w := f.do("super", "DELETE", "/api/admin/backups/"+daily, ""); w.Code != http.StatusUnauthorized ||
		!strings.Contains(w.Body.String(), "reauth_required") {
		t.Fatalf("without reauth: %d %s", w.Code, w.Body)
	}
	f.reauth(t, "super")

	// Lo que no está en la lista no existe: tampoco una ruta que salga de la carpeta.
	for _, name := range []string{"kudawiki-daily-20990101-000000.tar.zst", "..%2Fotro.tar.zst", "backup.key", "wiki.db"} {
		if w := f.do("super", "DELETE", "/api/admin/backups/"+name, ""); w.Code != http.StatusNotFound {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	if _, err := os.Stat(filepath.Join(f.backupDir, "..", "otro.tar.zst")); err != nil {
		t.Error("a file outside the folder was deleted")
	}

	st := f.backupsState(t)
	prot := map[string]bool{}
	for _, b := range st.Backups {
		prot[b.Name] = b.Protected
	}
	if !prot[verified.Name] || !prot[preReset] || prot[daily] {
		t.Errorf("protected flags: %v", prot)
	}
	for _, name := range []string{verified.Name, preReset} {
		if w := f.do("super", "DELETE", "/api/admin/backups/"+name, ""); w.Code != http.StatusConflict ||
			!strings.Contains(w.Body.String(), "backup_protected") {
			t.Errorf("protected %s: %d %s", name, w.Code, w.Body)
		}
	}

	if w := f.do("super", "DELETE", "/api/admin/backups/"+daily, ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(filepath.Join(f.backupDir, daily)); !os.IsNotExist(err) {
		t.Errorf("still there: %v", err)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = 'backup.delete' AND target_id = ? AND actor_id = ?`, daily, f.users["super"]); n != 1 {
		t.Errorf("audit: %d events", n)
	}
	if len(f.backupsState(t).Backups) != 2 {
		t.Error("the list still shows the deleted backup")
	}
}

// Bajar un respaldo desde el panel (F8-B.7): tal cual, con reautenticación, sin cache y con
// quién lo bajó en la auditoría. Solo un archivo de la lista.
func TestBackupDownload(t *testing.T) {
	f := newFixture(t)
	if w := f.do("super", "POST", "/api/admin/backups", ""); w.Code != http.StatusAccepted {
		t.Fatalf("run: %d %s", w.Code, w.Body)
	}
	name := f.backupsState(t).Backups[0].Name
	want, err := os.ReadFile(filepath.Join(f.backupDir, name))
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(f.backupDir, "..", "otro.tar.zst"), []byte("x"), 0o600)
	url := "/api/admin/backups/" + name + "/download"

	check := "/api/admin/backups/" + name
	for _, u := range []string{check, url} {
		if w := f.do("super", "GET", u, ""); w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "reauth_required") {
			t.Fatalf("%s without reauth: %d %s", u, w.Code, w.Body)
		}
	}
	f.reauth(t, "super")
	for _, n := range []string{"kudawiki-daily-20990101-000000.tar.zst", "..%2Fotro.tar.zst", "backup.key", "..%2Fwiki.db"} {
		for _, u := range []string{"/api/admin/backups/" + n, "/api/admin/backups/" + n + "/download"} {
			if w := f.do("super", "GET", u, ""); w.Code != http.StatusNotFound {
				t.Errorf("%s: %d", u, w.Code)
			}
		}
	}

	if w := f.do("super", "GET", check, ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"name":"`+name+`"`) {
		t.Fatalf("check: %d %s", w.Code, w.Body)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = 'backup.download'`); n != 0 {
		t.Errorf("checking is not downloading: %d events", n)
	}
	w := f.do("super", "GET", url, "")
	if w.Code != http.StatusOK || w.Body.String() != string(want) {
		t.Fatalf("download: %d, %d bytes of %d", w.Code, w.Body.Len(), len(want))
	}
	if h := w.Header(); h.Get("Cache-Control") != "no-store" || h.Get("Content-Disposition") != `attachment; filename="`+name+`"` ||
		h.Get("Content-Type") != "application/octet-stream" {
		t.Errorf("headers: %v", h)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = 'backup.download' AND target_id = ? AND actor_id = ?`, name, f.users["super"]); n != 1 {
		t.Errorf("audit: %d events", n)
	}
}

func TestReset(t *testing.T) {
	f := newFixture(t)
	f.expect(t, "editorTI", "PUT", "/api/w/ti/pages/manual", `{"title":"Manual","markdown":"texto secreto de TI","base_revision":0}`, http.StatusNoContent)
	if w := f.upload("editorTI", "ti", "diagrama.png", pngBytes(t)); w.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	doc := f.newSealed(t, "claves", "Claves", "la clave del firewall")
	f.attach(t, "editorTI", doc, "red.png", pngBytes(t))
	f.createTemplate(t, "super", "/api/admin/templates", `{"name":"Acta","markdown":"# Acta","level":2}`)
	auditBefore := f.count(t, `SELECT COUNT(*) FROM audit_log`)

	// Sin reautenticarse no hay borrado; con la frase mal, tampoco.
	if w := f.do("super", "POST", "/api/admin/reset", `{"confirm":"BORRAR TODO"}`); w.Code != http.StatusUnauthorized ||
		!strings.Contains(w.Body.String(), "reauth_required") {
		t.Fatalf("without reauth: %d %s", w.Code, w.Body)
	}
	f.reauth(t, "super")
	for _, phrase := range []string{"", "borrar todo", "DELETE ALL", "BORRAR TODO "} {
		if w := f.do("super", "POST", "/api/admin/reset", `{"confirm":"`+phrase+`"}`); w.Code != http.StatusUnprocessableEntity ||
			!strings.Contains(w.Body.String(), "reset_confirm_wrong") {
			t.Errorf("phrase %q: %d %s", phrase, w.Code, w.Body)
		}
	}
	if n := f.count(t, `SELECT COUNT(*) FROM users`); n != 6 {
		t.Fatalf("something was deleted before confirming: %d users", n)
	}

	// Si el respaldo de antes no se puede hacer, no se borra nada: la carpeta es un archivo.
	os.RemoveAll(f.backupDir)
	os.WriteFile(f.backupDir, []byte("no soy una carpeta"), 0o600)
	if w := f.do("super", "POST", "/api/admin/reset", `{"confirm":"BORRAR TODO"}`); w.Code != http.StatusAccepted {
		t.Fatalf("reset: %d %s", w.Code, w.Body)
	}
	if st := f.backupsState(t); st.Last == nil || st.Last.OK || st.Last.Kind != "reset" {
		t.Fatalf("reset without backup: %+v", st.Last)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM users`); n != 6 {
		t.Fatalf("deleted without a backup: %d users", n)
	}
	os.Remove(f.backupDir)

	if w := f.do("super", "POST", "/api/admin/reset", `{"confirm":"BORRAR TODO"}`); w.Code != http.StatusAccepted {
		t.Fatalf("reset: %d %s", w.Code, w.Body)
	}
	st := f.backupsState(t)
	if st.Last == nil || !st.Last.OK || st.Last.Kind != "reset" {
		t.Fatalf("reset result: %+v", st.Last)
	}
	// El respaldo de antes del borrado queda en la carpeta, y es lo que dice el resultado.
	if len(st.Backups) != 1 || st.Backups[0].Kind != backup.PreReset || st.Backups[0].Name != st.Last.File {
		t.Fatalf("pre-reset backup: %+v", st.Backups)
	}

	for query, want := range map[string]int{
		`SELECT COUNT(*) FROM users`:                                                1,
		`SELECT COUNT(*) FROM workspaces WHERE kind = 'area'`:                       0,
		`SELECT COUNT(*) FROM workspaces WHERE kind = 'personal'`:                   1, // el del superadmin, vacío
		`SELECT COUNT(*) FROM pages`:                                                0,
		`SELECT COUNT(*) FROM pages_fts`:                                            0,
		`SELECT COUNT(*) FROM media_assets`:                                         0,
		`SELECT COUNT(*) FROM sealed_pages`:                                         0,
		`SELECT COUNT(*) FROM sealed_media`:                                         0,
		`SELECT COUNT(*) FROM page_templates`:                                       0,
		`SELECT COUNT(*) FROM audit_log WHERE action = 'site.reset'`:                1,
		`SELECT COUNT(*) FROM audit_log WHERE action = 'backup.run'`:                1,
		`SELECT COUNT(*) FROM audit_log WHERE action = 'page.create'`:               1, // la auditoría es evidencia: queda
		`SELECT COUNT(*) FROM sessions WHERE user_id <> '` + f.users["super"] + `'`: 0,
	} {
		if n := f.count(t, query); n != want {
			t.Errorf("%s: %d, want %d", query, n, want)
		}
	}
	if n := f.count(t, `SELECT COUNT(*) FROM audit_log`); n < auditBefore {
		t.Errorf("audit shrank: %d < %d", n, auditBefore)
	}
	if files := f.encFiles(t); len(files) != 0 {
		t.Errorf("sealed files left on disk: %v", files)
	}
	entries, _ := os.ReadDir(f.storage)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "ws_") {
			t.Errorf("storage still has %s", e.Name())
		}
	}
	// Quien borró sigue adentro, con su sesión; los demás ya no existen.
	if w := f.do("super", "GET", "/api/auth/me", ""); w.Code != http.StatusOK {
		t.Errorf("super after reset: %d", w.Code)
	}
	if w := f.do("adminTI", "GET", "/api/auth/me", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("adminTI after reset: %d", w.Code)
	}
	if w := f.do("super", "GET", "/api/w/ti/pages/manual", ""); w.Code != http.StatusNotFound {
		t.Errorf("page after reset: %d", w.Code)
	}
}
