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
	s, err := NewService(r, audit.NewRecorder(r, c.now, slog.New(slog.DiscardHandler)), c.now)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSuperadmin(context.Background(), "ana@empresa.cl", "Ana", goodPassword); err != nil {
		t.Fatal(err)
	}
	return s, r, c
}

// session deja usar Login donde solo importa la sesión (personas sin 2FA).
func session(r LoginResult, err error) (string, error) { return r.Session, err }

func TestCheckPolicy(t *testing.T) {
	for pw, want := range map[string]error{
		"corta":         ErrPasswordTooShort,
		"1234567890":    ErrPasswordCommon,
		"QWERTYUIOP":    ErrPasswordCommon, // la lista se compara sin mayúsculas
		goodPassword:    nil,
		"ñandú-ñandú-1": nil,
	} {
		if got := CheckPolicy(pw, MinPasswordLength); !errors.Is(got, want) {
			t.Errorf("CheckPolicy(%q) = %v, want %v", pw, got, want)
		}
	}
	// Un mínimo menor que el piso no se aplica (QA-06): no hay excepción para desarrollo.
	if err := CheckPolicy("1234", 0); !errors.Is(err, ErrPasswordTooShort) {
		t.Errorf("below floor: %v", err)
	}
}

// QA-06: el superadmin sube el mínimo desde el panel; nunca lo baja del piso.
func TestPasswordMin(t *testing.T) {
	s, r, _ := newService(t)
	ctx := context.Background()
	ana, _ := r.UserByEmail(ctx, "ana@empresa.cl")
	if n, err := s.PasswordMin(ctx); err != nil || n != MinPasswordLength {
		t.Fatalf("default = %d, %v", n, err)
	}
	for _, n := range []int{MinPasswordLength - 1, 0, MaxPasswordMin + 1} {
		if err := s.SetPasswordMin(ctx, ana.ID, n); !errors.Is(err, ErrInvalidPasswordMin) {
			t.Errorf("SetPasswordMin(%d) = %v", n, err)
		}
	}
	if err := s.SetPasswordMin(ctx, ana.ID, 16); err != nil {
		t.Fatal(err)
	}
	// 14 caracteres ya no alcanzan para una contraseña nueva; la que existe sigue entrando.
	if err := s.SetPassword(ctx, "ana@empresa.cl", "catorce-letras"); !errors.Is(err, ErrPasswordTooShort) {
		t.Errorf("14 chars with min 16: %v", err)
	}
	if _, err := session(s.Login(ctx, "10.0.0.1", "ana@empresa.cl", goodPassword)); err != nil {
		t.Errorf("existing password: %v", err)
	}
	// Una fila editada a mano por debajo del piso no lo baja.
	if err := r.SetSiteSettings(ctx, map[string]*string{passwordMinKey: new("4")}, ana.ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.PasswordMin(ctx); n != MinPasswordLength {
		t.Errorf("tampered min = %d", n)
	}
}

func TestPasswordRoundTrip(t *testing.T) {
	h, err := HashPassword(context.Background(), goodPassword)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := VerifyPassword(context.Background(), h, goodPassword); !ok {
		t.Error("correct password rejected")
	}
	if ok, _ := VerifyPassword(context.Background(), h, goodPassword+"x"); ok {
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

	token, err := session(s.Login(ctx, "10.0.0.1", "ANA@empresa.cl", goodPassword)) // el email no distingue mayúsculas
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
	token, _ = session(s.Login(ctx, "10.0.0.1", "ana@empresa.cl", goodPassword))
	c.t = c.t.Add(sessionIdle + time.Minute)
	if _, err := s.Authenticate(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Errorf("idle: %v, want ErrNoSession", err)
	}

	// Check no cuenta como actividad: una sesión que solo se revisa vence igual (§4.11).
	token, _ = session(s.Login(ctx, "10.0.0.1", "ana@empresa.cl", goodPassword))
	for range 3 {
		c.t = c.t.Add(sessionIdle / 2)
		if _, err := s.Check(ctx, token); err != nil {
			break
		}
	}
	if _, err := s.Check(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Errorf("checked only: %v, want ErrNoSession", err)
	}

	// Aunque haya actividad, a los 7 días vence igual.
	token, _ = session(s.Login(ctx, "10.0.0.1", "ana@empresa.cl", goodPassword))
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

// QA-04 (§8.2): cada 3 fallos, 1 minuto de espera; desde el 9.º, 5 minutos. El intento que
// bloquea ya devuelve la espera, para que la pantalla la muestre sin gastar otro intento.
func TestLoginRateLimit(t *testing.T) {
	s, _, c := newService(t)
	ctx := context.Background()
	wrong := func() error {
		_, err := s.Login(ctx, "10.0.0.1", "ana@empresa.cl", "clave-equivocada")
		return err
	}
	for range 2 {
		if err := wrong(); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("before the 3rd: %v", err)
		}
	}
	var limited RateLimitedError
	if err := wrong(); !errors.As(err, &limited) || limited.RetryAfter != time.Minute {
		t.Fatalf("3rd: %v, want 1 minute", err)
	}
	// Bloqueado, ni la contraseña correcta entra, y la respuesta dice cuánto falta.
	c.t = c.t.Add(20 * time.Second)
	if _, err := s.Login(ctx, "10.0.0.1", "ana@empresa.cl", goodPassword); !errors.As(err, &limited) || limited.RetryAfter != 40*time.Second {
		t.Fatalf("while locked: %v, want 40s", err)
	}
	// Otra IP no queda bloqueada por la primera.
	if _, err := s.Login(ctx, "10.0.0.2", "ana@empresa.cl", goodPassword); err != nil {
		t.Errorf("other IP: %v", err)
	}
	c.t = c.t.Add(40 * time.Second)
	// El 6.º deja otro minuto; el 9.º y los que siguen, cinco.
	for i, want := range []time.Duration{time.Minute, 5 * time.Minute, 5 * time.Minute} {
		wrong()
		wrong()
		if err := wrong(); !errors.As(err, &limited) || limited.RetryAfter != want {
			t.Fatalf("attempt %d: %v, want %v", 6+3*i, err, want)
		}
		c.t = c.t.Add(limited.RetryAfter)
	}
	// Un acierto borra la serie.
	if _, err := session(s.Login(ctx, "10.0.0.1", "ana@empresa.cl", goodPassword)); err != nil {
		t.Fatalf("after the wait: %v", err)
	}
	wrong()
	wrong()
	// Quince minutos sin intentos también: el que sigue es el 1.º de una serie nueva.
	c.t = c.t.Add(loginForgetIdle)
	if err := wrong(); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("after 15 idle minutes: %v", err)
	}
}
