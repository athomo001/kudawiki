package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func (f fixture) userID(t *testing.T, email string) string {
	t.Helper()
	var us []struct{ ID, Email string }
	json.Unmarshal(f.do("super", "GET", "/api/admin/users", "").Body.Bytes(), &us)
	for _, u := range us {
		if u.Email == email {
			return u.ID
		}
	}
	t.Fatalf("no user %s", email)
	return ""
}

// §8.5: sin correo no hay "¿Olvidaste tu contraseña?"; con correo el formulario responde igual
// exista o no la cuenta, y el superadmin puede dar un enlace manual.
func TestPasswordResetAPI(t *testing.T) {
	f := newFixture(t)
	viewer := f.userID(t, "viewerTI@empresa.cl")
	if w := f.do("", "POST", "/api/auth/password-reset", `{"email":"viewerTI@empresa.cl"}`); w.Code != http.StatusNotFound {
		t.Errorf("without SMTP: %d", w.Code)
	}
	if page := f.do("", "GET", "/", "").Body.String(); !strings.Contains(page, `"password_reset":false`) {
		t.Error("the login offers reset without SMTP")
	}

	// Un servidor configurado (no hace falta que conteste: la cola guarda el correo y reintenta).
	if w := f.do("super", "PUT", "/api/admin/smtp", `{"host":"127.0.0.1","port":1,"security":"none","from":"wiki@empresa.cl"}`); w.Code != http.StatusOK {
		t.Fatalf("smtp: %d %s", w.Code, w.Body)
	}
	if page := f.do("", "GET", "/", "").Body.String(); !strings.Contains(page, `"password_reset":true`) {
		t.Error("the login does not offer reset with SMTP")
	}
	unknown := f.do("", "POST", "/api/auth/password-reset", `{"email":"nadie@empresa.cl"}`)
	known := f.do("", "POST", "/api/auth/password-reset", `{"email":"viewerTI@empresa.cl"}`)
	if unknown.Code != http.StatusAccepted || known.Code != unknown.Code || known.Body.String() != unknown.Body.String() {
		t.Errorf("responses differ: %d %q / %d %q", unknown.Code, unknown.Body, known.Code, known.Body)
	}
	// El correo se encola fuera de la petición: solo para la cuenta que existe.
	queued := 0
	for range 50 {
		f.db.Read.QueryRow(`SELECT count(*) FROM email_outbox WHERE template = 'password_reset'`).Scan(&queued)
		if queued > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if queued != 1 {
		t.Errorf("queued reset emails = %d, want 1", queued)
	}

	// Solo el superadmin da enlaces; al resto, el 404 uniforme.
	for _, who := range []string{"adminTI", "editorTI", "viewerTI", "outsider"} {
		if w := f.do(who, "POST", "/api/admin/users/"+viewer+"/password-reset", `{"delivery":"manual"}`); w.Code != http.StatusNotFound {
			t.Errorf("%s: %d", who, w.Code)
		}
	}
	w := f.do("super", "POST", "/api/admin/users/"+viewer+"/password-reset", `{"delivery":"manual"}`)
	var out struct {
		ResetURL string `json:"reset_url"`
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	token, ok := strings.CutPrefix(out.ResetURL, origin+"/reset#")
	if w.Code != http.StatusCreated || !ok {
		t.Fatalf("manual link: %d %s", w.Code, w.Body)
	}
	body := `{"token":"` + token + `","new_password":"nueva-clave-del-lector-9"}`
	if w := f.do("", "POST", "/api/auth/password-reset/complete", body); w.Code != http.StatusNoContent {
		t.Fatalf("complete: %d %s", w.Code, w.Body)
	}
	if w := f.do("viewerTI", "GET", "/api/auth/me", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("the old session survived the reset: %d", w.Code)
	}
	if w := f.do("", "POST", "/api/auth/password-reset/complete", body); w.Code != http.StatusGone || !strings.Contains(w.Body.String(), "invalid_reset") {
		t.Errorf("used link: %d %s", w.Code, w.Body)
	}
	if w := f.do("super", "POST", "/api/admin/users/"+viewer+"/password-reset", `{"delivery":"email"}`); w.Code != http.StatusAccepted {
		t.Errorf("by email: %d %s", w.Code, w.Body)
	}

	// Con correo, la invitación sale por correo y el panel no ve el enlace.
	w = f.do("super", "POST", "/api/admin/users", `{"email":"nueva@empresa.cl","full_name":"Nueva"}`)
	if w.Code != http.StatusCreated || strings.Contains(w.Body.String(), "invite_url") || !strings.Contains(w.Body.String(), `"delivery":"email"`) {
		t.Errorf("invite with SMTP: %d %s", w.Code, w.Body)
	}
	if h := f.do("", "GET", "/reset", "").Header().Get("Referrer-Policy"); h != "no-referrer" {
		t.Errorf("/reset Referrer-Policy = %q", h)
	}
}
