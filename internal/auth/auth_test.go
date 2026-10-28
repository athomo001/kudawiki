package auth

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/database/dbtest"
	"github.com/athomo001/kudawiki/internal/repo"
)

const goodPassword = "lluvia-sobre-temuco-42"

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newService(t *testing.T) (*Service, *repo.Repo, *clock) {
	t.Helper()
	r := repo.New(dbtest.Open(t))
	c := &clock{t: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	s, err := NewService(r, audit.NewRecorder(r, c.now, slog.New(slog.DiscardHandler)), c.now, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSuperadmin(context.Background(), "ana@empresa.cl", "Ana", goodPassword); err != nil {
		t.Fatal(err)
	}
	return s, r, c
}

func TestCheckPolicy(t *testing.T) {
	for pw, want := range map[string]error{
		"corta":         ErrPasswordTooShort,
		"1234567890":    ErrPasswordCommon,
		"QWERTYUIOP":    ErrPasswordCommon, // la lista se compara sin mayúsculas
		goodPassword:    nil,
		"ñandú-ñandú-1": nil,
	} {
		if got := CheckPolicy(pw, false); !errors.Is(got, want) {
			t.Errorf("CheckPolicy(%q) = %v, want %v", pw, got, want)
		}
	}
	// En desarrollo (base_url en localhost) basta con que no esté vacía.
	if err := CheckPolicy("1234", true); err != nil {
		t.Errorf("relaxed: %v", err)
	}
	if err := CheckPolicy("", true); !errors.Is(err, ErrPasswordTooShort) {
		t.Errorf("relaxed empty: %v", err)
	}
}

func TestPasswordRoundTrip(t *testing.T) {
	h, err := HashPassword(goodPassword)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := VerifyPassword(h, goodPassword); !ok {
		t.Error("correct password rejected")
	}
	if ok, _ := VerifyPassword(h, goodPassword+"x"); ok {
		t.Error("wrong password accepted")
	}
}

// Email inexistente y contraseña incorrecta deben ser indistinguibles para quien llama.
func TestLoginFailuresLookTheSame(t *testing.T) {
	s, _, _ := newService(t)
	ctx := context.Background()
	for _, tc := range []struct{ email, pw string }{
		{"ana@empresa.cl", "otra-clave-cualquiera"},
		{"nadie@empresa.cl", goodPassword},
	} {
		if _, err := s.Login(ctx, "10.0.0.1", tc.email, tc.pw); !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("Login(%s) = %v, want ErrInvalidCredentials", tc.email, err)
		}
	}
}

func TestSessionLifecycle(t *testing.T) {
	s, _, c := newService(t)
	ctx := context.Background()

	token, err := s.Login(ctx, "10.0.0.1", "ANA@empresa.cl", goodPassword) // el email no distingue mayúsculas
	if err != nil {
		t.Fatal(err)
	}
	if u, err := s.Authenticate(ctx, token); err != nil || u.Email != "ana@empresa.cl" || !u.IsGlobalAdmin {
		t.Fatalf("Authenticate = %+v, %v", u, err)
	}
	if err := s.Logout(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Errorf("after logout: %v, want ErrNoSession", err)
	}

	// Inactividad de más de 12 h vence la sesión (SPEC §8.2).
	token, _ = s.Login(ctx, "10.0.0.1", "ana@empresa.cl", goodPassword)
	c.t = c.t.Add(sessionIdle + time.Minute)
	if _, err := s.Authenticate(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Errorf("idle: %v, want ErrNoSession", err)
	}

	// Aunque haya actividad, a los 7 días vence igual.
	token, _ = s.Login(ctx, "10.0.0.1", "ana@empresa.cl", goodPassword)
	for range 15 {
		c.t = c.t.Add(sessionIdle - time.Minute)
		if _, err := s.Authenticate(ctx, token); err != nil {
			break
		}
	}
	if _, err := s.Authenticate(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Errorf("absolute: %v, want ErrNoSession", err)
	}
}

func TestLoginRateLimit(t *testing.T) {
	s, _, c := newService(t)
	ctx := context.Background()
	for range loginMaxAttempts {
		s.Login(ctx, "10.0.0.1", "ana@empresa.cl", "clave-equivocada")
	}
	var limited RateLimitedError
	if _, err := s.Login(ctx, "10.0.0.1", "ana@empresa.cl", goodPassword); !errors.As(err, &limited) {
		t.Fatalf("11th attempt: %v, want RateLimitedError", err)
	}
	// Otra IP no queda bloqueada por la primera.
	if _, err := s.Login(ctx, "10.0.0.2", "ana@empresa.cl", goodPassword); err != nil {
		t.Errorf("other IP: %v", err)
	}
	c.t = c.t.Add(loginWindow)
	if _, err := s.Login(ctx, "10.0.0.1", "ana@empresa.cl", goodPassword); err != nil {
		t.Errorf("after window: %v", err)
	}
}
