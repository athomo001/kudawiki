package ldap

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"slices"
	"strings"
	"testing"

	"github.com/athomo001/kudawiki/internal/ldap/ldaptest"
	"github.com/athomo001/kudawiki/internal/mail"
	"github.com/athomo001/kudawiki/internal/offboarding"
	"github.com/athomo001/kudawiki/internal/repo"
)

// leaversSpy anota a quién se le inició la baja; la baja de verdad se prueba en offboarding y en
// TestLDAPSync de httpapi.
type leaversSpy struct{ started []string }

func (l *leaversSpy) Start(_ context.Context, actorID, userID string, _ map[string]string, _ bool) (offboarding.Outcome, error) {
	if actorID != "" {
		return offboarding.Outcome{}, errors.New("the directory starts the offboarding without an actor")
	}
	l.started = append(l.started, userID)
	return offboarding.Outcome{}, nil
}

type mailSpy struct{ sent []mail.Message }

func (m *mailSpy) Enabled(context.Context) (bool, error) { return true, nil }
func (m *mailSpy) Enqueue(_ context.Context, msg mail.Message) error {
	m.sent = append(m.sent, msg)
	return nil
}

func syncFixture(t *testing.T) (fixture, *ldaptest.Server, *leaversSpy, *mailSpy) {
	t.Helper()
	f, d := loginFixture(t)
	l, m := &leaversSpy{}, &mailSpy{}
	f.svc.SetOffboarding(l)
	f.svc.SetMail(m, "https://wiki.acme.cl/")
	return f, d, l, m
}

func (f fixture) actions(t *testing.T) []string {
	t.Helper()
	ev, err := f.repo.AuditEvents(context.Background(), repo.AuditFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(ev))
	for i, e := range ev {
		out[i] = e.Action
	}
	return out
}

// §8.3.3: sin reglas no hay nada que sincronizar, y con la conexión apagada tampoco.
func TestSyncWithoutRulesOrConnection(t *testing.T) {
	f := newFixture(t, Patch{})
	ctx := context.Background()
	if _, err := f.svc.Sync(ctx, SyncOptions{Trigger: "manual"}); !errors.Is(err, ErrSyncDisabled) {
		t.Errorf("disabled = %v", err)
	}
	d := directory(t, ldaptest.Options{LDAPS: true})
	p := form(d, d.CAFile(t))
	p.Enabled = new(true)
	if _, _, err := f.svc.Update(ctx, "super", p, false); err != nil {
		t.Fatal(err)
	}
	r, err := f.svc.Sync(ctx, SyncOptions{Trigger: "manual"})
	if err != nil || r.Groups != 0 || r.People != 0 || r.Aborted != "" {
		t.Errorf("no rules = %+v, %v", r, err)
	}
	if slices.Contains(f.actions(t), "ldap.sync_start") {
		t.Error("a sync without rules was recorded as started")
	}
}

// §8.3.3: crea de antemano a quien está en un grupo con regla, con su foto, y la segunda vez no
// cambia nada. Sin precreate_users solo se pone al día a quien ya tiene cuenta.
func TestSyncPrecreates(t *testing.T) {
	f, d := loginFixture(t)
	ctx := context.Background()
	var img bytes.Buffer
	if err := png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	d.Set(anaDN, "thumbnailPhoto", img.String())

	if _, _, err := f.svc.Update(ctx, "super", Patch{PrecreateUsers: new(false)}, false); err != nil {
		t.Fatal(err)
	}
	r, err := f.svc.Sync(ctx, SyncOptions{Trigger: "manual"})
	if err != nil || r.People != 1 || r.Created != 0 {
		t.Fatalf("without precreate = %+v, %v", r, err)
	}
	if _, err := f.repo.UserByEmail(ctx, "ana@acme.cl"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("an account was created without precreate_users: %v", err)
	}

	if _, _, err := f.svc.Update(ctx, "super", Patch{PrecreateUsers: new(true)}, false); err != nil {
		t.Fatal(err)
	}
	r, err = f.svc.Sync(ctx, SyncOptions{Trigger: "periodic"})
	if err != nil || r.Created != 1 || r.Added != 2 || r.Removed != 0 {
		t.Fatalf("sync = %+v, %v", r, err)
	}
	u, err := f.repo.UserByEmail(ctx, "ana@acme.cl")
	if err != nil || u.AuthSource != "ldap" || u.LDAPGUID != anaGUID || u.FullName != "Ana Pérez" {
		t.Fatalf("ana = %+v, %v", u, err)
	}
	if f.source(t, "ti", u.ID).LDAPRole != "editor" || f.source(t, "fin", u.ID).LDAPRole != "viewer" {
		t.Error("memberships were not granted")
	}
	if a, err := f.repo.Avatar(ctx, u.ID); err != nil || len(a) == 0 {
		t.Errorf("photo = %d bytes, %v", len(a), err)
	}
	if ev := f.lastEvent(t); ev.Action != "ldap.sync_done" || ev.ActorID != "" || !strings.Contains(ev.Meta, `"system":"ldap"`) {
		t.Errorf("last event = %s %q %s", ev.Action, ev.ActorID, ev.Meta)
	}

	// La segunda vez no hay nada que hacer; el nombre que cambió en el directorio se actualiza.
	d.Set(anaDN, "displayName", "Ana Pérez Soto")
	r, err = f.svc.Sync(ctx, SyncOptions{Trigger: "periodic"})
	if err != nil || r.Created != 0 || r.Added != 0 || r.Changed != 0 || r.Updated != 1 {
		t.Errorf("second sync = %+v, %v", r, err)
	}
	if u, _ := f.repo.UserByEmail(ctx, "ana@acme.cl"); u.FullName != "Ana Pérez Soto" {
		t.Errorf("name = %q", u.FullName)
	}
}

// §8.3.4: un grupo con regla que vuelve vacío detiene la sincronización sin aplicar nada y avisa
// a los superadmins; forzada, da de baja (§8.4) a quien quedó sin grupo, con el motivo.
func TestSyncSafeguards(t *testing.T) {
	f, d, l, m := syncFixture(t)
	ctx := context.Background()
	if _, err := f.svc.Sync(ctx, SyncOptions{Trigger: "periodic"}); err != nil {
		t.Fatal(err)
	}
	ana, err := f.repo.UserByEmail(ctx, "ana@acme.cl")
	if err != nil {
		t.Fatal(err)
	}
	// Beto tenía cuenta del directorio y ahora está deshabilitado; una cuenta cuya identidad ya
	// no existe se borró del directorio.
	for _, u := range []repo.User{
		{ID: "beto", Email: "beto@acme.cl", FullName: "Beto", AuthSource: "ldap", LDAPGUID: guidOf("0123456789abcdef"), IsActive: true, Locale: "es"},
		{ID: "ida", Email: "ida@acme.cl", FullName: "Ida", AuthSource: "ldap", LDAPGUID: guidOf("ida-guid-0000000"), IsActive: true, Locale: "es"},
	} {
		if err := f.repo.CreateLDAPUser(ctx, u, "CN="+u.FullName+","+usersDN); err != nil {
			t.Fatal(err)
		}
	}

	// Ana sale de TI-Redes: los dos grupos con regla vuelven vacíos.
	d.Set(anaDN, "memberOf")
	r, err := f.svc.Sync(ctx, SyncOptions{Trigger: "periodic"})
	if err != nil || r.Aborted != "empty_group" || r.Group == "" {
		t.Fatalf("aborted = %+v, %v", r, err)
	}
	if len(l.started) != 0 || f.source(t, "ti", ana.ID).LDAPRole != "editor" {
		t.Fatal("an aborted sync applied changes")
	}
	if len(m.sent) != 1 || m.sent[0].To != "super@acme.cl" || m.sent[0].Template != "ldap_sync_aborted" || m.sent[0].URL != "https://wiki.acme.cl/admin/ldap" {
		t.Errorf("notice = %+v", m.sent)
	}
	if ev := f.lastEvent(t); ev.Action != "ldap.sync_aborted" {
		t.Errorf("last event = %s", ev.Action)
	}
	if st := f.svc.Status(ctx, true); st.LastSync == nil || st.LastSync.Aborted != "empty_group" {
		t.Errorf("status = %+v", st.LastSync)
	}

	// Ver qué haría, forzando, no avisa ni aplica.
	r, err = f.svc.Sync(ctx, SyncOptions{ActorID: "super", Trigger: "manual", DryRun: true, Force: true})
	reasons := map[string]string{}
	for _, lv := range r.Deactivated {
		reasons[lv.UserID] = lv.Reason
	}
	if err != nil || r.Aborted != "" || reasons[ana.ID] != "no_mapped_group" || reasons["beto"] != "disabled" || reasons["ida"] != "not_found" {
		t.Fatalf("forced preview = %+v (%v), %v", r, reasons, err)
	}
	if len(l.started) != 0 || len(m.sent) != 1 {
		t.Fatal("the preview applied or notified")
	}

	if _, err := f.svc.Sync(ctx, SyncOptions{ActorID: "super", Trigger: "manual", Force: true}); err != nil {
		t.Fatal(err)
	}
	slices.Sort(l.started)
	if want := []string{ana.ID, "beto", "ida"}; !slices.Equal(l.started, slices.Sorted(slices.Values(want))) {
		t.Errorf("offboarded = %v, want %v", l.started, want)
	}
	n := 0
	for _, a := range f.actions(t) {
		if a == "user.deactivated_by_ldap" {
			n++
		}
	}
	if n != 3 {
		t.Errorf("%d deactivation events", n)
	}
}

// §8.6.5: el log de depuración dura 30 minutos y queda en la auditoría; el estado no lo muestra
// apagado.
func TestDebugAndStatus(t *testing.T) {
	f, _ := loginFixture(t)
	ctx := context.Background()
	if st := f.svc.Status(ctx, true); !st.Enabled || st.DebugUntil != nil || st.LastSync != nil {
		t.Errorf("status = %+v", st)
	}
	until := f.svc.SetDebug(ctx, true)
	if st := f.svc.Status(ctx, false); st.DebugUntil == nil || !st.DebugUntil.Equal(until) || until.Sub(f.svc.now()) > debugFor {
		t.Errorf("debug on = %+v", st.DebugUntil)
	}
	if ev := f.lastEvent(t); ev.Action != "ldap.config_update" {
		t.Errorf("event = %s", ev.Action)
	}
	if _, err := f.svc.Login(ctx, "ana", "clave-ana"); err != nil {
		t.Fatal(err)
	}
	f.svc.SetDebug(ctx, false)
	if st := f.svc.Status(ctx, false); st.DebugUntil != nil || st.LastLogin == nil || st.CertDays == nil {
		t.Errorf("status = %+v", st)
	}
}
