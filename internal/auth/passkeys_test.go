package auth

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/athomo001/kudawiki/internal/auth/passkeytest"
	"github.com/athomo001/kudawiki/internal/repo"
)

const passkeyOrigin = "https://wiki.empresa.cl"

// passkeyService es totpService con base_url en https: ahí hay passkeys.
func passkeyService(t *testing.T) (*Service, *repo.Repo, *clock) {
	t.Helper()
	s, r, c := totpService(t)
	s.SetSecondFactor(SecondFactor{Key: bytes.Repeat([]byte{7}, 32), Enforce: MFAAdmins, BaseURL: passkeyOrigin})
	return s, r, c
}

// addPasskey registra una passkey nueva desde la sesión sessionToken.
func addPasskey(t *testing.T, s *Service, u repo.User, sessionToken, name string) *passkeytest.Authenticator {
	t.Helper()
	ctx := context.Background()
	key := passkeytest.New(passkeyOrigin)
	opts, err := s.BeginPasskey(ctx, u, sessionToken, "Wiki ACME")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishPasskey(ctx, u, sessionToken, name, key.Create(opts)); err != nil {
		t.Fatal(err)
	}
	return key
}

// passwordless entra solo con la passkey; challenge es el segundo paso tras la contraseña, si lo hay.
func passwordless(s *Service, key *passkeytest.Authenticator, challenge string) (string, error) {
	ctx := context.Background()
	opts, token, err := s.BeginPasskeyLogin(ctx)
	if err != nil {
		return "", err
	}
	return s.FinishPasskeyLogin(ctx, "10.0.0.1", token, challenge, key.Get(opts))
}

// §8.1: dónde puede haber passkeys. El navegador exige https (o localhost) y un dominio.
func TestPasskeysAvailable(t *testing.T) {
	ctx := context.Background()
	s, r, _ := totpService(t)
	ana, _ := r.UserByEmail(ctx, "ana@empresa.cl")
	for base, want := range map[string]bool{
		"https://wiki.empresa.cl": true, "http://localhost:8080": true,
		"http://wiki.empresa.cl": false, "https://10.0.0.5": false, "http://127.0.0.1:8080": false, "https://[::1]:8443": false,
	} {
		s.SetSecondFactor(SecondFactor{Key: bytes.Repeat([]byte{7}, 32), BaseURL: base})
		if got := s.PasskeysAvailable(); got != want {
			t.Errorf("%s: available = %v, want %v", base, got, want)
		}
	}
	// El orden del map es al azar: la última base_url de la vuelta no sirve para esto.
	s.SetSecondFactor(SecondFactor{Key: bytes.Repeat([]byte{7}, 32), BaseURL: "http://wiki.empresa.cl"})
	if _, err := s.BeginPasskey(ctx, ana, "x", "Wiki"); !errors.Is(err, ErrPasskeysUnavailable) {
		t.Errorf("begin without passkeys: %v", err)
	}
}

func TestPasskeyRegister(t *testing.T) {
	ctx := context.Background()
	s, r, _ := passkeyService(t)
	ana, _ := r.UserByEmail(ctx, "ana@empresa.cl")

	opts, err := s.BeginPasskey(ctx, ana, "sesion-1", "Wiki ACME")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"name":"Wiki ACME"`, `"id":"wiki.empresa.cl"`, `"residentKey":"required"`, `"userVerification":"required"`} {
		if !strings.Contains(string(opts), want) {
			t.Errorf("options lack %s: %s", want, opts)
		}
	}
	key := passkeytest.New(passkeyOrigin)
	cred := key.Create(opts)
	// La ceremonia es de la sesión que la empezó: otra sesión de la misma persona no la termina.
	if _, err := s.FinishPasskey(ctx, ana, "sesion-2", "Notebook", cred); !errors.Is(err, ErrPasskeyInvalid) {
		t.Errorf("finish from another session: %v", err)
	}
	for _, name := range []string{"  ", strings.Repeat("ñ", maxPasskeyName+1)} {
		if _, err := s.FinishPasskey(ctx, ana, "sesion-1", name, cred); !errors.Is(err, ErrInvalidPasskeyName) {
			t.Errorf("name %q: %v", name, err)
		}
	}
	p, err := s.FinishPasskey(ctx, ana, "sesion-1", " Notebook ", cred)
	if err != nil || p.Name != "Notebook" || p.ID != key.ID() || p.LastUsedAt != nil {
		t.Fatalf("finish = %+v, %v", p, err)
	}
	// Cada desafío se usa una vez.
	if _, err := s.FinishPasskey(ctx, ana, "sesion-1", "Otra", cred); !errors.Is(err, ErrPasskeyInvalid) {
		t.Errorf("finish twice: %v", err)
	}
	// Sin verificar a la persona (huella, rostro o PIN) no se registra.
	opts, _ = s.BeginPasskey(ctx, ana, "sesion-1", "Wiki ACME")
	noUV := passkeytest.New(passkeyOrigin)
	noUV.SkipUV = true
	if _, err := s.FinishPasskey(ctx, ana, "sesion-1", "Llave", noUV.Create(opts)); !errors.Is(err, ErrPasskeyInvalid) {
		t.Errorf("without user verification: %v", err)
	}
	// Las que ya tiene se excluyen: el mismo aparato no se registra dos veces.
	opts, _ = s.BeginPasskey(ctx, ana, "sesion-1", "Wiki ACME")
	if !strings.Contains(string(opts), key.ID()) {
		t.Errorf("the registered passkey is not excluded: %s", opts)
	}

	// Una passkey cumple el 2FA obligatorio, igual que el TOTP (§8.1).
	ana, _ = r.UserByEmail(ctx, "ana@empresa.cl")
	if need, _ := s.NeedsMFASetup(ctx, ana); need || ana.Passkeys != 1 {
		t.Errorf("with a passkey: needs setup %v, passkeys %d", need, ana.Passkeys)
	}
	ev, _ := r.AuditEvents(ctx, repo.AuditFilter{Action: "auth.passkey_add", Limit: 5})
	if len(ev) != 1 || !strings.Contains(ev[0].Meta, `"name":"Notebook"`) {
		t.Errorf("audit = %+v", ev)
	}
}

func TestPasskeyLogin(t *testing.T) {
	ctx := context.Background()
	s, r, _ := passkeyService(t)
	ana, _ := r.UserByEmail(ctx, "ana@empresa.cl")
	key := addPasskey(t, s, ana, "sesion-1", "Notebook")

	// Sin contraseña: la passkey sola abre la sesión de su dueño.
	token, err := passwordless(s, key, "")
	if err != nil {
		t.Fatal(err)
	}
	if u, err := s.Authenticate(ctx, token); err != nil || u.ID != ana.ID {
		t.Fatalf("passwordless session: %v %v", u.Email, err)
	}
	if ps, _ := s.Passkeys(ctx, ana); len(ps) != 1 || ps[0].LastUsedAt == nil {
		t.Errorf("last use not recorded: %+v", ps)
	}
	ev, _ := r.AuditEvents(ctx, repo.AuditFilter{Action: "auth.login", Limit: 1})
	if len(ev) != 1 || !strings.Contains(ev[0].Meta, `"method":"passkey"`) {
		t.Errorf("login audit = %+v", ev)
	}

	// La misma firma no vale dos veces, ni con otro desafío.
	opts, ceremony, _ := s.BeginPasskeyLogin(ctx)
	cred := key.Get(opts)
	if _, err := s.FinishPasskeyLogin(ctx, "10.0.0.1", ceremony, "", cred); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishPasskeyLogin(ctx, "10.0.0.1", ceremony, "", cred); !errors.Is(err, ErrPasskeyInvalid) {
		t.Errorf("replay: %v", err)
	}
	_, other, _ := s.BeginPasskeyLogin(ctx)
	if _, err := s.FinishPasskeyLogin(ctx, "10.0.0.1", other, "", cred); !errors.Is(err, ErrPasskeyInvalid) {
		t.Errorf("signature for another challenge: %v", err)
	}

	// Sin verificar a la persona, desde otro sitio o con el contador hacia atrás (una copia), no.
	key.SkipUV = true
	if _, err := passwordless(s, key, ""); !errors.Is(err, ErrPasskeyInvalid) {
		t.Errorf("without user verification: %v", err)
	}
	key.SkipUV = false
	key.Origin = "https://wiki.empresa.cl.evil.example"
	if _, err := passwordless(s, key, ""); !errors.Is(err, ErrPasskeyInvalid) {
		t.Errorf("phishing origin: %v", err)
	}
	key.Origin = passkeyOrigin
	key.Count = 0
	if _, err := passwordless(s, key, ""); !errors.Is(err, ErrPasskeyInvalid) {
		t.Errorf("cloned counter: %v", err)
	}
	// Una passkey que nadie registró.
	if _, err := passwordless(s, passkeytest.New(passkeyOrigin), ""); !errors.Is(err, ErrPasskeyInvalid) {
		t.Errorf("unknown passkey: %v", err)
	}
}

// §8.1: tras la contraseña, el segundo paso acepta la passkey; el desafío queda gastado.
func TestPasskeySecondStep(t *testing.T) {
	ctx := context.Background()
	s, r, _ := passkeyService(t)
	ana, _ := r.UserByEmail(ctx, "ana@empresa.cl")
	key := addPasskey(t, s, ana, "sesion-1", "Notebook")

	res, err := s.Login(ctx, "10.0.0.1", "ana@empresa.cl", goodPassword)
	if err != nil || res.Session != "" || res.Challenge == "" || strings.Join(res.Methods, ",") != "passkey" {
		t.Fatalf("login with a passkey = %+v, %v", res, err)
	}
	if _, err := passwordless(s, key, res.Challenge); err != nil {
		t.Fatal(err)
	}
	if _, err := s.VerifyLogin(ctx, res.Challenge, "123456"); !errors.Is(err, ErrMFAExpired) {
		t.Errorf("the challenge is still alive: %v", err)
	}
	// Con TOTP y passkey, el segundo paso ofrece los dos.
	enroll(t, s, r, "ana@empresa.cl")
	if res, _ := s.Login(ctx, "10.0.0.1", "ana@empresa.cl", goodPassword); strings.Join(res.Methods, ",") != "totp,passkey" {
		t.Errorf("methods = %v", res.Methods)
	}
	// Si la instalación pierde las passkeys (base_url sin https), no se ofrecen.
	s.SetSecondFactor(SecondFactor{Key: bytes.Repeat([]byte{7}, 32), BaseURL: "http://10.0.0.5"})
	if res, _ := s.Login(ctx, "10.0.0.1", "ana@empresa.cl", goodPassword); strings.Join(res.Methods, ",") != "totp" {
		t.Errorf("methods without passkeys = %v", res.Methods)
	}
}

// §8.2: la reautenticación acepta una passkey en lugar de contraseña y código.
func TestPasskeyReauth(t *testing.T) {
	ctx := context.Background()
	s, r, _ := passkeyService(t)
	token, err := session(s.Login(ctx, "10.0.0.1", "beto@empresa.cl", goodPassword))
	if err != nil {
		t.Fatal(err)
	}
	beto, _ := r.UserByEmail(ctx, "beto@empresa.cl")
	if _, err := s.BeginPasskeyReauth(ctx, beto, token); !errors.Is(err, ErrPasskeyInvalid) {
		t.Errorf("reauth without passkeys: %v", err)
	}
	key := addPasskey(t, s, beto, token, "Teléfono")

	opts, err := s.BeginPasskeyReauth(ctx, beto, token)
	if err != nil || !strings.Contains(string(opts), key.ID()) {
		t.Fatalf("begin reauth = %s, %v", opts, err)
	}
	cred := key.Get(opts)
	if err := s.FinishPasskeyReauth(ctx, beto, "otra-sesion", cred); !errors.Is(err, ErrPasskeyInvalid) {
		t.Errorf("another session finishes it: %v", err)
	}
	if err := s.FinishPasskeyReauth(ctx, beto, token, cred); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Reauthenticated(ctx, token); !ok {
		t.Error("the session is not reauthenticated")
	}
	ev, _ := r.AuditEvents(ctx, repo.AuditFilter{Action: "auth.reauth", Limit: 1})
	if len(ev) != 1 || !strings.Contains(ev[0].Meta, `"second_factor":"passkey"`) {
		t.Errorf("reauth audit = %+v", ev)
	}
}

func TestPasskeyRemoveAndReset(t *testing.T) {
	ctx := context.Background()
	s, r, c := passkeyService(t)
	ana, _ := r.UserByEmail(ctx, "ana@empresa.cl")
	beto, _ := r.UserByEmail(ctx, "beto@empresa.cl")
	key := addPasskey(t, s, ana, "sesion-1", "Notebook")
	ana, _ = r.UserByEmail(ctx, "ana@empresa.cl")

	// A quien se le exige el 2FA no se le deja sin ningún factor.
	if err := s.DeletePasskey(ctx, ana, key.ID()); !errors.Is(err, ErrTOTPRequired) {
		t.Errorf("remove the last factor: %v", err)
	}
	// La de otra persona no existe.
	if err := s.DeletePasskey(ctx, beto, key.ID()); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("remove someone else's: %v", err)
	}
	// Con el TOTP al lado sí se puede quitar, y con una passkey al lado se puede apagar el TOTP.
	_, codes := enroll(t, s, r, "ana@empresa.cl")
	c.t = c.t.Add(time.Minute)
	ana, _ = r.UserByEmail(ctx, "ana@empresa.cl")
	if err := s.DisableTOTP(ctx, ana, "", goodPassword, codes[0], false); err != nil {
		t.Errorf("disable TOTP with a passkey left: %v", err)
	}
	enroll(t, s, r, "ana@empresa.cl")
	ana, _ = r.UserByEmail(ctx, "ana@empresa.cl")
	if err := s.DeletePasskey(ctx, ana, key.ID()); err != nil {
		t.Errorf("remove with TOTP left: %v", err)
	}
	if ev, _ := r.AuditEvents(ctx, repo.AuditFilter{Action: "auth.passkey_remove", Limit: 5}); len(ev) != 1 {
		t.Errorf("remove audit = %+v", ev)
	}

	// El restablecimiento del superadmin y el de la consola borran también las passkeys (§13.6).
	addPasskey(t, s, beto, "sesion-b", "Teléfono")
	if err := s.ResetTOTP(ctx, beto.ID); err != nil {
		t.Fatal(err)
	}
	addPasskey(t, s, ana, "sesion-1", "Notebook")
	if err := s.Reset2FA(ctx, "ana@empresa.cl"); err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{"ana@empresa.cl", "beto@empresa.cl"} {
		if u, _ := r.UserByEmail(ctx, email); u.Passkeys != 0 || u.TOTPEnabled {
			t.Errorf("%s after reset: passkeys %d, totp %v", email, u.Passkeys, u.TOTPEnabled)
		}
	}
}

// Cada cambio del 2FA avisa a la persona por correo (§12.1, no desactivable): activar o apagar
// el TOTP, sumar o quitar una passkey y el restablecimiento del superadmin.
func TestTwoFactorChangedNotice(t *testing.T) {
	ctx := context.Background()
	s, r, c := passkeyService(t)
	m := &fakeMail{on: true}
	s.SetEmail(Email{Mail: m, BaseURL: "https://wiki.acme.cl/"})
	notices := func() int {
		n := 0
		for _, msg := range m.of("two_factor_changed") {
			if msg.To != "beto@empresa.cl" || msg.URL != "mailto:ana@empresa.cl" || msg.Vars["name"] != "Beto" {
				t.Errorf("notice = %+v", msg)
			}
			n++
		}
		return n
	}
	secret, _ := enroll(t, s, r, "beto@empresa.cl")
	if n := notices(); n != 1 {
		t.Fatalf("after enabling TOTP: %d", n)
	}
	beto, _ := r.UserByEmail(ctx, "beto@empresa.cl")
	key := addPasskey(t, s, beto, "sesion-b", "Teléfono")
	beto, _ = r.UserByEmail(ctx, "beto@empresa.cl")
	if err := s.DeletePasskey(ctx, beto, key.ID()); err != nil {
		t.Fatal(err)
	}
	if n := notices(); n != 3 {
		t.Fatalf("after adding and removing a passkey: %d", n)
	}
	c.t = c.t.Add(time.Minute)
	if err := s.DisableTOTP(ctx, beto, "", goodPassword, totpCode(secret, c.t.Unix()/totpPeriod), false); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetTOTP(ctx, beto.ID); err != nil {
		t.Fatal(err)
	}
	if n := notices(); n != 5 {
		t.Errorf("after disabling and the superadmin's reset: %d", n)
	}
}
