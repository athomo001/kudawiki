package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// §8.2: mis sesiones (cada persona las suyas), cerrar las demás al desactivar el 2FA y el
// superadmin cerrando las de otra persona (B16).
func TestSessions(t *testing.T) {
	f := newFixture(t)
	uniform := f.do("adminTI", "GET", "/api/w/no-existe/tree", "")
	// login abre otra sesión de viewerTI desde un navegador y la deja en f.cookies[name].
	login := func(name, ua string) {
		t.Helper()
		r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"email":"viewerTI@empresa.cl","password":"`+password+`"}`))
		r.Header.Set("Origin", origin)
		r.Header.Set("User-Agent", ua)
		w := httptest.NewRecorder()
		f.h.ServeHTTP(w, r)
		if w.Code != http.StatusNoContent {
			t.Fatalf("login %s: %d %s", name, w.Code, w.Body)
		}
		f.cookies[name] = w.Result().Cookies()[0]
	}
	type info struct {
		ID      string
		Device  struct{ Browser, OS string }
		IP      string
		Current bool
	}
	sessions := func(who string) []info {
		t.Helper()
		var out []info
		json.Unmarshal([]byte(f.expect(t, who, "GET", "/api/me/sessions", "", http.StatusOK)), &out)
		return out
	}

	login("chrome", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36")
	list := sessions("viewerTI")
	if len(list) != 2 {
		t.Fatalf("sessions = %+v", list)
	}
	var other info
	for _, x := range list {
		if !x.Current {
			other = x
		}
	}
	if other.Device.Browser != "Chrome" || other.Device.OS != "Windows" || other.IP == "" || other.ID == "" {
		t.Errorf("the other session = %+v", other)
	}
	if body := f.expect(t, "viewerTI", "GET", "/api/me/sessions", "", http.StatusOK); strings.Contains(body, "token") {
		t.Errorf("the list carries the token hash: %s", body)
	}
	// La sesión de otra persona no existe para nadie más.
	if w := f.do("editorTI", "DELETE", "/api/me/sessions/"+other.ID, ""); w.Code != uniform.Code || w.Body.String() != uniform.Body.String() {
		t.Errorf("close someone else's session: %d %s", w.Code, w.Body)
	}
	f.expect(t, "chrome", "GET", "/api/auth/me", "", http.StatusOK)
	f.expect(t, "viewerTI", "DELETE", "/api/me/sessions/"+other.ID, "", http.StatusNoContent)
	f.expect(t, "chrome", "GET", "/api/auth/me", "", http.StatusUnauthorized)

	login("firefox", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14.6; rv:141.0) Gecko/20100101 Firefox/141.0")
	login("edge", "Mozilla/5.0 (Windows NT 10.0) Chrome/140.0.0.0 Safari/537.36 Edg/140.0.0.0")
	if body := f.expect(t, "viewerTI", "POST", "/api/me/sessions/revoke-others", "", http.StatusOK); !strings.Contains(body, `"sessions_closed":2`) {
		t.Errorf("revoke others: %s", body)
	}
	f.expect(t, "firefox", "GET", "/api/auth/me", "", http.StatusUnauthorized)
	f.expect(t, "viewerTI", "GET", "/api/auth/me", "", http.StatusOK)
	if e := find(auditOf(t, f, "super", "/api/admin/audit"), "session.revoke", "viewerTI"); e == nil || e.Meta["others"] != true {
		t.Errorf("revoke left no trace: %+v", e)
	}

	// Desactivar el 2FA ofrece cerrar las demás (§8.2).
	codes := f.enrollTOTP(t, "viewerTI").codes
	w := f.do("", "POST", "/api/auth/login", `{"email":"viewerTI@empresa.cl","password":"`+password+`"}`)
	f.cookies["mfa"] = w.Result().Cookies()[0]
	w = f.do("mfa", "POST", "/api/auth/totp", `{"code":"`+codes[0]+`"}`)
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			f.cookies["second"] = c
		}
	}
	f.expect(t, "viewerTI", "DELETE", "/api/me/totp", `{"password":"`+password+`","code":"`+codes[1]+`","revoke_others":true}`, http.StatusNoContent)
	f.expect(t, "second", "GET", "/api/auth/me", "", http.StatusUnauthorized)
	f.expect(t, "viewerTI", "GET", "/api/auth/me", "", http.StatusOK)

	// El superadmin cierra todas las de otra persona; para los demás la ruta no existe.
	revoke := `{"revoke_sessions":true}`
	if w := f.do("adminTI", "PATCH", "/api/admin/users/"+f.users["viewerTI"], revoke); w.Code != uniform.Code || w.Body.String() != uniform.Body.String() {
		t.Errorf("area admin closes sessions: %d", w.Code)
	}
	f.expect(t, "super", "PATCH", "/api/admin/users/01ARZ3NDEKTSV4RRFFQ69G5FAV", revoke, http.StatusNotFound)
	if body := f.expect(t, "super", "PATCH", "/api/admin/users/"+f.users["viewerTI"], revoke, http.StatusOK); !strings.Contains(body, `"sessions_closed":1`) {
		t.Errorf("admin revoke: %s", body)
	}
	f.expect(t, "viewerTI", "GET", "/api/auth/me", "", http.StatusUnauthorized)
	f.expect(t, "editorTI", "GET", "/api/auth/me", "", http.StatusOK)
}
