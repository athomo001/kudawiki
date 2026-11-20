package auth

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/athomo001/kudawiki/internal/audit"
)

// Política de contraseñas locales (SPEC §8.1, QA-06): el superadmin fija el mínimo desde el panel.
// Nunca baja de MinPasswordLength: ni el panel ni una fila editada a mano en la BD dejan entrar
// una contraseña más corta, y no hay excepción para localhost.
const (
	MinPasswordLength = 10
	MaxPasswordMin    = 64
	passwordMinKey    = "auth.password_min_length"
)

var ErrInvalidPasswordMin = errors.New("invalid_password_min")

// PasswordMin es el mínimo de caracteres vigente.
func (s *Service) PasswordMin(ctx context.Context) (int, error) {
	vals, err := s.store.SiteSettings(ctx, passwordMinKey)
	if err != nil {
		return 0, err
	}
	n := MinPasswordLength
	if v, ok := vals[passwordMinKey]; ok {
		if err := json.Unmarshal([]byte(v), &n); err != nil {
			return 0, err
		}
	}
	return min(max(n, MinPasswordLength), MaxPasswordMin), nil
}

// SetPasswordMin cambia el mínimo. Las contraseñas que ya existen siguen valiendo: el mínimo se
// aplica a la próxima que se elija.
func (s *Service) SetPasswordMin(ctx context.Context, actorID string, n int) error {
	if n < MinPasswordLength || n > MaxPasswordMin {
		return ErrInvalidPasswordMin
	}
	prev, err := s.PasswordMin(ctx)
	if err != nil {
		return err
	}
	if n == prev {
		return nil
	}
	v := strconv.Itoa(n)
	value := &v
	if n == MinPasswordLength {
		value = nil // el valor por defecto no se guarda
	}
	if err := s.store.SetSiteSettings(ctx, map[string]*string{passwordMinKey: value}, actorID); err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.SitePasswordPolicyUpdate, TargetType: "site",
		Meta: map[string]any{"from": prev, "to": n}})
	return nil
}

func (s *Service) checkPolicy(ctx context.Context, password string) error {
	n, err := s.PasswordMin(ctx)
	if err != nil {
		return err
	}
	return CheckPolicy(password, n)
}
