package httpapi

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func (f fixture) uploadFile(who, method, path string, data []byte) *httptest.ResponseRecorder {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("file", "foto")
	part.Write(data)
	mw.Close()
	r := httptest.NewRequest(method, path, &body)
	r.Header.Set("Origin", origin)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	if c := f.cookies[who]; c != nil {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	return w
}

// §8.5.2 y §14.1: cada persona cambia su nombre, su contraseña y su foto, y nada más.
func TestProfile(t *testing.T) {
	f := newFixture(t)
	login := func(email, pass string) *httptest.ResponseRecorder {
		return f.do("", "POST", "/api/auth/login", `{"email":"`+email+`","password":"`+pass+`"}`)
	}
	f.cookies["viewerTI2"] = login("viewerTI@empresa.cl", password).Result().Cookies()[0] // otra sesión, otro equipo

	// Nombre: sin vacíos ni caracteres de control.
	if w := f.do("viewerTI", "PATCH", "/api/me", `{"full_name":"  Vera Lectora "}`); w.Code != http.StatusNoContent {
		t.Fatalf("set name: %d %s", w.Code, w.Body)
	}
	if body := f.do("viewerTI", "GET", "/api/auth/me", "").Body.String(); !strings.Contains(body, `"full_name":"Vera Lectora"`) {
		t.Errorf("name not saved: %s", body)
	}
	for _, bad := range []string{`{"full_name":"  "}`, `{"full_name":"a\nb"}`} {
		if w := f.do("viewerTI", "PATCH", "/api/me", bad); w.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d", bad, w.Code)
		}
	}

	// Contraseña: pide la actual y la política; cierra las demás sesiones, no la propia.
	const next = "otra-clave-del-sur-77"
	if w := f.do("viewerTI", "POST", "/api/me/password", `{"current_password":"mala-clave-123","new_password":"`+next+`"}`); w.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(w.Body.String(), "wrong_password") {
		t.Errorf("wrong current: %d %s", w.Code, w.Body)
	}
	if w := f.do("viewerTI", "POST", "/api/me/password", `{"current_password":"`+password+`","new_password":"corta"}`); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("weak new password: %d", w.Code)
	}
	if w := f.do("viewerTI", "POST", "/api/me/password", `{"current_password":"`+password+`","new_password":"`+next+`","revoke_others":true}`); w.Code != http.StatusNoContent {
		t.Fatalf("change: %d %s", w.Code, w.Body)
	}
	if f.do("viewerTI", "GET", "/api/auth/me", "").Code != http.StatusOK || f.do("viewerTI2", "GET", "/api/auth/me", "").Code != http.StatusUnauthorized {
		t.Error("revoke_others must close the other session and keep this one")
	}
	if login("viewerTI@empresa.cl", password).Code != http.StatusUnauthorized || login("viewerTI@empresa.cl", next).Code != http.StatusNoContent {
		t.Error("the new password must replace the old one")
	}

	// Una sesión robada no sirve para adivinar la contraseña: mismo tope que el login.
	for i := range 11 {
		w := f.do("editorTI", "POST", "/api/me/password", `{"current_password":"intento-`+string(rune('a'+i))+`-123","new_password":"`+next+`"}`)
		if want := map[bool]int{true: http.StatusTooManyRequests, false: http.StatusUnprocessableEntity}[i == 10]; w.Code != want {
			t.Fatalf("attempt %d: %d, want %d", i+1, w.Code, want)
		}
	}

	// Foto: se re-codifica a 128 px; nada que no sea una imagen.
	if w := f.do("viewerTI", "GET", "/api/me/avatar", ""); w.Code != http.StatusNotFound {
		t.Errorf("no avatar yet: %d", w.Code)
	}
	var src bytes.Buffer
	png.Encode(&src, image.NewRGBA(image.Rect(0, 0, 300, 200)))
	if w := f.uploadFile("viewerTI", "PUT", "/api/me/avatar", src.Bytes()); w.Code != http.StatusNoContent {
		t.Fatalf("upload avatar: %d %s", w.Code, w.Body)
	}
	w := f.do("viewerTI", "GET", "/api/me/avatar", "")
	img, err := png.Decode(w.Body)
	if w.Code != http.StatusOK || err != nil || img.Bounds().Dx() != 128 || img.Bounds().Dy() != 128 ||
		!strings.Contains(w.Header().Get("Cache-Control"), "private") {
		t.Errorf("avatar: %d %v %v", w.Code, err, w.Header())
	}
	var me struct {
		HasAvatar bool `json:"has_avatar"`
	}
	json.Unmarshal(f.do("viewerTI", "GET", "/api/auth/me", "").Body.Bytes(), &me)
	if !me.HasAvatar {
		t.Error("has_avatar after upload")
	}
	if w := f.uploadFile("viewerTI", "PUT", "/api/me/avatar", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)); w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("svg avatar: %d", w.Code)
	}
	if w := f.uploadFile("viewerTI", "PUT", "/api/me/avatar", bytes.Repeat([]byte("x"), 5<<20+1)); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("huge avatar: %d", w.Code)
	}
	if f.do("editorTI", "GET", "/api/me/avatar", "").Code != http.StatusNotFound {
		t.Error("each person sees only their own avatar")
	}
	if w := f.do("viewerTI", "DELETE", "/api/me/avatar", ""); w.Code != http.StatusNoContent || f.do("viewerTI", "GET", "/api/me/avatar", "").Code != http.StatusNotFound {
		t.Errorf("delete avatar: %d", w.Code)
	}
	if f.do("", "GET", "/api/me/avatar", "").Code != http.StatusUnauthorized {
		t.Error("avatar without session")
	}

	// En la auditoría: el cambio de contraseña y los del perfil (nombre, foto subida y quitada).
	for action, want := range map[string]int{"auth.password_change": 1, "user.profile_update": 3} {
		var out struct{ Events []struct{ Action string } }
		json.Unmarshal(f.do("super", "GET", "/api/admin/audit?action="+action, "").Body.Bytes(), &out)
		if len(out.Events) != want {
			t.Errorf("%s audited %d times, want %d", action, len(out.Events), want)
		}
	}
}
