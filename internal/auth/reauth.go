package auth

import (
	"context"
	"errors"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/repo"
)

// reauthTTL es cuánto vale la reautenticación (§8.2): lo justo para terminar lo que se estaba
// haciendo, no el resto del día.
const reauthTTL = 10 * time.Minute

// Reauth vuelve a pedir quién es antes de lo delicado (§8.2): la contraseña y, con 2FA, un código
// de la app o uno de recuperación. Vale solo para la sesión que la pidió.
//
// Los intentos comparten el contador del segundo paso del login (por persona): con una sesión
// robada, la reautenticación no da una ventana más para adivinar la contraseña o el código.
//
// Una cuenta del directorio no tiene contraseña local: la suya se prueba con un bind (§8.6.2),
// después del mismo tope.
func (s *Service) Reauth(ctx context.Context, u repo.User, sessionToken, password, code string) error {
	if u.PasswordHash == "" && (!isDirectory(u) || s.directory == nil) {
		return ErrNoLocalPassword
	}
	key := "totp|" + u.ID
	n, err := s.allow(key)
	if err != nil {
		return err
	}
	if err := s.checkPassword(ctx, u, password); err != nil {
		return err
	}
	factor := ""
	if u.TOTPEnabled {
		if factor, err = s.checkCode(ctx, u, code, n); err != nil {
			return err
		}
	}
	s.forget(key)
	return s.markReauth(ctx, u, sessionToken, factor)
}

// markReauth deja la sesión reautenticada por reauthTTL. factor es con qué ("totp", "recovery",
// "passkey"), vacío si bastó la contraseña.
func (s *Service) markReauth(ctx context.Context, u repo.User, sessionToken, factor string) error {
	if err := s.store.SetReauth(ctx, hashToken(sessionToken), s.now().Add(reauthTTL)); err != nil {
		return err
	}
	e := audit.Event{Action: audit.AuthReauth, TargetType: "user", TargetID: u.ID}
	if factor != "" {
		e.Meta = map[string]any{"second_factor": factor}
	}
	s.audit.Record(ctx, e)
	return nil
}

// Reauthenticated dice si la sesión se reautenticó hace menos de reauthTTL.
func (s *Service) Reauthenticated(ctx context.Context, sessionToken string) (bool, error) {
	sess, err := s.store.SessionByHash(ctx, hashToken(sessionToken))
	if errors.Is(err, repo.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return s.now().Before(sess.ReauthUntil), nil
}

// checkPassword prueba la contraseña de una persona con sesión: la local o, si la cuenta es del
// directorio, con un bind. Quien la llama ya aplicó el tope de intentos.
func (s *Service) checkPassword(ctx context.Context, u repo.User, password string) error {
	if isDirectory(u) {
		if s.directory == nil {
			return ErrNoLocalPassword
		}
		return s.directory.VerifyPassword(ctx, u, password)
	}
	if u.PasswordHash == "" {
		return ErrNoLocalPassword
	}
	ok, err := VerifyPassword(u.PasswordHash, password)
	if err != nil {
		return err
	}
	if !ok {
		return ErrWrongPassword
	}
	return nil
}
