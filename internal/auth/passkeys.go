package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// Passkeys (SPEC §8.1): segundo factor equivalente al TOTP y, para usuarios locales, login sin
// contraseña. Todas se crean descubribles y con verificación del usuario (huella, rostro o PIN):
// así la misma passkey sirve para las dos cosas y para la reautenticación (§8.2), y nunca vale
// solo tener el aparato en la mano.

const maxPasskeyName = 64

var (
	ErrPasskeysUnavailable = errors.New("passkeys_unavailable") // base_url sin https o con una IP
	ErrPasskeyInvalid      = errors.New("passkey_invalid")      // no verificó, venció o no es de nadie
	ErrInvalidPasskeyName  = errors.New("invalid_passkey_name")
)

// relyingParty arma la configuración WebAuthn desde base_url, o nil si ahí no puede funcionar: el
// navegador exige un contexto seguro (https, o localhost) y un RP ID que sea un dominio, no una IP.
func relyingParty(baseURL string) *webauthn.WebAuthn {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil
	}
	host := u.Hostname()
	if net.ParseIP(host) != nil || (u.Scheme != "https" && host != "localhost") {
		return nil
	}
	// RPDisplayName solo se ve al registrar, y ahí va el título del sitio (§15.1).
	w, err := webauthn.New(&webauthn.Config{RPID: host, RPDisplayName: "Kudawiki", RPOrigins: []string{u.Scheme + "://" + u.Host}})
	if err != nil {
		return nil
	}
	return w
}

// PasskeysAvailable dice si esta instalación puede usar passkeys (§8.1).
func (s *Service) PasskeysAvailable() bool { return s.rp != nil }

// PasskeyInfo es lo que ve la persona en su perfil: nunca la clave.
type PasskeyInfo struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

func infoOf(p repo.Passkey) PasskeyInfo {
	i := PasskeyInfo{ID: p.ID, Name: p.Name, CreatedAt: p.CreatedAt}
	if !p.LastUsedAt.IsZero() {
		i.LastUsedAt = &p.LastUsedAt
	}
	return i
}

func (s *Service) Passkeys(ctx context.Context, u repo.User) ([]PasskeyInfo, error) {
	ps, err := s.store.PasskeysOf(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	out := make([]PasskeyInfo, len(ps))
	for i, p := range ps {
		out[i] = infoOf(p)
	}
	return out, nil
}

// passkeyUser es la persona como la ve la librería. El user handle es el id: el login sin
// contraseña lo devuelve y con él se encuentra a la persona sin preguntar su email.
type passkeyUser struct {
	u     repo.User
	creds []webauthn.Credential
}

func (p passkeyUser) WebAuthnID() []byte                         { return []byte(p.u.ID) }
func (p passkeyUser) WebAuthnName() string                       { return p.u.Email }
func (p passkeyUser) WebAuthnDisplayName() string                { return p.u.FullName }
func (p passkeyUser) WebAuthnCredentials() []webauthn.Credential { return p.creds }

// loadPasskeyUser junta la persona con sus passkeys de este RP ID: las registradas con otro
// base_url no pueden verificar aquí.
func (s *Service) loadPasskeyUser(ctx context.Context, u repo.User) (passkeyUser, error) {
	ps, err := s.store.PasskeysOf(ctx, u.ID)
	if err != nil {
		return passkeyUser{}, err
	}
	pu := passkeyUser{u: u}
	for _, p := range ps {
		if p.RPID != s.rp.Config.RPID {
			continue
		}
		id, err := base64.RawURLEncoding.DecodeString(p.ID)
		if err != nil {
			return passkeyUser{}, err
		}
		pu.creds = append(pu.creds, webauthn.Credential{ID: id, PublicKey: p.PublicKey,
			Flags:         webauthn.CredentialFlags{UserPresent: true, UserVerified: true, BackupEligible: p.BackupEligible, BackupState: p.BackupState},
			Authenticator: webauthn.Authenticator{SignCount: p.SignCount}})
	}
	return pu, nil
}

// ceremony es un registro o una verificación empezada: el desafío que el navegador tiene que
// firmar. Vive en memoria, como el segundo paso del login.
type ceremony struct {
	data    webauthn.SessionData
	expires time.Time
}

func (s *Service) putCeremony(key string, data *webauthn.SessionData) {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, c := range s.ceremonies {
		if now.After(c.expires) {
			delete(s.ceremonies, k)
		}
	}
	s.ceremonies[key] = ceremony{data: *data, expires: now.Add(mfaChallengeTTL)}
}

// takeCeremony la saca: cada desafío se usa una sola vez, salga bien o mal.
func (s *Service) takeCeremony(key string) (webauthn.SessionData, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.ceremonies[key]
	delete(s.ceremonies, key)
	if !ok || s.now().After(c.expires) {
		return webauthn.SessionData{}, false
	}
	return c.data, true
}

// Las ceremonias de una sesión van por el hash de su token: otra sesión de la misma persona,
// quizá robada, no puede terminar lo que esta empezó.
func registerKey(sessionToken string) string { return "register|" + hashToken(sessionToken) }
func reauthKey(sessionToken string) string   { return "reauth|" + hashToken(sessionToken) }
func loginKey(token string) string           { return "login|" + hashToken(token) }

var passkeyVerification = webauthn.WithUserVerification(protocol.VerificationRequired)

// BeginPasskey empieza a registrar una passkey. rpName es el título del sitio (§15.1): lo que el
// sistema muestra al guardarla. Las que la persona ya tiene se excluyen: el mismo aparato no se
// registra dos veces.
func (s *Service) BeginPasskey(ctx context.Context, u repo.User, sessionToken, rpName string) (json.RawMessage, error) {
	if s.rp == nil {
		return nil, ErrPasskeysUnavailable
	}
	pu, err := s.loadPasskeyUser(ctx, u)
	if err != nil {
		return nil, err
	}
	opts, data, err := s.rp.BeginRegistration(pu,
		webauthn.WithRegistrationRelyingPartyName(rpName),
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			ResidentKey: protocol.ResidentKeyRequirementRequired, RequireResidentKey: protocol.ResidentKeyRequired(),
			UserVerification: protocol.VerificationRequired}),
		webauthn.WithExclusions(webauthn.Credentials(pu.creds).CredentialDescriptors()))
	if err != nil {
		return nil, err
	}
	s.putCeremony(registerKey(sessionToken), data)
	return json.Marshal(opts)
}

// FinishPasskey guarda la passkey que el navegador acaba de crear, con el nombre que le dio la
// persona ("Notebook", "YubiKey").
func (s *Service) FinishPasskey(ctx context.Context, u repo.User, sessionToken, name string, credential []byte) (PasskeyInfo, error) {
	if s.rp == nil {
		return PasskeyInfo{}, ErrPasskeysUnavailable
	}
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxPasskeyName {
		return PasskeyInfo{}, ErrInvalidPasskeyName
	}
	data, ok := s.takeCeremony(registerKey(sessionToken))
	if !ok {
		return PasskeyInfo{}, ErrPasskeyInvalid
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(credential)
	if err != nil {
		return PasskeyInfo{}, ErrPasskeyInvalid
	}
	pu, err := s.loadPasskeyUser(ctx, u)
	if err != nil {
		return PasskeyInfo{}, err
	}
	cred, err := s.rp.CreateCredential(pu, data, parsed)
	if err != nil {
		return PasskeyInfo{}, ErrPasskeyInvalid
	}
	p := repo.Passkey{ID: base64.RawURLEncoding.EncodeToString(cred.ID), UserID: u.ID, Name: name, PublicKey: cred.PublicKey,
		SignCount: cred.Authenticator.SignCount, BackupEligible: cred.Flags.BackupEligible, BackupState: cred.Flags.BackupState,
		RPID: s.rp.Config.RPID, CreatedAt: s.now()}
	if err := s.store.CreatePasskey(ctx, p); errors.Is(err, repo.ErrConflict) {
		return PasskeyInfo{}, ErrPasskeyInvalid // la credencial ya está registrada
	} else if err != nil {
		return PasskeyInfo{}, err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.AuthPasskeyAdd, TargetType: "user", TargetID: u.ID, Meta: map[string]any{"name": name}})
	return infoOf(p), nil
}

// DeletePasskey la quita. A quien se le exige el 2FA no se le deja sin ningún factor (§8.1).
func (s *Service) DeletePasskey(ctx context.Context, u repo.User, id string) error {
	ps, err := s.store.PasskeysOf(ctx, u.ID)
	if err != nil {
		return err
	}
	var name string
	for _, p := range ps {
		if p.ID == id {
			name = p.Name
		}
	}
	if name == "" {
		return repo.ErrNotFound
	}
	if !u.TOTPEnabled && len(ps) == 1 {
		required, err := s.MFARequired(ctx, u)
		if err != nil {
			return err
		}
		if required {
			return ErrTOTPRequired
		}
	}
	if err := s.store.DeletePasskey(ctx, u.ID, id); err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.AuthPasskeyRemove, TargetType: "user", TargetID: u.ID, Meta: map[string]any{"name": name}})
	return nil
}

// BeginPasskeyLogin empieza el login con passkey: sin email, el navegador ofrece las passkeys que
// tiene para este sitio. Devuelve las opciones y el token que identifica la ceremonia (va en una
// cookie).
func (s *Service) BeginPasskeyLogin(ctx context.Context) (json.RawMessage, string, error) {
	if s.rp == nil {
		return nil, "", ErrPasskeysUnavailable
	}
	opts, data, err := s.rp.BeginDiscoverableLogin(passkeyVerification)
	if err != nil {
		return nil, "", err
	}
	token, err := newToken()
	if err != nil {
		return nil, "", err
	}
	s.putCeremony(loginKey(token), data)
	raw, err := json.Marshal(opts)
	return raw, token, err
}

// FinishPasskeyLogin verifica la passkey y abre la sesión de su dueño. Sirve sola (login sin
// contraseña) y como segundo paso tras la contraseña; challenge es ese segundo paso, si lo hay.
// Sin contraseña solo entran las cuentas locales: una de LDAP (F6) tiene que pasar antes por el
// directorio, que es quien sabe si sigue activa.
func (s *Service) FinishPasskeyLogin(ctx context.Context, ip, token, challenge string, credential []byte) (string, error) {
	if s.rp == nil {
		return "", ErrPasskeysUnavailable
	}
	key := "passkey|" + ip
	if _, err := s.allow(key); err != nil {
		return "", err
	}
	data, ok := s.takeCeremony(loginKey(token))
	if !ok {
		return "", ErrPasskeyInvalid
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(credential)
	if err != nil {
		return "", ErrPasskeyInvalid
	}
	var owner repo.User
	find := func(_, userHandle []byte) (webauthn.User, error) {
		u, err := s.store.UserByID(ctx, string(userHandle))
		if err != nil {
			return nil, err
		}
		owner = u
		return s.loadPasskeyUser(ctx, u)
	}
	_, cred, err := s.rp.ValidatePasskeyLogin(find, data, parsed)
	if err != nil || cred.Authenticator.CloneWarning || !owner.IsActive {
		return "", ErrPasskeyInvalid
	}
	h := hashToken(challenge)
	s.mu.Lock()
	c, stepped := s.challenges[h]
	stepped = stepped && c.userID == owner.ID
	if stepped {
		delete(s.challenges, h)
	}
	s.mu.Unlock()
	if owner.PasswordHash == "" && !stepped {
		return "", ErrPasskeyInvalid
	}
	if err := s.usedPasskey(ctx, cred); err != nil {
		return "", err
	}
	s.forget(key)
	return s.startSession(ctx, owner, map[string]any{"method": "passkey"})
}

// usedPasskey guarda el contador de firmas: si una copia de la passkey firma con un contador
// viejo, la librería lo marca (CloneWarning) y no se acepta.
func (s *Service) usedPasskey(ctx context.Context, cred *webauthn.Credential) error {
	return s.store.UsePasskey(ctx, base64.RawURLEncoding.EncodeToString(cred.ID), cred.Authenticator.SignCount,
		cred.Flags.BackupState, s.now())
}

// BeginPasskeyReauth empieza la reautenticación con passkey (§8.2), en lugar de contraseña y código.
func (s *Service) BeginPasskeyReauth(ctx context.Context, u repo.User, sessionToken string) (json.RawMessage, error) {
	if s.rp == nil {
		return nil, ErrPasskeysUnavailable
	}
	pu, err := s.loadPasskeyUser(ctx, u)
	if err != nil {
		return nil, err
	}
	if len(pu.creds) == 0 {
		return nil, ErrPasskeyInvalid
	}
	opts, data, err := s.rp.BeginLogin(pu, passkeyVerification)
	if err != nil {
		return nil, err
	}
	s.putCeremony(reauthKey(sessionToken), data)
	return json.Marshal(opts)
}

// FinishPasskeyReauth la termina. Comparte el contador de intentos con la reautenticación por
// contraseña: una sesión robada no suma oportunidades por cambiar de método.
func (s *Service) FinishPasskeyReauth(ctx context.Context, u repo.User, sessionToken string, credential []byte) error {
	if s.rp == nil {
		return ErrPasskeysUnavailable
	}
	key := "totp|" + u.ID
	if _, err := s.allow(key); err != nil {
		return err
	}
	data, ok := s.takeCeremony(reauthKey(sessionToken))
	if !ok {
		return ErrPasskeyInvalid
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(credential)
	if err != nil {
		return ErrPasskeyInvalid
	}
	pu, err := s.loadPasskeyUser(ctx, u)
	if err != nil {
		return err
	}
	cred, err := s.rp.ValidateLogin(pu, data, parsed)
	if err != nil || cred.Authenticator.CloneWarning {
		return ErrPasskeyInvalid
	}
	if err := s.usedPasskey(ctx, cred); err != nil {
		return err
	}
	s.forget(key)
	return s.markReauth(ctx, u, sessionToken, "passkey")
}
