package auth

import (
	"context"
	"errors"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	gomail "github.com/athomo001/kudawiki/internal/mail"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/oklog/ulid/v2"
)

// defaultInviteTTL: la invitación vale 7 días (auth.local.invite_ttl, SPEC §8.5.3).
const defaultInviteTTL = 7 * 24 * time.Hour

var (
	ErrInvalidUser   = errors.New("invalid_user")
	ErrEmailTaken    = errors.New("email_taken")
	ErrInvalidInvite = errors.New("invalid_invite")
	// ErrNotInvited: la persona ya eligió su contraseña; no hay invitación que reenviar.
	ErrNotInvited = errors.New("not_invited")
)

// Invite da de alta a una persona sin contraseña: el superadmin nunca define la contraseña de
// otro (SPEC §8.5.3). Con correo, la invitación sale por la cola y el token vuelve vacío; sin
// correo, vuelve el token para mostrar el enlace una sola vez.
func (s *Service) Invite(ctx context.Context, actorID, email, fullName string) (string, error) {
	email, fullName = strings.TrimSpace(email), strings.TrimSpace(fullName)
	if addr, err := mail.ParseAddress(email); err != nil || addr.Address != email || fullName == "" {
		return "", ErrInvalidUser
	}
	token, err := newToken()
	if err != nil {
		return "", err
	}
	u := repo.User{ID: ulid.Make().String(), Email: email, FullName: fullName, Locale: "es"}
	err = s.store.CreateInvitedUser(ctx, u, repo.AuthToken{TokenHash: hashToken(token), CreatedBy: actorID, ExpiresAt: s.now().Add(s.inviteTTL)})
	if errors.Is(err, repo.ErrConflict) {
		return "", ErrEmailTaken
	}
	if err != nil {
		return "", err
	}
	return s.deliverInvite(ctx, actorID, u, token, false)
}

// ResendInvite genera un enlace nuevo para quien todavía no aceptó (vencido o no) y anula el
// anterior; se entrega igual que el alta (SPEC §8.5.3).
func (s *Service) ResendInvite(ctx context.Context, actorID, userID string) (string, error) {
	u, err := s.store.UserByID(ctx, userID)
	if err != nil {
		return "", err
	}
	if !u.Invited {
		return "", ErrNotInvited
	}
	token, err := s.newLink(ctx, userID, "invite", actorID, s.inviteTTL)
	if err != nil {
		return "", err
	}
	return s.deliverInvite(ctx, actorID, u, token, true)
}

// deliverInvite encola el correo de invitación o, sin correo, devuelve el token para que el
// superadmin lo entregue por otro canal. La auditoría dice cuál de las dos (§8.5.3).
func (s *Service) deliverInvite(ctx context.Context, actorID string, u repo.User, token string, resend bool) (string, error) {
	meta := map[string]any{"delivery": "manual"}
	if resend {
		meta["resend"] = true
	}
	if s.mailOn(ctx) {
		inviter, err := s.store.UserByID(ctx, actorID)
		if err != nil {
			return "", err
		}
		err = s.email.Mail.Enqueue(ctx, gomail.Message{To: u.Email, Lang: u.Locale, Template: "invite", URL: s.email.BaseURL + "/invite#" + token,
			Vars: map[string]string{"name": u.FullName, "inviter": inviter.FullName, "email": u.Email, "days": strconv.Itoa(int(s.inviteTTL.Hours() / 24))}})
		if err != nil {
			return "", err
		}
		meta["delivery"] = "email"
		token = ""
	}
	s.audit.Record(ctx, audit.Event{Action: audit.UserInvite, TargetType: "user", TargetID: u.ID, Meta: meta})
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
