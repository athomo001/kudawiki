package auth

import (
	"context"
	"errors"

	"github.com/athomo001/kudawiki/internal/repo"
)

// ErrInvalidPreferences: idioma o preferencia de lectura fuera de la lista de SPEC §15.
var ErrInvalidPreferences = errors.New("invalid_preferences")

// Preferences es un cambio parcial: lo que viene en nil queda como estaba.
type Preferences struct {
	Locale       *string
	ReadingPrefs *repo.ReadingPrefs
	EditorMode   *string
}

// SetPreferences guarda el idioma y las preferencias de lectura de la persona misma (SPEC §15).
// No va a la auditoría: no da ni quita acceso a nada (§11.2).
func (s *Service) SetPreferences(ctx context.Context, userID string, p Preferences) error {
	u, err := s.store.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	locale, prefs, mode := u.Locale, u.ReadingPrefs, u.EditorMode
	if p.Locale != nil {
		locale = *p.Locale
	}
	if p.ReadingPrefs != nil {
		prefs = *p.ReadingPrefs
	}
	if p.EditorMode != nil {
		mode = *p.EditorMode
	}
	if (locale != "es" && locale != "en") || (mode != "markdown" && mode != "visual") {
		return ErrInvalidPreferences
	}
	switch prefs.Font {
	case "default", "dyslexic", "legible":
	default:
		return ErrInvalidPreferences
	}
	return s.store.SetPreferences(ctx, userID, locale, prefs, mode)
}
