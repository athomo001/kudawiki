package notify

import (
	"bytes"
	"context"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/database/dbtest"
	"github.com/athomo001/kudawiki/internal/mail"
	"github.com/athomo001/kudawiki/internal/proposals"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/athomo001/kudawiki/internal/wiki"
	"github.com/oklog/ulid/v2"
)

// fakeMail guarda lo que se encola en vez de mandarlo.
type fakeMail struct {
	enabled bool
	sent    []mail.Message
}

func (m *fakeMail) Enabled(context.Context) (bool, error) { return m.enabled, nil }
func (m *fakeMail) Enqueue(_ context.Context, msg mail.Message) error {
	m.sent = append(m.sent, msg)
	return nil
}

type env struct {
	ctx      context.Context
	store    *repo.Repo
	pages    *wiki.Service
	props    *proposals.Service
	mail     *fakeMail
	svc      *Service
	clock    time.Time
	sc       repo.Scope
	ana, ben string // ana administra el área; ben la lee
}

// newEnv arma un área con una admin y un lector que sigue la página "guia". El reloj del
// servicio lo mueve cada test.
func newEnv(t *testing.T, start time.Time) *env {
	t.Helper()
	e := &env{ctx: context.Background(), mail: &fakeMail{enabled: true}, clock: start}
	db := dbtest.Open(t)
	e.store = repo.New(db)
	rec := audit.NewRecorder(e.store, time.Now, slog.New(slog.DiscardHandler))
	e.ana, e.ben = ulid.Make().String(), ulid.Make().String()
	for id, name := range map[string]string{e.ana: "Ana", e.ben: "Ben"} {
		if err := e.store.CreateUser(e.ctx, repo.User{ID: id, Email: strings.ToLower(name) + "@x.cl", FullName: name, IsActive: true, Locale: "es"}); err != nil {
			t.Fatal(err)
		}
	}
	ws := repo.Workspace{ID: ulid.Make().String(), Slug: "ti", Name: "TI"}
	if err := e.store.CreateWorkspace(e.ctx, ws); err != nil {
		t.Fatal(err)
	}
	e.sc = repo.Scope{WsID: ws.ID, MaxRank: 3}
	for id, role := range map[string]string{e.ana: "admin", e.ben: "viewer"} {
		if err := e.store.SetMember(e.ctx, e.sc, id, role); err != nil {
			t.Fatal(err)
		}
	}
	e.pages = wiki.NewService(e.store, rec)
	e.props = proposals.NewService(e.store, e.pages, rec)
	e.svc = NewService(e.store, e.mail, e.props, bytes.Repeat([]byte{7}, 32), "https://wiki.x.cl/", 10*time.Minute, 3*24*time.Hour,
		func() time.Time { return e.clock })
	e.save(t, "guia", 0)
	e.pass(t)
	p, err := e.store.PageBySlug(e.ctx, e.sc, "guia")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.Watch(e.ctx, repo.Scope{WsID: ws.ID, MaxRank: 1}, e.ben, p.ID, false); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) save(t *testing.T, slug string, base int) {
	t.Helper()
	if err := e.pages.Save(e.ctx, e.sc, e.ana, base, repo.PageWrite{Slug: slug, Title: "Guía", Markdown: "rev " + time.Now().String(), Lang: "es"}); err != nil {
		t.Fatal(err)
	}
}

func (e *env) pass(t *testing.T) {
	t.Helper()
	if err := e.svc.Pass(e.ctx); err != nil {
		t.Fatal(err)
	}
}

// TestEmailWindow: el correo sale pasada la ventana si el aviso sigue sin leer, una sola vez
// aunque la página siga cambiando (§12.1), con la baja en un clic y sin contenido de la página.
func TestEmailWindow(t *testing.T) {
	e := newEnv(t, time.Now())
	e.save(t, "guia", 1)
	e.pass(t)
	if len(e.mail.sent) != 0 {
		t.Fatalf("mail sent before the window: %+v", e.mail.sent)
	}
	e.clock = time.Now().Add(11 * time.Minute)
	e.pass(t)
	if len(e.mail.sent) != 1 {
		t.Fatalf("want one mail after the window, got %d", len(e.mail.sent))
	}
	m := e.mail.sent[0]
	if m.To != "ben@x.cl" || m.Template != "page_changed" || m.Vars["title"] != "Guía" || m.Vars["actor"] != "Ana" ||
		m.URL != "https://wiki.x.cl/w/ti/p/guia" || !strings.HasPrefix(m.Unsubscribe, "https://wiki.x.cl/unsubscribe?t="+e.ben+".watched.") {
		t.Fatalf("mail: %+v", m)
	}
	for _, v := range m.Vars {
		if strings.Contains(v, "rev ") {
			t.Fatalf("the mail carries page content: %+v", m.Vars)
		}
	}
	// Más cambios con el aviso sin leer: se agrupan y no hay otro correo.
	e.save(t, "guia", 2)
	e.pass(t)
	if len(e.mail.sent) != 1 {
		t.Fatalf("a second mail for the same unread notice: %d", len(e.mail.sent))
	}
	// Leído antes de la ventana: no sale.
	if err := e.svc.PageOpened(e.ctx, e.ben, pageID(t, e, "guia")); err != nil {
		t.Fatal(err)
	}
	e.clock = time.Now()
	e.save(t, "guia", 3)
	e.pass(t)
	if err := e.svc.PageOpened(e.ctx, e.ben, pageID(t, e, "guia")); err != nil {
		t.Fatal(err)
	}
	e.clock = time.Now().Add(11 * time.Minute)
	e.pass(t)
	if len(e.mail.sent) != 1 {
		t.Fatalf("a notice read inside the window was mailed: %d", len(e.mail.sent))
	}
}

func pageID(t *testing.T, e *env, slug string) string {
	t.Helper()
	p, err := e.store.PageBySlug(e.ctx, e.sc, slug)
	if err != nil {
		t.Fatal(err)
	}
	return p.ID
}

// TestEmailPrefsAndNoSMTP: "no" no manda nada; sin correo configurado, solo hay bandeja (§12).
func TestEmailPrefsAndNoSMTP(t *testing.T) {
	e := newEnv(t, time.Now())
	if err := e.store.SetEmailPref(e.ctx, e.ben, "watched", "off"); err != nil {
		t.Fatal(err)
	}
	e.save(t, "guia", 1)
	e.clock = time.Now().Add(11 * time.Minute)
	e.pass(t)
	if len(e.mail.sent) != 0 {
		t.Fatalf("mail sent with the preference off: %+v", e.mail.sent)
	}
	if err := e.store.SetEmailPref(e.ctx, e.ben, "watched", "immediate"); err != nil {
		t.Fatal(err)
	}
	e.svc.Read(e.ctx, e.ben, nil)
	e.mail.enabled = false
	e.save(t, "guia", 2)
	e.clock = time.Now().Add(11 * time.Minute)
	e.pass(t)
	if len(e.mail.sent) != 0 {
		t.Fatalf("mail queued without SMTP: %+v", e.mail.sent)
	}
	if ns, unread, _ := e.svc.List(e.ctx, e.ben); unread != 1 || len(ns) == 0 {
		t.Fatalf("the inbox must still have the notice: %d %+v", unread, ns)
	}
}

// TestDigest: quien eligió el resumen diario recibe un solo correo a las 8 con todos sus
// avisos, y la baja del resumen apaga todas las categorías.
func TestDigest(t *testing.T) {
	now := time.Now()
	morning := time.Date(now.Year(), now.Month(), now.Day(), 7, 0, 0, 0, now.Location())
	e := newEnv(t, morning)
	if err := e.store.SetEmailPref(e.ctx, e.ben, "watched", "daily"); err != nil {
		t.Fatal(err)
	}
	e.save(t, "guia", 1)
	e.pass(t)
	e.clock = morning.Add(30 * time.Minute)
	e.pass(t)
	if len(e.mail.sent) != 0 {
		t.Fatalf("digest before 8: %+v", e.mail.sent)
	}
	e.clock = morning.Add(90 * time.Minute)
	e.pass(t)
	if len(e.mail.sent) != 1 || e.mail.sent[0].Template != "digest" || len(e.mail.sent[0].Lines) != 1 ||
		e.mail.sent[0].Lines[0].Template != "page_changed" || e.mail.sent[0].Vars["count"] != "1" {
		t.Fatalf("digest: %+v", e.mail.sent)
	}
	e.pass(t)
	if len(e.mail.sent) != 1 {
		t.Fatalf("a second digest the same day: %d", len(e.mail.sent))
	}

	// La baja del resumen: el enlace firmado apaga todo; uno adulterado no hace nada.
	u, _ := url.Parse(e.mail.sent[0].Unsubscribe)
	token := u.Query().Get("t")
	forged := strings.Replace(token, e.ben, e.ana, 1)
	if err := e.svc.Unsubscribe(e.ctx, forged); err != repo.ErrNotFound {
		t.Fatalf("forged token: %v", err)
	}
	if err := e.svc.Unsubscribe(e.ctx, token); err != nil {
		t.Fatal(err)
	}
	p, _ := e.svc.Prefs(e.ctx, e.ben)
	for _, c := range repo.NotificationCategories {
		if p.Email[c] != "off" {
			t.Fatalf("after unsubscribing from the digest %s is %q", c, p.Email[c])
		}
	}
	if a, _ := e.svc.Prefs(e.ctx, e.ana); a.Email["watched"] != "immediate" {
		t.Fatalf("someone else's preferences changed: %+v", a)
	}
}

// TestReminder: una propuesta pendiente hace más de approval.remind_after_days se le recuerda
// a los admins una sola vez (§5.3), y reenviarla vuelve a armar el recordatorio.
func TestReminder(t *testing.T) {
	e := newEnv(t, time.Now())
	// Un lector no propone páginas nuevas: la propone Ben como editor.
	if err := e.store.SetMember(e.ctx, e.sc, e.ben, "editor"); err != nil {
		t.Fatal(err)
	}
	id, err := e.props.Create(e.ctx, repo.Scope{WsID: e.sc.WsID, MaxRank: 2}, e.ben, proposals.Draft{Slug: "guia/nueva", Title: "Nueva", Markdown: "x"})
	if err != nil {
		t.Fatal(err)
	}
	e.pass(t)
	e.svc.Read(e.ctx, e.ana, nil)
	e.pass(t)
	if _, unread, _ := e.svc.List(e.ctx, e.ana); unread != 0 {
		t.Fatalf("reminder before the deadline: %d", unread)
	}
	e.clock = time.Now().Add(73 * time.Hour)
	e.pass(t)
	ns, unread, _ := e.svc.List(e.ctx, e.ana)
	if unread != 1 || ns[0].Kind != "proposal_reminder" || !strings.Contains(ns[0].Payload, `"days":3`) {
		t.Fatalf("reminder: %d %+v", unread, ns)
	}
	e.svc.Read(e.ctx, e.ana, nil)
	e.pass(t)
	if _, unread, _ := e.svc.List(e.ctx, e.ana); unread != 0 {
		t.Fatalf("the reminder went out twice: %d", unread)
	}
	e.clock = time.Now()
	if err := e.props.Reject(e.ctx, e.sc, e.ana, id, "no"); err != nil {
		t.Fatal(err)
	}
	if err := e.props.Resubmit(e.ctx, repo.Scope{WsID: e.sc.WsID, MaxRank: 2}, e.ben, id); err != nil {
		t.Fatal(err)
	}
	e.pass(t)
	e.svc.Read(e.ctx, e.ana, nil)
	e.clock = time.Now().Add(73 * time.Hour)
	e.pass(t)
	if ns, _, _ := e.svc.List(e.ctx, e.ana); ns[0].Kind != "proposal_reminder" || ns[0].Read {
		t.Fatalf("resubmitting did not re-arm the reminder: %+v", ns[0])
	}
}
