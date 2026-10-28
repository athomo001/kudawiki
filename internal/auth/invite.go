package auth

import (
	"context"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/oklog/ulid/v2"
)

// inviteTTL: la invitación vale 7 días (SPEC §8.5.3).
const inviteTTL = 7 * 24 * time.Hour

var (
	ErrInvalidUser   = errors.New("invalid_user")
	ErrEmailTaken    = errors.New("email_taken")
	ErrInvalidInvite = errors.New("invalid_invite")
	// ErrNotInvited: la persona ya eligió su contraseña; no hay invitación que reenviar.
	ErrNotInvited = errors.New("not_invited")
)

// Invite da de alta a una persona sin contraseña y devuelve el token del enlace, que se
// muestra una sola vez: el superadmin nunca define la contraseña de otro (SPEC §8.5.3).
func (s *Service) Invite(ctx context.Context, actorID, email, fullName string) (string, error) {
	email, fullName = strings.TrimSpace(email), strings.TrimSpace(fullName)
	if addr, err := mail.ParseAddress(email); err != nil || addr.Address != email || fullName == "" {
		return "", ErrInvalidUser
	}
	token, err := newToken()
	if err != nil {
		return "", err
	}
	id := ulid.Make().String()
	err = s.store.CreateInvitedUser(ctx,
		repo.User{ID: id, Email: email, FullName: fullName, Locale: "es"},
		repo.AuthToken{TokenHash: hashToken(token), CreatedBy: actorID, ExpiresAt: s.now().Add(inviteTTL)})
	if errors.Is(err, repo.ErrConflict) {
		return "", ErrEmailTaken
	}
	if err != nil {
		return "", err
	}
	// Sin correo (llega en F4) el enlace lo entrega el superadmin por otro canal, y así queda
	// en la auditoría (SPEC §8.5.3).
	s.audit.Record(ctx, audit.Event{Action: audit.UserInvite, TargetType: "user", TargetID: id, Meta: map[string]any{"delivery": "manual"}})
	return token, nil
}

// ResendInvite genera un enlace nuevo para quien todavía no aceptó (vencido o no) y anula el
// anterior. Como el alta, el enlace se muestra una sola vez (SPEC §8.5.3).
func (s *Service) ResendInvite(ctx context.Context, actorID, userID string) (string, error) {
	u, err := s.store.UserByID(ctx, userID)
	if err != nil {
		return "", err
	}
	if !u.Invited {
		return "", ErrNotInvited
	}
	token, err := newToken()
	if err != nil {
		return "", err
	}
	if err := s.store.ReplaceInvite(ctx, userID,
		repo.AuthToken{TokenHash: hashToken(token), CreatedBy: actorID, ExpiresAt: s.now().Add(inviteTTL)}); err != nil {
		return "", err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.UserInvite, TargetType: "user", TargetID: userID,
		Meta: map[string]any{"delivery": "manual", "resend": true}})
	return token, nil
}

// AcceptInvite fija la contraseña elegida por la persona. No inicia sesión: se entra con ella.
func (s *Service) AcceptInvite(ctx context.Context, token, password string) error {
	if err := CheckPolicy(password, s.relaxed); err != nil {
		return err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	id, err := s.store.AcceptInvite(ctx, hashToken(token), s.now(), hash)
	if errors.Is(err, repo.ErrNotFound) {
		return ErrInvalidInvite
	}
	if err != nil {
		return err
	}
	// Todavía no hay sesión: el actor es la persona que acaba de aceptar.
	u, err := s.store.UserByID(ctx, id)
	if err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.UserInviteAccept, TargetType: "user", TargetID: id, Actor: actorOf(u)})
	return nil
}
