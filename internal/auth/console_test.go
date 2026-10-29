package auth

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/repo"
)

// La consola corre en otro proceso que el servidor: aquí, otro Service sobre la misma BD.
func consoleFor(t *testing.T, r *repo.Repo, c *clock) *Service {
	t.Helper()
	s, err := NewService(r, audit.NewRecorder(r, c.now, slog.New(slog.DiscardHandler)), c.now, false)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func lastAudit(t *testing.T, r *repo.Repo) repo.AuditEntry {
	t.Helper()
	events, err := r.AuditEvents(context.Background(), repo.AuditFilter{Limit: 1})
	if err != nil || len(events) == 0 {
		t.Fatalf("audit: %v, %d events", err, len(events))
	}
	return events[0]
}

func TestConsoleUnlock(t *testing.T) {
	server, r, c := newService(t)
	ctx := context.Background()
	for range loginMaxAttempts {
		server.Login(ctx, "10.0.0.1", "ana@empresa.cl", "clave-equivocada")
	}
	if _, err := server.Login(ctx, "10.0.0.1", "ana@empresa.cl", goodPassword); !errors.As(err, new(RateLimitedError)) {
		t.Fatalf("locked: %v, want RateLimitedError", err)
	}

	c.t = c.t.Add(time.Minute)
	if err := consoleFor(t, r, c).UnlockLogin(ctx, "ANA@empresa.cl"); err != nil {
		t.Fatal(err)
	}
	if _, err := server.Login(ctx, "10.0.0.1", "ana@empresa.cl", goodPassword); err != nil {
		t.Errorf("after unlock: %v", err)
	}
	if e := lastAudit(t, r); e.Action != "auth.login" {
		t.Errorf("last event %q, want auth.login", e.Action)
	}

	// Un desbloqueo viejo no sirve contra un bloqueo nuevo.
	c.t = c.t.Add(time.Minute)
	for range loginMaxAttempts {
		server.Login(ctx, "10.0.0.1", "ana@empresa.cl", "clave-equivocada")
	}
	if _, err := server.Login(ctx, "10.0.0.1", "ana@empresa.cl", goodPassword); !errors.As(err, new(RateLimitedError)) {
		t.Errorf("new lockout after an old unlock: %v, want RateLimitedError", err)
	}

	if err := consoleFor(t, r, c).UnlockLogin(ctx, "nadie@empresa.cl"); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("unknown email: %v, want ErrNotFound", err)
	}
}

func TestConsoleSetPassword(t *testing.T) {
	server, r, c := newService(t)
	ctx := context.Background()
	token, err := server.Login(ctx, "10.0.0.1", "ana@empresa.cl", goodPassword)
	if err != nil {
		t.Fatal(err)
	}
	console := consoleFor(t, r, c)
	if err := console.SetPassword(ctx, "ana@empresa.cl", "1234567890"); !errors.Is(err, ErrPasswordCommon) {
		t.Errorf("common password: %v", err)
	}
	if err := console.SetPassword(ctx, "ana@empresa.cl", "otra-clave-de-emergencia"); err != nil {
		t.Fatal(err)
	}
	if _, err := server.Authenticate(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Errorf("old session: %v, want ErrNoSession", err)
	}
	if _, err := server.Login(ctx, "10.0.0.1", "ana@empresa.cl", goodPassword); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("old password: %v", err)
	}
	if _, err := server.Login(ctx, "10.0.0.1", "ana@empresa.cl", "otra-clave-de-emergencia"); err != nil {
		t.Errorf("new password: %v", err)
	}

	// A quien tenía una invitación pendiente se la anula: el enlace ya no fija otra contraseña.
	ana, _ := r.UserByEmail(ctx, "ana@empresa.cl")
	link, err := server.Invite(ctx, ana.ID, "beto@empresa.cl", "Beto")
	if err != nil {
		t.Fatal(err)
	}
	if err := console.SetPassword(ctx, "beto@empresa.cl", "clave-de-beto-por-consola"); err != nil {
		t.Fatal(err)
	}
	if err := server.AcceptInvite(ctx, link, "otra-clave-de-beto"); !errors.Is(err, ErrInvalidInvite) {
		t.Errorf("invite after set-password: %v, want ErrInvalidInvite", err)
	}
	if e := lastAudit(t, r); e.Action != "admin.cli_set_password" || e.ActorID != "" || !strings.Contains(e.Meta, `"system":"cli"`) {
		t.Errorf("audit = %+v, want admin.cli_set_password by the system cli", e)
	}
}

func TestConsoleCreateSuperadmin(t *testing.T) {
	server, r, c := newService(t)
	ctx := context.Background()
	console := consoleFor(t, r, c)
	if err := console.CreateEmergencySuperadmin(ctx, "ana@empresa.cl", "Ana", goodPassword); !errors.Is(err, ErrEmailTaken) {
		t.Errorf("existing email: %v, want ErrEmailTaken", err)
	}
	if err := console.CreateEmergencySuperadmin(ctx, "no es un correo", "X", goodPassword); !errors.Is(err, ErrInvalidUser) {
		t.Errorf("bad email: %v, want ErrInvalidUser", err)
	}
	if err := console.CreateEmergencySuperadmin(ctx, "emergencia@empresa.cl", "Emergencia", goodPassword); err != nil {
		t.Fatal(err)
	}
	token, err := server.Login(ctx, "10.0.0.1", "emergencia@empresa.cl", goodPassword)
	if err != nil {
		t.Fatal(err)
	}
	if u, _ := server.Authenticate(ctx, token); !u.IsGlobalAdmin {
		t.Error("emergency account is not superadmin")
	}
}

func TestConsoleRevokeSessions(t *testing.T) {
	server, r, c := newService(t)
	ctx := context.Background()
	if err := server.CreateSuperadmin(ctx, "beto@empresa.cl", "Beto", goodPassword); err != nil {
		t.Fatal(err)
	}
	ana, _ := server.Login(ctx, "10.0.0.1", "ana@empresa.cl", goodPassword)
	beto, _ := server.Login(ctx, "10.0.0.1", "beto@empresa.cl", goodPassword)
	console := consoleFor(t, r, c)

	if n, err := console.RevokeSessions(ctx, "beto@empresa.cl"); err != nil || n != 1 {
		t.Fatalf("revoke beto = %d, %v", n, err)
	}
	if _, err := server.Authenticate(ctx, beto); !errors.Is(err, ErrNoSession) {
		t.Errorf("beto: %v, want ErrNoSession", err)
	}
	if _, err := server.Authenticate(ctx, ana); err != nil {
		t.Errorf("ana should keep her session: %v", err)
	}
	if n, err := console.RevokeSessions(ctx, ""); err != nil || n != 1 {
		t.Fatalf("revoke all = %d, %v", n, err)
	}
	if _, err := server.Authenticate(ctx, ana); !errors.Is(err, ErrNoSession) {
		t.Errorf("ana after --all: %v, want ErrNoSession", err)
	}
	if e := lastAudit(t, r); e.Action != "admin.cli_revoke_sessions" || !strings.Contains(e.Meta, `"all":true`) {
		t.Errorf("audit = %+v", e)
	}
}
