package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/athomo001/kudawiki/internal/mail"
	"github.com/athomo001/kudawiki/internal/repo"
)

// fakeMail es la cola de correos: guarda lo encolado.
type fakeMail struct {
	on   bool
	sent []mail.Message
}

func (f *fakeMail) Enabled(context.Context) (bool, error) { return f.on, nil }
func (f *fakeMail) Enqueue(_ context.Context, m mail.Message) error {
	f.sent = append(f.sent, m)
	return nil
}
func (f *fakeMail) of(template string) []mail.Message {
	var out []mail.Message
	for _, m := range f.sent {
		if m.Template == template {
			out = append(out, m)
		}
	}
	return out
}

func withMail(t *testing.T) (*Service, *repo.Repo, *clock, *fakeMail) {
	t.Helper()
	s, r, c := newService(t)
	m := &fakeMail{on: true}
	s.SetEmail(Email{Mail: m, BaseURL: "https://wiki.acme.cl/", PasswordReset: true})
	s.async = func(f func()) { f() } // sincrónico: el test mira el resultado enseguida
	return s, r, c, m
}

func tokenOf(t *testing.T, m mail.Message, prefix string) string {
	t.Helper()
	tok, ok := strings.CutPrefix(m.URL, "https://wiki.acme.cl/"+prefix+"#")
	if !ok || tok == "" {
		t.Fatalf("link = %q", m.URL)
	}
	return tok
}

func TestPasswordReset(t *testing.T) {
	s, r, c, m := withMail(t)
	ctx := context.Background()
	if err := s.CreateEmergencySuperadmin(ctx, "beto@empresa.cl", "Beto", goodPassword); err != nil {
		t.Fatal(err)
	}
	session, err := s.Login(ctx, "10.0.0.1", "ana@empresa.cl", goodPassword)
	if err != nil {
		t.Fatal(err)
	}

	// Un correo que no tiene cuenta: misma respuesta, ningún correo.
	if err := s.RequestPasswordReset(ctx, "10.0.0.9", "nadie@empresa.cl"); err != nil || len(m.sent) != 0 {
		t.Fatalf("unknown email: %v, %d emails", err, len(m.sent))
	}
	if err := s.RequestPasswordReset(ctx, "10.0.0.9", " ana@empresa.cl "); err != nil {
		t.Fatal(err)
	}
	first := m.of("password_reset")
	if len(first) != 1 || first[0].To != "ana@empresa.cl" || first[0].Vars["ip"] != "10.0.0.9" || first[0].Vars["minutes"] != "30" || first[0].Vars["name"] != "Ana" {
		t.Fatalf("reset email = %+v", first)
	}
	old := tokenOf(t, first[0], "reset")
	// Pedir otro anula el anterior.
	s.RequestPasswordReset(ctx, "10.0.0.9", "ana@empresa.cl")
	tok := tokenOf(t, m.of("password_reset")[1], "reset")
	if err := s.CompletePasswordReset(ctx, old, "otra-clave-del-sur-77"); !errors.Is(err, ErrInvalidReset) {
		t.Errorf("old link: %v", err)
	}
	// La actual no sirve como nueva, y el enlace sigue vigente después de ese error.
	if err := s.CompletePasswordReset(ctx, tok, goodPassword); !errors.Is(err, ErrSamePassword) {
		t.Errorf("same password: %v", err)
	}
	if err := s.CompletePasswordReset(ctx, tok, "otra-clave-del-sur-77"); err != nil {
		t.Fatal(err)
	}
	if err := s.CompletePasswordReset(ctx, tok, "tercera-clave-del-sur-88"); !errors.Is(err, ErrInvalidReset) {
		t.Errorf("a used link works again: %v", err)
	}
	// Todas las sesiones se cierran; se entra con la nueva.
	if _, err := s.Authenticate(ctx, session); !errors.Is(err, ErrNoSession) {
		t.Errorf("old session still open: %v", err)
	}
	if _, err := s.Login(ctx, "10.0.0.1", "ana@empresa.cl", "otra-clave-del-sur-77"); err != nil {
		t.Errorf("login with the new password: %v", err)
	}
	// Avisos: a Ana, con el enlace para escribirle al otro superadmin, y a Beto.
	changed := m.of("password_changed")
	if len(changed) != 1 || changed[0].To != "ana@empresa.cl" || changed[0].URL != "mailto:beto@empresa.cl" {
		t.Errorf("password changed notice = %+v", changed)
	}
	if n := m.of("superadmin_password_reset"); len(n) != 1 || n[0].To != "beto@empresa.cl" || n[0].Vars["email"] != "ana@empresa.cl" {
		t.Errorf("superadmin notice = %+v", n)
	}
	ev, _ := r.AuditEvents(ctx, repo.AuditFilter{Action: "auth.password_reset_complete", Limit: 50})
	req, _ := r.AuditEvents(ctx, repo.AuditFilter{Action: "auth.password_reset_request", Limit: 50})
	if len(ev) != 1 || len(req) != 2 {
		t.Errorf("audit: %d complete, %d requests (the unknown email must not be there)", len(ev), len(req))
	}

	// Un enlace vencido no sirve.
	s.RequestPasswordReset(ctx, "10.0.0.8", "beto@empresa.cl")
	late := tokenOf(t, m.of("password_reset")[2], "reset")
	c.t = c.t.Add(31 * time.Minute)
	if err := s.CompletePasswordReset(ctx, late, "clave-de-beto-nueva-1"); !errors.Is(err, ErrInvalidReset) {
		t.Errorf("expired link: %v", err)
	}
}

// Límites (§8.5.1): 3 por correo y 10 por IP cada hora; pasado el tope la respuesta es la misma.
func TestPasswordResetLimits(t *testing.T) {
	s, _, c, m := withMail(t)
	ctx := context.Background()
	for i := range 5 {
		if err := s.RequestPasswordReset(ctx, fmt.Sprintf("10.0.0.%d", i), "ana@empresa.cl"); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(m.of("password_reset")); n != 3 {
		t.Errorf("per email: %d emails, want 3", n)
	}
	c.t = c.t.Add(time.Hour)
	for range 10 {
		s.RequestPasswordReset(ctx, "10.9.9.9", "nadie@empresa.cl")
	}
	s.RequestPasswordReset(ctx, "10.9.9.9", "ana@empresa.cl")
	if n := len(m.of("password_reset")); n != 3 {
		t.Errorf("per IP: %d emails, want still 3", n)
	}
	s.RequestPasswordReset(ctx, "10.9.9.8", "ana@empresa.cl")
	if n := len(m.of("password_reset")); n != 4 {
		t.Errorf("another IP after the window: %d emails, want 4", n)
	}
}

func TestPasswordResetUnavailable(t *testing.T) {
	s, _, _ := newService(t)
	ctx := context.Background()
	if s.ResetAvailable(ctx) || !errors.Is(s.RequestPasswordReset(ctx, "ip", "ana@empresa.cl"), ErrResetUnavailable) {
		t.Error("available without email")
	}
	s.SetEmail(Email{Mail: &fakeMail{on: true}, PasswordReset: false})
	if s.ResetAvailable(ctx) {
		t.Error("available with auth.local.password_reset: false")
	}
}

// Con correo la invitación sale por la cola; sin correo vuelve el enlace manual (§8.5.3).
func TestInviteByEmail(t *testing.T) {
	s, r, _, m := withMail(t)
	ctx := context.Background()
	ana, _ := r.UserByEmail(ctx, "ana@empresa.cl")
	tok, err := s.Invite(ctx, ana.ID, "carla@empresa.cl", "Carla")
	if err != nil || tok != "" {
		t.Fatalf("invite with email: %q, %v", tok, err)
	}
	inv := m.of("invite")
	if len(inv) != 1 || inv[0].To != "carla@empresa.cl" || inv[0].Vars["inviter"] != "Ana" || inv[0].Vars["days"] != "7" {
		t.Fatalf("invite email = %+v", inv)
	}
	if err := s.AcceptInvite(ctx, tokenOf(t, inv[0], "invite"), "clave-de-carla-del-sur"); err != nil {
		t.Errorf("accept the emailed invite: %v", err)
	}

	m.on = false
	tok, err = s.Invite(ctx, ana.ID, "dani@empresa.cl", "Dani")
	if err != nil || tok == "" || len(m.of("invite")) != 1 {
		t.Errorf("invite without email: %q, %v", tok, err)
	}
	ev, _ := r.AuditEvents(ctx, repo.AuditFilter{Action: "user.invite", Limit: 50})
	if len(ev) != 2 || !strings.Contains(ev[0].Meta, `"manual"`) || !strings.Contains(ev[1].Meta, `"email"`) {
		t.Errorf("invite audit = %+v", ev)
	}
}

func TestAdminPasswordReset(t *testing.T) {
	s, r, _, m := withMail(t)
	ctx := context.Background()
	ana, _ := r.UserByEmail(ctx, "ana@empresa.cl")
	s.CreateEmergencySuperadmin(ctx, "beto@empresa.cl", "Beto", goodPassword)
	beto, _ := r.UserByEmail(ctx, "beto@empresa.cl")

	if tok, err := s.AdminPasswordReset(ctx, ana.ID, "10.1.1.1", beto.ID, true); err != nil || tok != "" || len(m.of("password_reset")) != 1 {
		t.Errorf("by email: %q, %v", tok, err)
	}
	tok, err := s.AdminPasswordReset(ctx, ana.ID, "10.1.1.1", beto.ID, false)
	if err != nil || tok == "" {
		t.Fatalf("manual: %q, %v", tok, err)
	}
	if err := s.CompletePasswordReset(ctx, tok, "clave-de-beto-nueva-1"); err != nil {
		t.Errorf("the manual link works: %v", err)
	}
	m.on = false
	if _, err := s.AdminPasswordReset(ctx, ana.ID, "ip", beto.ID, true); !errors.Is(err, mail.ErrNotConfigured) {
		t.Errorf("by email without SMTP: %v", err)
	}
	s.Invite(ctx, ana.ID, "carla@empresa.cl", "Carla")
	carla, _ := r.UserByEmail(ctx, "carla@empresa.cl")
	if _, err := s.AdminPasswordReset(ctx, ana.ID, "ip", carla.ID, false); !errors.Is(err, ErrNoLocalPassword) {
		t.Errorf("invited person: %v", err)
	}
}

// El cambio desde el perfil también avisa "Tu contraseña cambió" (§8.5.2).
func TestChangePasswordNotice(t *testing.T) {
	s, r, _, m := withMail(t)
	ctx := context.Background()
	ana, _ := r.UserByEmail(ctx, "ana@empresa.cl")
	tok, _ := s.Login(ctx, "ip", "ana@empresa.cl", goodPassword)
	if err := s.ChangePassword(ctx, ana.ID, tok, goodPassword, "otra-clave-del-sur-77", true); err != nil {
		t.Fatal(err)
	}
	if n := m.of("password_changed"); len(n) != 1 || n[0].To != "ana@empresa.cl" || n[0].URL != "" {
		t.Errorf("notice = %+v (no other superadmin: no report link)", n)
	}
}
