package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// §8.1: el 2FA obligatorio para los admins, el segundo paso del login y el restablecimiento.
func TestTOTP(t *testing.T) {
	f := newFixture(t)

	// El estado viaja con /auth/me.
	var me struct {
		TOTP struct {
			Enabled  bool `json:"enabled"`
			Required bool `json:"required"`
			Left     int  `json:"recovery_codes_left"`
		} `json:"totp"`
		Setup bool `json:"mfa_setup_required"`
	}
	json.Unmarshal(f.do("adminTI", "GET", "/api/auth/me", "").Body.Bytes(), &me)
	if !me.TOTP.Enabled || !me.TOTP.Required || me.TOTP.Left != 10 || me.Setup {
		t.Errorf("adminTI me = %+v", me)
	}
	// Ser admin de su espacio personal (§7) no lo hace obligatorio para un lector. /auth/me crea
	// el espacio; el pedido siguiente ya lo tiene.
	f.do("viewerTI", "GET", "/api/auth/me", "")
	me.TOTP.Required = true
	json.Unmarshal(f.do("viewerTI", "GET", "/api/auth/me", "").Body.Bytes(), &me)
	if me.TOTP.Required || me.Setup {
		t.Errorf("viewerTI me = %+v", me)
	}
	if w := f.do("viewerTI", "GET", "/api/me/notifications", ""); w.Code != http.StatusOK {
		t.Errorf("viewer without 2FA: %d", w.Code)
	}

	// Con 2FA, la contraseña sola no abre sesión: da el desafío en su propia cookie.
	w := f.do("", "POST", "/api/auth/login", `{"email":"adminTI@empresa.cl","password":"`+password+`"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"totp_required":true`) {
		t.Fatalf("login with 2FA: %d %s", w.Code, w.Body)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != mfaCookie || !cookies[0].HttpOnly || cookies[0].Path != "/api/auth" {
		t.Fatalf("login cookies = %+v", cookies)
	}
	f.cookies["mfa"] = cookies[0]
	if w := f.do("mfa", "GET", "/api/auth/me", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("the challenge is not a session: %d", w.Code)
	}
	if w := f.do("mfa", "POST", "/api/auth/totp", `{"code":"zzzz-zzzz-zzzz-zzzz"}`); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "invalid_code") {
		t.Errorf("wrong code: %d %s", w.Code, w.Body)
	}
	if w := f.do("", "POST", "/api/auth/totp", `{"code":"123456"}`); w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "mfa_expired") {
		t.Errorf("without challenge: %d %s", w.Code, w.Body)
	}
	// El código de la app de este paso ya se usó al configurarlo: entra con uno de recuperación.
	w = f.do("mfa", "POST", "/api/auth/totp", `{"code":"`+f.totp["adminTI"].codes[0]+`"}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("recovery code: %d %s", w.Code, w.Body)
	}
	for _, c := range w.Result().Cookies() {
		f.cookies["second"] = c
		if c.Name == sessionCookie {
			break
		}
	}
	if w := f.do("second", "GET", "/api/w/ti/tree", ""); w.Code != http.StatusOK {
		t.Errorf("session after the second step: %d", w.Code)
	}

	// Al admin se le exige: no puede apagarlo.
	if w := f.do("adminTI", "DELETE", "/api/me/totp", `{"password":"`+password+`","code":"`+f.totp["adminTI"].codes[1]+`"}`); w.Code != http.StatusConflict {
		t.Errorf("admin disables 2FA: %d %s", w.Code, w.Body)
	}

	// Restablecerlo es del superadmin; para los demás la ruta no existe.
	reset := `{"reset_totp":true}`
	if w := f.do("adminFin", "PATCH", "/api/admin/users/"+f.users["adminTI"], reset); w.Code != http.StatusNotFound {
		t.Errorf("area admin resets 2FA: %d", w.Code)
	}
	if w := f.do("super", "PATCH", "/api/admin/users/"+f.users["adminTI"], reset); w.Code != http.StatusNoContent {
		t.Fatalf("reset: %d %s", w.Code, w.Body)
	}
	// Sin el 2FA que se le exige, su sesión solo sirve para configurarlo.
	if w := f.do("adminTI", "GET", "/api/w/ti/tree", ""); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "mfa_setup_required") {
		t.Errorf("area route without 2FA: %d %s", w.Code, w.Body)
	}
	json.Unmarshal(f.do("adminTI", "GET", "/api/auth/me", "").Body.Bytes(), &me)
	if !me.Setup {
		t.Error("me after reset: mfa_setup_required = false")
	}
	f.enrollTOTP(t, "adminTI")
	if w := f.do("adminTI", "GET", "/api/w/ti/tree", ""); w.Code != http.StatusOK {
		t.Errorf("after setting it up again: %d", w.Code)
	}

	// Un lector lo activa y lo desactiva con su contraseña y un código.
	viewer := f.enrollTOTP(t, "viewerTI")
	if w := f.do("viewerTI", "POST", "/api/me/totp", ""); w.Code != http.StatusConflict {
		t.Errorf("begin with 2FA on: %d", w.Code)
	}
	if w := f.do("viewerTI", "DELETE", "/api/me/totp", `{"password":"otra","code":"`+viewer.codes[0]+`"}`); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("disable with a wrong password: %d", w.Code)
	}
	if w := f.do("viewerTI", "DELETE", "/api/me/totp", `{"password":"`+password+`","code":"`+viewer.codes[0]+`"}`); w.Code != http.StatusNoContent {
		t.Errorf("disable: %d %s", w.Code, w.Body)
	}
	if w := f.do("", "POST", "/api/auth/login", `{"email":"viewerTI@empresa.cl","password":"`+password+`"}`); w.Code != http.StatusNoContent {
		t.Errorf("login after disabling: %d", w.Code)
	}
}
