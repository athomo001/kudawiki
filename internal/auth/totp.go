package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/crypto"
	"github.com/athomo001/kudawiki/internal/repo"
	"rsc.io/qr"
)

// Segundo factor TOTP (SPEC §8.1): RFC 6238 con HMAC-SHA1, 6 dígitos y pasos de 30 s, lo que
// leen Google Authenticator, Authy y Microsoft Authenticator.

const (
	totpPeriod      = 30
	totpSkew        = 1  // un paso antes y uno después: el reloj del celular puede estar corrido
	recoveryCount   = 10 // §8.1
	mfaChallengeTTL = 5 * time.Minute
)

var (
	ErrTOTPEnabled    = errors.New("totp_enabled")     // ya hay uno activo: un alta nueva no lo reemplaza
	ErrTOTPNotStarted = errors.New("totp_not_started") // confirmar sin haber empezado el alta
	ErrTOTPRequired   = errors.New("totp_required")    // obligatorio para esta persona: no se desactiva
	ErrInvalidCode    = errors.New("invalid_code")
	ErrMFAExpired     = errors.New("mfa_expired") // el segundo paso del login venció: se vuelve a empezar
)

// MFAPolicy es auth.totp.enforce_for (§8.1, §13.1): a quién se le exige el 2FA.
type MFAPolicy string

const (
	MFAAdmins MFAPolicy = "admins" // superadmin y admins de área; el resto, si quiere
	MFAAll    MFAPolicy = "all"
)

// SecondFactor es lo que el 2FA necesita del arranque. Sin la llave no se puede cifrar el secreto:
// la consola (`kudawiki admin`) no la carga y solo borra.
// BaseURL decide si hay passkeys: su host es el RP ID (§8.1).
type SecondFactor struct {
	Key     []byte
	Enforce MFAPolicy
	BaseURL string
}

func (s *Service) SetSecondFactor(f SecondFactor) {
	if f.Enforce == "" {
		f.Enforce = MFAAdmins
	}
	s.mfa = f
	s.rp = relyingParty(f.BaseURL)
}

// TOTPSetup es lo que ve la persona al empezar el alta: el secreto para escribirlo a mano y la
// URI otpauth:// como QR. QR va como filas de '0' y '1' que la interfaz dibuja en SVG, sin una
// imagen que la CSP tendría que permitir.
type TOTPSetup struct {
	Secret string   `json:"secret"`
	URI    string   `json:"uri"`
	QR     []string `json:"qr"`
}

// TOTPStatus es lo que muestran el perfil y /auth/me.
type TOTPStatus struct {
	Enabled           bool `json:"enabled"`
	Required          bool `json:"required"`
	RecoveryCodesLeft int  `json:"recovery_codes_left"`
}

// MFARequired dice si a esta persona se le exige el 2FA (§8.1). Se mira en cada pedido de quien no
// lo tiene: un rol admin recién dado, también por un grupo de AD (§8.3.4), lo exige desde ya.
func (s *Service) MFARequired(ctx context.Context, u repo.User) (bool, error) {
	if s.mfa.Enforce == MFAAll || u.IsGlobalAdmin {
		return true, nil
	}
	return s.store.IsAreaAdmin(ctx, u.ID)
}

// NeedsMFASetup: la persona tiene que configurar el 2FA antes de hacer cualquier otra cosa. Una
// passkey lo cumple igual que el TOTP (§8.1).
func (s *Service) NeedsMFASetup(ctx context.Context, u repo.User) (bool, error) {
	if u.TOTPEnabled || u.Passkeys > 0 {
		return false, nil
	}
	return s.MFARequired(ctx, u)
}

func (s *Service) TOTPStatus(ctx context.Context, u repo.User) (TOTPStatus, error) {
	required, err := s.MFARequired(ctx, u)
	if err != nil {
		return TOTPStatus{}, err
	}
	st := TOTPStatus{Enabled: u.TOTPEnabled, Required: required}
	if u.TOTPEnabled {
		st.RecoveryCodesLeft, err = s.store.RecoveryCodesLeft(ctx, u.ID)
	}
	return st, err
}

// BeginTOTP genera un secreto nuevo y lo deja pendiente hasta que la persona lo confirme con un
// código. issuer es el título del sitio (§15.1): así la app muestra "Wiki ACME", no "Kudawiki".
func (s *Service) BeginTOTP(ctx context.Context, u repo.User, issuer string) (TOTPSetup, error) {
	if u.TOTPEnabled {
		return TOTPSetup{}, ErrTOTPEnabled
	}
	secret := make([]byte, 20) // 160 bits, lo que recomienda RFC 4226 §4
	if _, err := rand.Read(secret); err != nil {
		return TOTPSetup{}, err
	}
	enc, err := crypto.Seal(s.mfa.Key, secret, totpAAD(u.ID))
	if err != nil {
		return TOTPSetup{}, err
	}
	if err := s.store.SetPendingTOTP(ctx, u.ID, enc, crypto.MasterKeyVersion); errors.Is(err, repo.ErrConflict) {
		return TOTPSetup{}, ErrTOTPEnabled
	} else if err != nil {
		return TOTPSetup{}, err
	}
	b32 := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
	uri := "otpauth://totp/" + uriEscape(issuer) + ":" + uriEscape(u.Email) +
		"?secret=" + b32 + "&issuer=" + uriEscape(issuer) + "&algorithm=SHA1&digits=6&period=30"
	code, err := qr.Encode(uri, qr.M)
	if err != nil {
		return TOTPSetup{}, err
	}
	rows := make([]string, code.Size)
	for y := range code.Size {
		var b strings.Builder
		for x := range code.Size {
			if code.Black(x, y) {
				b.WriteByte('1')
			} else {
				b.WriteByte('0')
			}
		}
		rows[y] = b.String()
	}
	return TOTPSetup{Secret: b32, URI: uri, QR: rows}, nil
}

// uriEscape escapa como pide el formato de Key URI: espacios como %20, no como "+".
func uriEscape(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }

// ConfirmTOTP activa el alta con un código de la app y devuelve los códigos de recuperación, que
// se muestran esta única vez. revokeOthers cierra las demás sesiones (§8.2).
func (s *Service) ConfirmTOTP(ctx context.Context, u repo.User, sessionToken, code string, revokeOthers bool) ([]string, error) {
	if _, err := s.allow("totp|" + u.ID); err != nil {
		return nil, err
	}
	t, err := s.store.TOTPOf(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	if t.Enabled {
		return nil, ErrTOTPEnabled
	}
	if t.SecretEnc == nil {
		return nil, ErrTOTPNotStarted
	}
	secret, err := s.openTOTP(u.ID, t)
	if err != nil {
		return nil, err
	}
	step, ok := matchTOTP(secret, normalizeCode(code), s.now())
	if !ok {
		return nil, ErrInvalidCode
	}
	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		return nil, err
	}
	if err := s.store.EnableTOTP(ctx, u.ID, step, hashes); errors.Is(err, repo.ErrConflict) {
		return nil, ErrTOTPEnabled
	} else if err != nil {
		return nil, err
	}
	s.forget("totp|" + u.ID)
	var closed int64
	if revokeOthers {
		if closed, err = s.store.DeleteOtherSessions(ctx, u.ID, hashToken(sessionToken)); err != nil {
			return nil, err
		}
	}
	s.audit.Record(ctx, audit.Event{Action: audit.AuthTOTPEnable, TargetType: "user", TargetID: u.ID,
		Meta: map[string]any{"other_sessions_closed": closed}})
	s.notifySecurity(ctx, u, "two_factor_changed")
	return codes, nil
}

// DisableTOTP lo apaga la persona misma, con su contraseña y un código: una sesión robada no
// alcanza. A quien se le exige no puede apagarlo, salvo que le quede una passkey (§8.1).
// revokeOthers cierra las demás sesiones (§8.2).
func (s *Service) DisableTOTP(ctx context.Context, u repo.User, sessionToken, password, code string, revokeOthers bool) error {
	required, err := s.MFARequired(ctx, u)
	if err != nil {
		return err
	}
	if required && u.Passkeys == 0 {
		return ErrTOTPRequired
	}
	if !u.TOTPEnabled {
		return nil
	}
	key := "totp|" + u.ID
	n, err := s.allow(key)
	if err != nil {
		return err
	}
	if err := s.checkPassword(ctx, u, password); err != nil { // de una cuenta del directorio, con un bind
		return err
	}
	if _, err := s.checkCode(ctx, u, code, n); err != nil {
		return err
	}
	if err := s.store.DisableTOTP(ctx, u.ID); err != nil {
		return err
	}
	s.forget(key)
	var closed int64
	if revokeOthers {
		if closed, err = s.store.DeleteOtherSessions(ctx, u.ID, hashToken(sessionToken)); err != nil {
			return err
		}
	}
	s.audit.Record(ctx, audit.Event{Action: audit.AuthTOTPDisable, TargetType: "user", TargetID: u.ID,
		Meta: map[string]any{"other_sessions_closed": closed}})
	s.notifySecurity(ctx, u, "two_factor_changed")
	return nil
}

// ResetTOTP es el restablecimiento del superadmin para quien perdió el celular (§14.2): borra el
// TOTP y las passkeys. Si a la persona se le exige, en su próximo pedido tendrá que configurarlo
// de nuevo.
func (s *Service) ResetTOTP(ctx context.Context, userID string) error {
	n, err := s.resetSecondFactor(ctx, userID)
	if err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.AuthTOTPReset, TargetType: "user", TargetID: userID, Meta: map[string]any{"passkeys": n}})
	if u, err := s.store.UserByID(ctx, userID); err == nil {
		s.notifySecurity(ctx, u, "two_factor_changed")
	}
	return nil
}

// resetSecondFactor borra todo el 2FA de una persona y dice cuántas passkeys tenía.
func (s *Service) resetSecondFactor(ctx context.Context, userID string) (int64, error) {
	if err := s.store.DisableTOTP(ctx, userID); err != nil {
		return 0, err
	}
	return s.store.DeletePasskeys(ctx, userID)
}

// challenge guarda el primer paso de un login con 2FA: la contraseña ya se verificó y falta el
// código o la passkey. Vive en memoria, como los intentos: si el servidor se reinicia se vuelve a
// empezar.
func (s *Service) challenge(u repo.User) (LoginResult, error) {
	token, err := newToken()
	if err != nil {
		return LoginResult{}, err
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, c := range s.challenges { // pocas a la vez: barrer en cada alta no cuesta
		if now.After(c.expires) {
			delete(s.challenges, k)
		}
	}
	s.challenges[hashToken(token)] = mfaChallenge{userID: u.ID, expires: now.Add(mfaChallengeTTL)}
	var methods []string
	if u.TOTPEnabled {
		methods = append(methods, "totp")
	}
	if u.Passkeys > 0 && s.rp != nil {
		methods = append(methods, "passkey")
	}
	return LoginResult{Challenge: token, Methods: methods}, nil
}

// VerifyLogin es el segundo paso (§14.1 POST /auth/totp): con el código de la app o uno de
// recuperación abre la sesión. Los intentos se cuentan por persona, no por IP: con la contraseña
// en la mano, cambiar de IP no da más oportunidades de adivinar.
func (s *Service) VerifyLogin(ctx context.Context, challenge, code string) (string, error) {
	h := hashToken(challenge)
	s.mu.Lock()
	c, ok := s.challenges[h]
	s.mu.Unlock()
	if !ok || s.now().After(c.expires) {
		return "", ErrMFAExpired
	}
	u, err := s.store.UserByID(ctx, c.userID)
	if errors.Is(err, repo.ErrNotFound) {
		return "", ErrMFAExpired
	}
	if err != nil {
		return "", err
	}
	if !u.IsActive || !u.TOTPEnabled {
		return "", ErrMFAExpired
	}
	key := "totp|" + u.ID
	n, err := s.allow(key)
	if err != nil {
		return "", err
	}
	factor, err := s.checkCode(ctx, u, code, n)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	delete(s.challenges, h)
	s.mu.Unlock()
	s.forget(key)
	return s.startSession(ctx, u, map[string]any{"second_factor": factor})
}

// checkCode acepta un código de la app (6 dígitos) o uno de recuperación. attempt es el número
// del intento en la ventana, para auditar el fallo como el del login.
func (s *Service) checkCode(ctx context.Context, u repo.User, code string, attempt int) (string, error) {
	code = normalizeCode(code)
	factor := "totp"
	ok, err := false, error(nil)
	if len(code) == 6 && strings.Trim(code, "0123456789") == "" {
		ok, err = s.useTOTP(ctx, u.ID, code)
	} else {
		factor = "recovery"
		ok, err = s.store.UseRecoveryCode(ctx, u.ID, hashRecovery(code))
	}
	if err != nil {
		return "", err
	}
	if !ok {
		e := audit.Event{TargetType: "user", TargetID: u.ID, Actor: actorOf(u),
			Meta: map[string]any{"attempts": attempt, "window_minutes": int(loginWindow.Minutes())}}
		switch attempt {
		case 1:
			e.Action = audit.AuthTOTPFailed
			s.audit.Record(ctx, e)
		case loginMaxAttempts:
			e.Action = audit.AuthTOTPLockout
			s.audit.Record(ctx, e)
		}
		return "", ErrInvalidCode
	}
	if factor == "recovery" {
		left, err := s.store.RecoveryCodesLeft(ctx, u.ID)
		if err != nil {
			return "", err
		}
		s.audit.Record(ctx, audit.Event{Action: audit.AuthTOTPRecoveryUsed, TargetType: "user", TargetID: u.ID, Actor: actorOf(u),
			Meta: map[string]any{"left": left}})
	}
	return factor, nil
}

// useTOTP verifica el código y gasta su paso: el mismo código no vale dos veces (RFC 6238 §5.2).
func (s *Service) useTOTP(ctx context.Context, userID, code string) (bool, error) {
	t, err := s.store.TOTPOf(ctx, userID)
	if err != nil {
		return false, err
	}
	if !t.Enabled {
		return false, nil
	}
	secret, err := s.openTOTP(userID, t)
	if err != nil {
		return false, err
	}
	step, ok := matchTOTP(secret, code, s.now())
	if !ok || step <= t.LastStep {
		return false, nil
	}
	return s.store.UseTOTPStep(ctx, userID, step)
}

func (s *Service) openTOTP(userID string, t repo.TOTP) ([]byte, error) {
	if t.KeyVersion != crypto.MasterKeyVersion {
		return nil, fmt.Errorf("totp secret of %s was sealed with master key version %d", userID, t.KeyVersion)
	}
	return crypto.Open(s.mfa.Key, t.SecretEnc, totpAAD(userID))
}

// totpAAD ata el secreto a su persona: copiado a otra fila no se abre.
func totpAAD(userID string) []byte { return []byte("totp|" + userID) }

// matchTOTP busca el paso cuyo código coincide, con la tolerancia de reloj; devuelve el paso
// para que no se pueda volver a usar.
func matchTOTP(secret []byte, code string, now time.Time) (int64, bool) {
	base := now.Unix() / totpPeriod
	for d := int64(-totpSkew); d <= totpSkew; d++ {
		if subtle.ConstantTimeCompare([]byte(totpCode(secret, base+d)), []byte(code)) == 1 {
			return base + d, true
		}
	}
	return 0, false
}

// totpCode es HOTP (RFC 4226 §5.3) sobre el número de paso.
func totpCode(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	m := hmac.New(sha1.New, secret)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", v%1_000_000)
}

// normalizeCode tolera lo que se copia y pega: espacios, guiones y mayúsculas.
func normalizeCode(code string) string {
	return strings.ToLower(strings.NewReplacer(" ", "", "-", "", "\t", "").Replace(strings.TrimSpace(code)))
}

// recoveryAlphabet es base32 sin 0, 1, l ni o, que se confunden al copiarlos de un papel.
const recoveryAlphabet = "abcdefghijkmnpqrstuvwxyz23456789"

// newRecoveryCodes genera los diez códigos (16 caracteres, 80 bits) como xxxx-xxxx-xxxx-xxxx.
func newRecoveryCodes() (codes, hashes []string, err error) {
	for range recoveryCount {
		raw := make([]byte, 16)
		if _, err := rand.Read(raw); err != nil {
			return nil, nil, err
		}
		var b strings.Builder
		for i, c := range raw {
			if i > 0 && i%4 == 0 {
				b.WriteByte('-')
			}
			b.WriteByte(recoveryAlphabet[int(c)%len(recoveryAlphabet)]) // 256 es múltiplo de 32: sin sesgo
		}
		codes = append(codes, b.String())
		hashes = append(hashes, hashRecovery(normalizeCode(b.String())))
	}
	return codes, hashes, nil
}

func hashRecovery(normalized string) string {
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}
