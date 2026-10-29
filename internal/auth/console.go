package auth

import (
	"context"
	"errors"
	"net/mail"
	"strings"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/repo"
)

// Recuperación por consola (SPEC §13.6): para cuando el superadmin queda afuera. Solo la usa
// `kudawiki admin`, que lee la BD directamente: quien la corre tiene shell en el servidor y no
// es una persona de la wiki, así que se registra como el sistema "cli", sin actor (§11.2).
const consoleSystem = "cli"

// UnlockLogin levanta el bloqueo por intentos fallidos de una persona, en todas las IPs.
func (s *Service) UnlockLogin(ctx context.Context, email string) error {
	u, err := s.store.UserByEmail(ctx, strings.TrimSpace(email))
	if err != nil {
		return err
	}
	if err := s.store.UnlockLogin(ctx, u.ID, s.now()); err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.CLIUnlock, TargetType: "user", TargetID: u.ID, System: consoleSystem})
	return nil
}

// SetPassword fija una contraseña con la misma política que el resto (§8.1), cierra las
// sesiones de la persona y anula sus enlaces sin usar. Mientras no haya LDAP (F6) todas las
// cuentas son locales.
func (s *Service) SetPassword(ctx context.Context, email, password string) error {
	if err := CheckPolicy(password, s.relaxed); err != nil {
		return err
	}
	u, err := s.store.UserByEmail(ctx, strings.TrimSpace(email))
	if err != nil {
		return err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	if err := s.store.SetPassword(ctx, u.ID, hash); err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.CLISetPassword, TargetType: "user", TargetID: u.ID, System: consoleSystem})
	return nil
}

// CreateEmergencySuperadmin crea una cuenta local de superadmin, para cuando no queda ninguno
// que pueda entrar. A diferencia del alta de `init`, esta sí queda en la auditoría: ya hay una
// wiki en uso.
func (s *Service) CreateEmergencySuperadmin(ctx context.Context, email, fullName, password string) error {
	email, fullName = strings.TrimSpace(email), strings.TrimSpace(fullName)
	if addr, err := mail.ParseAddress(email); err != nil || addr.Address != email || fullName == "" {
		return ErrInvalidUser
	}
	err := s.CreateSuperadmin(ctx, email, fullName, password)
	if errors.Is(err, repo.ErrConflict) {
		return ErrEmailTaken
	}
	if err != nil {
		return err
	}
	u, err := s.store.UserByEmail(ctx, email)
	if err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.CLICreateSuperadmin, TargetType: "user", TargetID: u.ID, System: consoleSystem})
	return nil
}

// RevokeSessions cierra las sesiones de una persona, o las de todos con email vacío. Devuelve
// cuántas cerró.
func (s *Service) RevokeSessions(ctx context.Context, email string) (int64, error) {
	e := audit.Event{Action: audit.CLIRevokeSessions, System: consoleSystem}
	userID := ""
	if email != "" {
		u, err := s.store.UserByEmail(ctx, strings.TrimSpace(email))
		if err != nil {
			return 0, err
		}
		userID, e.TargetType, e.TargetID = u.ID, "user", u.ID
	}
	n, err := s.store.DeleteSessions(ctx, userID)
	if err != nil {
		return 0, err
	}
	e.Meta = map[string]any{"sessions": n, "all": email == ""}
	s.audit.Record(ctx, e)
	return n, nil
}
