package auth

import (
	"bytes"
	"context"
	"encoding/base32"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/oklog/ulid/v2"
)

// Vectores de RFC 6238, apéndice B (SHA-1), con los 6 últimos dígitos.
func TestTOTPCodeRFC6238(t *testing.T) {
	secret := []byte("12345678901234567890")
	for unix, want := range map[int64]string{59: "287082", 1111111109: "081804", 1234567890: "005924", 2000000000: "279037"} {
		if got := totpCode(secret, unix/totpPeriod); got != want {
			t.Errorf("totpCode(T=%d) = %s, want %s", unix, got, want)
		}
	}
}

// totpService es newService con la llave del 2FA y una segunda persona sin rol de admin.
func totpService(t *testing.T) (*Service, *repo.Repo, *clock) {
	t.Helper()
	s, r, c := newService(t)
	s.SetSecondFactor(SecondFactor{Key: bytes.Repeat([]byte{7}, 32)})
	hash, _ := HashPassword(goodPassword)
	if err := r.CreateUser(context.Background(), repo.User{ID: ulid.Make().String(), Email: "beto@empresa.cl", FullName: "Beto",
		PasswordHash: hash, IsActive: true, Locale: "es"}); err != nil {
		t.Fatal(err)
	}
	return s, r, c
}

// enroll configura el 2FA de una persona y devuelve su secreto y sus códigos de recuperación.
func enroll(t *testing.T, s *Service, r *repo.Repo, email string) ([]byte, []string) {
	t.Helper()
	ctx := context.Background()
	u, _ := r.UserByEmail(ctx, email)
	setup, err := s.BeginTOTP(ctx, u, "Wiki ACME")
	if err != nil {
		t.Fatal(err)
	}
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(setup.Secret)
	if err != nil {
		t.Fatal(err)
	}
	codes, err := s.ConfirmTOTP(ctx, u, "", totpCode(secret, s.now().Unix()/totpPeriod), false)
	if err != nil {
		t.Fatal(err)
	}
	return secret, codes
}

func TestTOTPEnrollAndLogin(t *testing.T) {
	ctx := context.Background()
	s, r, c := totpService(t)
	ana, _ := r.UserByEmail(ctx, "ana@empresa.cl")

	// Sin 2FA, el superadmin entra, pero solo para configurarlo (§8.1).
	if need, _ := s.NeedsMFASetup(ctx, ana); !need {
		t.Fatal("superadmin without TOTP must set it up")
	}
	setup, err := s.BeginTOTP(ctx, ana, "Wiki ACME")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(setup.URI, "otpauth://totp/Wiki%20ACME:ana%40empresa.cl?secret=") || !strings.Contains(setup.URI, "&issuer=Wiki%20ACME") {
		t.Errorf("uri = %s", setup.URI)
	}
	if len(setup.QR) < 21 || len(setup.QR[0]) != len(setup.QR) {
		t.Errorf("qr is %d rows", len(setup.QR))
	}
	secret, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(setup.Secret)
	if _, err := s.ConfirmTOTP(ctx, ana, "", "000000", false); !errors.Is(err, ErrInvalidCode) && totpCode(secret, c.t.Unix()/totpPeriod) != "000000" {
		t.Errorf("confirm with a wrong code: %v", err)
	}
	code := totpCode(secret, c.t.Unix()/totpPeriod)
	codes, err := s.ConfirmTOTP(ctx, ana, "", code, false)
	if err != nil || len(codes) != 10 {
		t.Fatalf("confirm: %d codes, %v", len(codes), err)
	}
	ana, _ = r.UserByEmail(ctx, "ana@empresa.cl")
	if _, err := s.BeginTOTP(ctx, ana, "Wiki ACME"); !errors.Is(err, ErrTOTPEnabled) {
		t.Errorf("begin with TOTP on: %v", err)
	}

	// Con 2FA, la contraseña no abre sesión: da el desafío del segundo paso.
	res, err := s.Login(ctx, "10.0.0.1", "ana@empresa.cl", goodPassword)
	if err != nil || res.Session != "" || res.Challenge == "" {
		t.Fatalf("login with TOTP: %+v %v", res, err)
	}
	// El código con que se confirmó ya se usó: no vale otra vez (RFC 6238 §5.2).
	if _, err := s.VerifyLogin(ctx, res.Challenge, code); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("replayed code: %v", err)
	}
	c.t = c.t.Add(30 * time.Second)
	token, err := s.VerifyLogin(ctx, res.Challenge, totpCode(secret, c.t.Unix()/totpPeriod))
	if err != nil {
		t.Fatal(err)
	}
	if u, err := s.Authenticate(ctx, token); err != nil || u.Email != "ana@empresa.cl" {
		t.Fatalf("session after TOTP: %v", err)
	}
	// El desafío es de un solo uso.
	if _, err := s.VerifyLogin(ctx, res.Challenge, totpCode(secret, c.t.Unix()/totpPeriod+1)); !errors.Is(err, ErrMFAExpired) {
		t.Errorf("challenge reused: %v", err)
	}

	// Un código de recuperación sirve una vez, escrito como sea.
	res, _ = s.Login(ctx, "10.0.0.1", "ana@empresa.cl", goodPassword)
	if _, err := s.VerifyLogin(ctx, res.Challenge, " "+strings.ToUpper(codes[3])+" "); err != nil {
		t.Fatalf("recovery code: %v", err)
	}
	res, _ = s.Login(ctx, "10.0.0.1", "ana@empresa.cl", goodPassword)
	if _, err := s.VerifyLogin(ctx, res.Challenge, codes[3]); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("recovery code reused: %v", err)
	}
	if st, _ := s.TOTPStatus(ctx, ana); !st.Enabled || !st.Required || st.RecoveryCodesLeft != 9 {
		t.Errorf("status = %+v", st)
	}

	// El desafío vence a los 5 minutos.
	c.t = c.t.Add(6 * time.Minute)
	if _, err := s.VerifyLogin(ctx, res.Challenge, totpCode(secret, c.t.Unix()/totpPeriod)); !errors.Is(err, ErrMFAExpired) {
		t.Errorf("expired challenge: %v", err)
	}
}

// Los intentos del segundo paso se cuentan por persona: con la contraseña en la mano, abrir
// desafíos nuevos o cambiar de IP no da más oportunidades.
func TestTOTPAttemptsPerPerson(t *testing.T) {
	ctx := context.Background()
	s, r, c := totpService(t)
	enroll(t, s, r, "beto@empresa.cl")
	for i := range loginMaxAttempts {
		res, _ := s.Login(ctx, "10.0.0."+string(rune('1'+i%9)), "beto@empresa.cl", goodPassword)
		s.VerifyLogin(ctx, res.Challenge, "bad-code-bad-code")
	}
	res, _ := s.Login(ctx, "10.9.9.9", "beto@empresa.cl", goodPassword)
	var limited RateLimitedError
	if _, err := s.VerifyLogin(ctx, res.Challenge, "bad-code-bad-code"); !errors.As(err, &limited) {
		t.Fatalf("11th attempt: %v, want rate limited", err)
	}
	c.t = c.t.Add(16 * time.Minute)
	if _, err := s.VerifyLogin(ctx, res.Challenge, "x"); !errors.Is(err, ErrMFAExpired) {
		t.Errorf("after the window the old challenge is gone: %v", err)
	}
}

func TestTOTPDisableAndReset(t *testing.T) {
	ctx := context.Background()
	s, r, c := totpService(t)
	secret, _ := enroll(t, s, r, "beto@empresa.cl")
	enroll(t, s, r, "ana@empresa.cl")
	beto, _ := r.UserByEmail(ctx, "beto@empresa.cl")
	ana, _ := r.UserByEmail(ctx, "ana@empresa.cl")

	// A quien se le exige no puede apagarlo.
	if err := s.DisableTOTP(ctx, ana, "", goodPassword, "x", false); !errors.Is(err, ErrTOTPRequired) {
		t.Errorf("superadmin disables: %v", err)
	}
	c.t = c.t.Add(30 * time.Second)
	code := totpCode(secret, c.t.Unix()/totpPeriod)
	if err := s.DisableTOTP(ctx, beto, "", "otra-clave", code, false); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("wrong password: %v", err)
	}
	if err := s.DisableTOTP(ctx, beto, "", goodPassword, code, false); err != nil {
		t.Fatal(err)
	}
	if res, _ := s.Login(ctx, "10.0.0.1", "beto@empresa.cl", goodPassword); res.Session == "" {
		t.Error("without TOTP the password opens the session")
	}

	// El restablecimiento del superadmin y el de la consola lo borran; el login vuelve a pedirlo.
	if err := s.ResetTOTP(ctx, ana.ID); err != nil {
		t.Fatal(err)
	}
	ana, _ = r.UserByEmail(ctx, "ana@empresa.cl")
	if need, _ := s.NeedsMFASetup(ctx, ana); !need {
		t.Error("after a reset the superadmin sets it up again")
	}
	enroll(t, s, r, "ana@empresa.cl")
	if err := s.Reset2FA(ctx, "ANA@empresa.cl"); err != nil {
		t.Fatal(err)
	}
	if left, _ := r.RecoveryCodesLeft(ctx, ana.ID); left != 0 {
		t.Errorf("recovery codes after reset-2fa: %d", left)
	}
}

// Con enforce_for = all, también a quien no es admin.
func TestTOTPEnforceAll(t *testing.T) {
	ctx := context.Background()
	s, r, _ := totpService(t)
	beto, _ := r.UserByEmail(ctx, "beto@empresa.cl")
	if need, _ := s.NeedsMFASetup(ctx, beto); need {
		t.Error("admins policy: a reader does not need it")
	}
	s.SetSecondFactor(SecondFactor{Key: bytes.Repeat([]byte{7}, 32), Enforce: MFAAll})
	if need, _ := s.NeedsMFASetup(ctx, beto); !need {
		t.Error("all policy: everybody needs it")
	}
}
