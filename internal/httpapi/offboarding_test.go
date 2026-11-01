package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/repo"
)

func (f fixture) count(t *testing.T, query string, args ...any) (n int) {
	t.Helper()
	if err := f.db.Read.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (f fixture) runOffboarding(t *testing.T, after time.Duration) {
	t.Helper()
	*f.clock = f.clock.Add(after)
	if err := f.offboarding.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// TestOffboarding es la baja de usuarios (§8.4): solo la ve el superadmin, desactiva en el acto,
// reactivar antes de la gracia no pierde nada, y al vencer lo suyo pasa al admin del área y lo
// no publicado se borra. Un año después, la auditoría la muestra con un seudónimo (B14).
func TestOffboarding(t *testing.T) {
	f := newFixture(t)
	editor := f.users["editorTI"]
	plan := "/api/admin/users/" + editor + "/offboarding"

	f.expect(t, "editorTI", "PUT", "/api/w/ti/pages/manual", `{"title":"Manual","markdown":"uno","base_revision":0}`, http.StatusNoContent)
	f.expect(t, "editorTI", "POST", "/api/w/ti/read-requests", `{"page":"manual","audience":{"all":true}}`, http.StatusCreated)
	f.expect(t, "editorTI", "POST", "/api/w/ti/quizzes", `{"page":"manual","title":"Manual"}`, http.StatusCreated)
	f.expect(t, "editorTI", "PUT", "/api/w/ti/drafts/nueva", `{"title":"Nueva","markdown":"a medio escribir","base_revision":0}`, http.StatusNoContent)
	own := f.personalSlug(t, "editorTI")
	f.expect(t, "editorTI", "PUT", "/api/w/"+own+"/pages/idea", `{"title":"Idea","markdown":"mía","base_revision":0}`, http.StatusNoContent)

	// Solo el superadmin: a cualquier otro, el 404 uniforme.
	uniform := f.do("adminTI", "GET", "/api/w/no-existe/tree", "")
	for _, who := range []string{"adminTI", "editorTI", "outsider"} {
		for _, method := range []string{"GET", "POST"} {
			if w := f.do(who, method, plan, `{"now":true}`); w.Code != uniform.Code || w.Body.String() != uniform.Body.String() {
				t.Errorf("%s %s %s: %d, want the uniform 404", who, method, plan, w.Code)
			}
		}
	}
	var p struct {
		Areas []struct {
			Slug, Receiver               string
			Pages, Quizzes, ReadRequests int  `json:"-"`
			NeedsAdmin                   bool `json:"needs_admin"`
		}
		PersonalPages int `json:"personal_pages"`
		Drafts        int
		GraceDays     int `json:"grace_days"`
	}
	json.Unmarshal([]byte(f.expect(t, "super", "GET", plan, "", http.StatusOK)), &p)
	if len(p.Areas) != 1 || p.Areas[0].Slug != "ti" || p.Areas[0].Receiver != f.users["adminTI"] || p.Areas[0].NeedsAdmin ||
		p.PersonalPages != 1 || p.Drafts != 1 || p.GraceDays != 30 {
		t.Fatalf("plan: %+v", p)
	}

	// Desactivar pide reautenticarse; a sí mismo no, y el receptor tiene que ser admin del área.
	f.expect(t, "super", "POST", plan, `{}`, http.StatusUnauthorized)
	f.reauth(t, "super")
	f.expect(t, "super", "POST", "/api/admin/users/"+f.users["super"]+"/offboarding", `{}`, http.StatusConflict)
	f.expect(t, "super", "POST", plan, `{"receivers":{"`+wsID(t, f, "ti")+`":"`+f.users["viewerTI"]+`"}}`, http.StatusUnprocessableEntity)

	// La gracia: la cuenta queda sin acceso ya, pero nada se transfiere todavía.
	if body := f.expect(t, "super", "POST", plan, `{}`, http.StatusOK); !strings.Contains(body, `"pending"`) {
		t.Fatalf("start: %s", body)
	}
	f.expect(t, "editorTI", "GET", "/api/auth/me", "", http.StatusUnauthorized)
	if w := f.do("", "POST", "/api/auth/login", `{"email":"editorTI@empresa.cl","password":"`+password+`"}`); w.Code == http.StatusNoContent {
		t.Fatal("a deactivated account logs in")
	}
	if body := f.expect(t, "super", "GET", "/api/admin/users", "", http.StatusOK); !strings.Contains(body, `"offboarding_at"`) {
		t.Errorf("the list does not show the pending offboarding: %s", body)
	}
	f.expect(t, "super", "POST", plan, `{}`, http.StatusConflict) // ya hay una en curso
	f.runOffboarding(t, 29*24*time.Hour)
	if n := f.count(t, `SELECT COUNT(*) FROM pages WHERE slug = 'manual' AND owner_id = ?`, editor); n != 1 {
		t.Fatalf("transferred before the grace period ended: %d pages left", n)
	}

	// Reactivar antes de que venza: vuelve a entrar y todo sigue ahí.
	f.expect(t, "super", "PATCH", "/api/admin/users/"+editor, `{"is_active":true}`, http.StatusNoContent)
	f.expect(t, "super", "PATCH", "/api/admin/users/"+editor, `{"is_active":true}`, http.StatusConflict)
	w := f.do("", "POST", "/api/auth/login", `{"email":"editorTI@empresa.cl","password":"`+password+`"}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("reactivated login: %d", w.Code)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM page_drafts WHERE user_id = ?`, editor); n != 1 {
		t.Errorf("reactivating lost the draft")
	}

	// Ahora sí: vence la gracia y el job completa la baja.
	f.expect(t, "super", "POST", plan, `{}`, http.StatusOK)
	f.runOffboarding(t, 31*24*time.Hour)
	admin := f.users["adminTI"]
	for q, want := range map[string]int{
		`SELECT COUNT(*) FROM pages WHERE slug = 'manual' AND owner_id = '` + admin + `'`:                                                    1,
		`SELECT COUNT(*) FROM quizzes WHERE created_by = '` + admin + `'`:                                                                    1,
		`SELECT COUNT(*) FROM read_requests WHERE created_by = '` + admin + `'`:                                                              1,
		`SELECT COUNT(*) FROM page_revisions r JOIN pages p ON p.id = r.page_id WHERE p.slug = 'manual' AND r.created_by = '` + editor + `'`: 1,
		`SELECT COUNT(*) FROM page_drafts WHERE user_id = '` + editor + `'`:                                                                  0,
		`SELECT COUNT(*) FROM workspaces WHERE owner_user_id = '` + editor + `'`:                                                             0,
		`SELECT COUNT(*) FROM workspace_members WHERE user_id = '` + editor + `'`:                                                            0,
		`SELECT COUNT(*) FROM users WHERE id = '` + editor + `' AND offboarded_at IS NOT NULL AND is_active = 0`:                             1,
	} {
		if got := f.count(t, q); got != want {
			t.Errorf("%s = %d, want %d", q, got, want)
		}
	}
	if body := f.expect(t, "adminTI", "GET", "/api/me/notifications", "", http.StatusOK); !strings.Contains(body, `"ownership_received"`) ||
		!strings.Contains(body, `"count":3`) {
		t.Errorf("the receiver was not told: %s", body)
	}
	for _, action := range []string{"user.offboarding_start", "user.reactivate", "user.offboarding_complete", "page.owner_transfer", "personal.purge"} {
		if f.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = ?`, action) == 0 {
			t.Errorf("no %s in the audit log", action)
		}
	}
	f.expect(t, "super", "POST", plan, `{"now":true}`, http.StatusConflict) // ya se fue

	// B14: al año de la baja, sus eventos quedan con un seudónimo y la cadena sigue intacta.
	f.runOffboarding(t, 364*24*time.Hour)
	if n := f.count(t, `SELECT COUNT(*) FROM audit_log WHERE actor_id = ? AND actor_snapshot_json LIKE '%pseudonym%'`, editor); n != 0 {
		t.Fatalf("pseudonymized before the year: %d", n)
	}
	f.runOffboarding(t, 2*24*time.Hour)
	if n := f.count(t, `SELECT COUNT(*) FROM audit_log WHERE actor_id = ? AND actor_snapshot_json NOT LIKE '{"pseudonym":"%'`, editor); n != 0 {
		t.Errorf("%d events still carry the person's name", n)
	}
	if body := f.expect(t, "super", "GET", "/api/admin/audit?action=page.create", "", http.StatusOK); strings.Contains(body, "editorTI@empresa.cl") ||
		!strings.Contains(body, `"pseudonym"`) {
		t.Errorf("the audit view still names the person: %s", body)
	}
	if _, err := audit.Verify(context.Background(), repo.New(f.db)); err != nil {
		t.Errorf("pseudonymizing broke the chain: %v", err)
	}
}

// TestOffboardingNow: la baja inmediata espera a que el área tenga otro admin si quien se va era
// el último (§8.4); con la gracia, el job tampoco la completa mientras tanto.
func TestOffboardingNow(t *testing.T) {
	f := newFixture(t)
	f.reauth(t, "super")
	plan := "/api/admin/users/" + f.users["adminFin"] + "/offboarding"
	var p struct {
		Areas []struct {
			NeedsAdmin bool `json:"needs_admin"`
		}
	}
	json.Unmarshal([]byte(f.expect(t, "super", "GET", plan, "", http.StatusOK)), &p)
	if len(p.Areas) != 1 || !p.Areas[0].NeedsAdmin {
		t.Fatalf("plan: %+v", p)
	}
	if body := f.expect(t, "super", "POST", plan, `{"now":true}`, http.StatusConflict); !strings.Contains(body, "area_needs_admin") {
		t.Fatalf("now without an admin: %s", body)
	}
	f.expect(t, "super", "POST", plan, `{}`, http.StatusOK)
	f.runOffboarding(t, 31*24*time.Hour)
	if n := f.count(t, `SELECT COUNT(*) FROM users WHERE id = ? AND offboarded_at IS NULL`, f.users["adminFin"]); n != 1 {
		t.Fatal("completed with the area left without an admin")
	}
	// Con otro admin nombrado, se puede adelantar.
	f.expect(t, "super", "POST", "/api/admin/workspaces/"+wsID(t, f, "finanzas")+"/members", `{"email":"outsider@empresa.cl","role":"admin"}`, http.StatusNoContent)
	if body := f.expect(t, "super", "POST", plan, `{"now":true}`, http.StatusOK); !strings.Contains(body, `"completed"`) {
		t.Fatalf("now: %s", body)
	}
}

// TestSuperadminToggle es B15: dar y quitar el superadmin pide reautenticarse, a sí mismo no se
// lo quita nadie y una cuenta desactivada no lo recibe.
func TestSuperadminToggle(t *testing.T) {
	f := newFixture(t)
	user := "/api/admin/users/" + f.users["adminTI"]
	f.expect(t, "super", "PATCH", user, `{"is_global_admin":true}`, http.StatusUnauthorized)
	f.expect(t, "adminTI", "PATCH", user, `{"is_global_admin":true}`, http.StatusNotFound)
	f.reauth(t, "super")
	f.expect(t, "super", "PATCH", user, `{"is_global_admin":true}`, http.StatusNoContent)
	f.expect(t, "adminTI", "GET", "/api/admin/users", "", http.StatusOK)
	f.expect(t, "super", "PATCH", user, `{"is_global_admin":false}`, http.StatusNoContent)
	f.expect(t, "adminTI", "GET", "/api/admin/users", "", http.StatusNotFound)
	f.expect(t, "super", "PATCH", "/api/admin/users/"+f.users["super"], `{"is_global_admin":false}`, http.StatusConflict)
	f.expect(t, "super", "PATCH", user, `{"is_active":false}`, http.StatusBadRequest)
	f.expect(t, "super", "POST", "/api/admin/users/"+f.users["viewerTI"]+"/offboarding", `{}`, http.StatusOK)
	f.expect(t, "super", "PATCH", "/api/admin/users/"+f.users["viewerTI"], `{"is_global_admin":true}`, http.StatusConflict)
	for _, action := range []string{"user.superadmin_grant", "user.superadmin_revoke"} {
		if f.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = ?`, action) != 1 {
			t.Errorf("want one %s", action)
		}
	}
}

// TestRemoveMemberTransfers: quitar a alguien de un área le pasa lo suyo de esa área al admin
// más antiguo (§8.4), sin aviso para quien lo hizo.
func TestRemoveMemberTransfers(t *testing.T) {
	f := newFixture(t)
	f.expect(t, "editorTI", "PUT", "/api/w/ti/pages/manual", `{"title":"Manual","markdown":"uno","base_revision":0}`, http.StatusNoContent)
	f.reauth(t, "adminTI")
	f.expect(t, "adminTI", "DELETE", "/api/w/ti/members/"+f.users["editorTI"], "", http.StatusNoContent)
	if n := f.count(t, `SELECT COUNT(*) FROM pages WHERE slug = 'manual' AND owner_id = ?`, f.users["adminTI"]); n != 1 {
		t.Fatal("the page did not pass to the area admin")
	}
	if f.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = 'page.owner_transfer'`) != 1 {
		t.Error("the transfer is not in the audit log")
	}
	if body := f.expect(t, "adminTI", "GET", "/api/me/notifications", "", http.StatusOK); strings.Contains(body, "ownership_received") {
		t.Error("the admin was notified of their own change")
	}
}
