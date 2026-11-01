package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

// §8.2: la reautenticación pide contraseña y código, vale 10 minutos y es de una sola sesión.
func TestReauth(t *testing.T) {
	ctx := context.Background()
	s, r, c := totpService(t)
	secret, codes := enroll(t, s, r, "ana@empresa.cl")
	c.t = c.t.Add(time.Minute) // el código del alta ya se gastó
	login := func() string {
		t.Helper()
		res, err := s.Login(ctx, "10.0.0.1", "ana@empresa.cl", goodPassword)
		if err != nil {
			t.Fatal(err)
		}
		token, err := s.VerifyLogin(ctx, res.Challenge, codes[len(codes)-1])
		if err != nil {
			t.Fatal(err)
		}
		codes = codes[:len(codes)-1]
		return token
	}
	token, other := login(), login()
	ana, _ := r.UserByEmail(ctx, "ana@empresa.cl")
	reauthed := func(tok string) bool {
		t.Helper()
		ok, err := s.Reauthenticated(ctx, tok)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if reauthed(token) {
		t.Fatal("a new session is already reauthenticated")
	}

	code := totpCode(secret, c.t.Unix()/totpPeriod)
	if err := s.Reauth(ctx, ana, token, "otra-clave-mala", code); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("wrong password: %v", err)
	}
	if err := s.Reauth(ctx, ana, token, goodPassword, ""); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("without the code: %v", err)
	}
	if reauthed(token) {
		t.Fatal("a failed reauth counts")
	}
	if err := s.Reauth(ctx, ana, token, goodPassword, code); err != nil {
		t.Fatal(err)
	}
	if !reauthed(token) || reauthed(other) {
		t.Errorf("reauth: this session %v, the other %v; want only this one", reauthed(token), reauthed(other))
	}
	// El mismo código no vale dos veces (RFC 6238 §5.2), tampoco para reautenticarse.
	if err := s.Reauth(ctx, ana, other, goodPassword, code); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("reused code: %v", err)
	}
	c.t = c.t.Add(reauthTTL - time.Second)
	if !reauthed(token) {
		t.Error("reauth expired before 10 minutes")
	}
	c.t = c.t.Add(time.Second)
	if reauthed(token) {
		t.Error("reauth still valid after 10 minutes")
	}
	if reauthed("no-es-una-sesion") {
		t.Error("an unknown token is reauthenticated")
	}

	// Comparte el contador de intentos del segundo paso: diez fallos y bloquea también el login.
	for range loginMaxAttempts {
		s.Reauth(ctx, ana, token, "otra-clave-mala", "")
	}
	if err := s.Reauth(ctx, ana, token, goodPassword, totpCode(secret, c.t.Unix()/totpPeriod)); !errors.As(err, new(RateLimitedError)) {
		t.Errorf("after 10 failures: %v", err)
	}
	res, _ := s.Login(ctx, "10.0.0.2", "ana@empresa.cl", goodPassword)
	if _, err := s.VerifyLogin(ctx, res.Challenge, codes[0]); !errors.As(err, new(RateLimitedError)) {
		t.Errorf("second login step after 10 reauth failures: %v", err)
	}
}

// Sin 2FA basta la contraseña (a quien no se le exige, §8.1).
func TestReauthWithoutTOTP(t *testing.T) {
	ctx := context.Background()
	s, r, _ := totpService(t)
	res, err := s.Login(ctx, "10.0.0.1", "beto@empresa.cl", goodPassword)
	if err != nil {
		t.Fatal(err)
	}
	beto, _ := r.UserByEmail(ctx, "beto@empresa.cl")
	if err := s.Reauth(ctx, beto, res.Session, goodPassword, ""); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Reauthenticated(ctx, res.Session); !ok {
		t.Error("password-only reauth did not count")
	}
}
