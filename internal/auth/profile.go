package auth

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/athomo001/kudawiki/internal/audit"
)

// Perfil de la persona misma (SPEC §8.5.2, §14.1): su nombre, su contraseña y su foto.

// ErrWrongPassword: la contraseña actual no coincide al cambiarla desde el perfil.
var ErrWrongPassword = errors.New("wrong_password")

const maxFullName = 120

// SetFullName cambia el nombre con que la persona aparece en la wiki. Queda en la auditoría: un
// nombre ajeno ("Jefe CDC") confundiría en el historial y en lo que firma.
func (s *Service) SetFullName(ctx context.Context, userID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxFullName || strings.ContainsFunc(name, unicode.IsControl) {
		return ErrInvalidUser
	}
	if err := s.store.SetFullName(ctx, userID, name); err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.UserProfileUpdate, TargetType: "user", TargetID: userID, Meta: map[string]any{"keys": []string{"full_name"}}})
	return nil
}

// ChangePassword es el cambio desde el perfil (§8.5.2). Pide la actual con el mismo tope de
// intentos que el login: una sesión robada no sirve para adivinarla. revokeOthers cierra las
// demás sesiones y deja abierta la de quien la cambia.
func (s *Service) ChangePassword(ctx context.Context, userID, sessionToken, current, next string, revokeOthers bool) error {
	if err := CheckPolicy(next, s.relaxed); err != nil {
		return err
	}
	key := "password|" + userID
	if _, err := s.allow(key); err != nil {
		return err
	}
	u, err := s.store.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	ok := false
	if u.PasswordHash != "" { // sin contraseña local (LDAP, F6) no hay nada que cambiar aquí
		if ok, err = VerifyPassword(u.PasswordHash, current); err != nil {
			return err
		}
	}
	if !ok {
		return ErrWrongPassword
	}
	hash, err := HashPassword(next)
	if err != nil {
		return err
	}
	if err := s.store.ChangePassword(ctx, userID, hash); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.attempts, key)
	s.mu.Unlock()
	var closed int64
	if revokeOthers {
		if closed, err = s.store.DeleteOtherSessions(ctx, userID, hashToken(sessionToken)); err != nil {
			return err
		}
	}
	s.audit.Record(ctx, audit.Event{Action: audit.AuthPasswordChange, TargetType: "user", TargetID: userID,
		Meta: map[string]any{"other_sessions_closed": closed}})
	return nil
}

// SetAvatar guarda la foto ya re-codificada: nunca se sirve el archivo que subió la persona.
func (s *Service) SetAvatar(ctx context.Context, userID string, data []byte) error {
	png, err := avatarPNG(data)
	if err != nil {
		return err
	}
	return s.saveAvatar(ctx, userID, png)
}

// DeleteAvatar quita la foto: vuelven las iniciales.
func (s *Service) DeleteAvatar(ctx context.Context, userID string) error {
	return s.saveAvatar(ctx, userID, nil)
}

func (s *Service) saveAvatar(ctx context.Context, userID string, png []byte) error {
	if err := s.store.SetAvatar(ctx, userID, png); err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.UserProfileUpdate, TargetType: "user", TargetID: userID, Meta: map[string]any{"keys": []string{"avatar"}}})
	return nil
}

// Avatar devuelve la foto de la persona; repo.ErrNotFound si no tiene.
func (s *Service) Avatar(ctx context.Context, userID string) ([]byte, error) {
	return s.store.Avatar(ctx, userID)
}
