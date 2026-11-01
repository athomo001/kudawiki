package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/athomo001/kudawiki/internal/auth/passkeytest"
)

// §8.1: registrar, entrar sin contraseña, el segundo paso, la reautenticación (§8.2) y quitar
// una passkey. El fixture tiene base_url en localhost: ahí hay passkeys.
func TestPasskeys(t *testing.T) {
	f := newFixture(t)
	uniform := f.do("adminTI", "GET", "/api/w/no-existe/tree", "")
	// post manda varias cookies a la vez: la ceremonia del login y, a veces, el segundo paso.
	post := func(path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Origin", origin)
		for _, c := range cookies {
			if c != nil {
				r.AddCookie(c)
			}
		}
		w := httptest.NewRecorder()
		f.h.ServeHTTP(w, r)
		return w
	}
	cookie := func(w *httptest.ResponseRecorder, name string) *http.Cookie {
		for _, c := range w.Result().Cookies() {
			if c.Name == name {
				return c
			}
		}
		return nil
	}
	// register registra una passkey de who; antes se reautentica con su contraseña.
	register := func(who, name string) *passkeytest.Authenticator {
		t.Helper()
		f.expect(t, who, "POST", "/api/auth/reauth", `{"password":"`+password+`","code":""}`, http.StatusNoContent)
		w := f.do(who, "POST", "/api/me/passkeys/begin", "")
		if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("begin %s: %d %s", who, w.Code, w.Body)
		}
		key := passkeytest.New(origin)
		body, _ := json.Marshal(map[string]any{"name": name, "credential": json.RawMessage(key.Create(w.Body.Bytes()))})
		f.expect(t, who, "POST", "/api/me/passkeys/finish", string(body), http.StatusCreated)
		return key
	}

	// El login sabe que hay passkeys antes de que nadie entre.
	if body := f.do("", "GET", "/", "").Body.String(); !strings.Contains(body, `"passkeys":true`) {
		t.Errorf("the login page does not offer passkeys: %s", body)
	}

	// Registrar pide reautenticarse: con una sesión robada no se suma una passkey propia.
	if w := f.do("viewerTI", "POST", "/api/me/passkeys/begin", ""); w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "reauth_required") {
		t.Errorf("begin without reauth: %d %s", w.Code, w.Body)
	}
	key := register("viewerTI", "Notebook")
	var list []struct{ ID, Name string }
	json.Unmarshal([]byte(f.expect(t, "viewerTI", "GET", "/api/me/passkeys", "", http.StatusOK)), &list)
	if len(list) != 1 || list[0].Name != "Notebook" || list[0].ID != key.ID() {
		t.Fatalf("passkeys = %+v", list)
	}
	if body := f.expect(t, "viewerTI", "GET", "/api/auth/me", "", http.StatusOK); !strings.Contains(body, `"passkeys":{"available":true,"count":1}`) {
		t.Errorf("me: %s", body)
	}
	// La de otra persona no existe para nadie más.
	if body := f.expect(t, "editorTI", "GET", "/api/me/passkeys", "", http.StatusOK); body != "[]\n" {
		t.Errorf("someone else's list: %s", body)
	}
	f.expect(t, "editorTI", "POST", "/api/auth/reauth", `{"password":"`+password+`","code":""}`, http.StatusNoContent)
	if w := f.do("editorTI", "DELETE", "/api/me/passkeys/"+key.ID(), ""); w.Code != uniform.Code || w.Body.String() != uniform.Body.String() {
		t.Errorf("remove someone else's: %d %s", w.Code, w.Body)
	}

	// Sin contraseña: la ceremonia va en su propia cookie, que solo viaja a /api/auth.
	begin := post("/api/auth/passkey/begin", "")
	ceremony := cookie(begin, passkeyCookie)
	if begin.Code != http.StatusOK || ceremony == nil || !ceremony.HttpOnly || ceremony.Path != "/api/auth" {
		t.Fatalf("passkey login begin: %d %+v", begin.Code, ceremony)
	}
	cred := string(key.Get(begin.Body.Bytes()))
	if w := post("/api/auth/passkey/finish", cred); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "passkey_invalid") {
		t.Errorf("finish without the ceremony: %d %s", w.Code, w.Body)
	}
	begin = post("/api/auth/passkey/begin", "")
	w := post("/api/auth/passkey/finish", string(key.Get(begin.Body.Bytes())), cookie(begin, passkeyCookie))
	if w.Code != http.StatusNoContent || cookie(w, sessionCookie) == nil {
		t.Fatalf("passkey login: %d %s", w.Code, w.Body)
	}
	f.cookies["byPasskey"] = cookie(w, sessionCookie)
	if body := f.expect(t, "byPasskey", "GET", "/api/auth/me", "", http.StatusOK); !strings.Contains(body, `"email":"viewerTI@empresa.cl"`) {
		t.Errorf("passkey session: %s", body)
	}

	// Con passkey, la contraseña sola ya no abre sesión: el segundo paso la acepta.
	w = f.do("", "POST", "/api/auth/login", `{"email":"viewerTI@empresa.cl","password":"`+password+`"}`)
	if w.Code != http.StatusOK || w.Body.String() != `{"methods":["passkey"],"mfa_required":true}`+"\n" {
		t.Fatalf("login with a passkey: %d %s", w.Code, w.Body)
	}
	mfa := cookie(w, mfaCookie)
	begin = post("/api/auth/passkey/begin", "")
	w = post("/api/auth/passkey/finish", string(key.Get(begin.Body.Bytes())), cookie(begin, passkeyCookie), mfa)
	if w.Code != http.StatusNoContent || cookie(w, mfaCookie) == nil || cookie(w, mfaCookie).MaxAge >= 0 {
		t.Fatalf("second step with a passkey: %d %+v", w.Code, w.Result().Cookies())
	}
	if w := post("/api/auth/totp", `{"code":"123456"}`, mfa); w.Code != http.StatusUnauthorized {
		t.Errorf("the second step is still open: %d", w.Code)
	}

	// La reautenticación acepta la passkey, y quitarla la pide.
	if w := f.do("byPasskey", "DELETE", "/api/me/passkeys/"+key.ID(), ""); w.Code != http.StatusUnauthorized {
		t.Errorf("remove without reauth: %d", w.Code)
	}
	opts := f.expect(t, "byPasskey", "POST", "/api/auth/reauth/passkey/begin", "", http.StatusOK)
	f.expect(t, "byPasskey", "POST", "/api/auth/reauth/passkey/finish", string(key.Get([]byte(opts))), http.StatusNoContent)
	f.expect(t, "byPasskey", "DELETE", "/api/me/passkeys/"+key.ID(), "", http.StatusNoContent)
	if body := f.expect(t, "byPasskey", "GET", "/api/me/passkeys", "", http.StatusOK); body != "[]\n" {
		t.Errorf("after removing: %s", body)
	}
	if e := find(auditOf(t, f, "super", "/api/admin/audit"), "auth.passkey_remove", "viewerTI"); e == nil {
		t.Error("the removal left no trace")
	}

	// A quien se le exige el 2FA, una passkey se lo cumple: outsider pasa a admin de Finanzas.
	f.reauth(t, "super")
	f.expect(t, "super", "POST", "/api/admin/workspaces/"+f.wsID(t, "finanzas")+"/members", `{"email":"outsider@empresa.cl","role":"admin"}`, http.StatusNoContent)
	if body := f.expect(t, "outsider", "GET", "/api/auth/me", "", http.StatusOK); !strings.Contains(body, `"mfa_setup_required":true`) {
		t.Fatalf("outsider must set up 2FA: %s", body)
	}
	register("outsider", "YubiKey")
	if body := f.expect(t, "outsider", "GET", "/api/auth/me", "", http.StatusOK); !strings.Contains(body, `"mfa_setup_required":false`) {
		t.Errorf("a passkey does not count as 2FA: %s", body)
	}
	f.expect(t, "outsider", "GET", "/api/me/notifications", "", http.StatusOK)
}
