package auth

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/mail"
	"github.com/athomo001/kudawiki/internal/repo"
)

// Recuperación de contraseña e invitaciones por correo (SPEC §8.5), solo para cuentas locales.

const (
	defaultResetTTL = 30 * time.Minute // auth.local.reset_ttl
	// Límites del formulario público (§8.5.1): por correo y por IP, cada hora.
	resetPerEmail = 3
	resetPerIP    = 10
	resetWindow   = time.Hour
	asyncTimeout  = 30 * time.Second
)

var (
	ErrInvalidReset     = errors.New("invalid_reset")     // vencido, usado o inexistente
	ErrSamePassword     = errors.New("same_password")     // §8.5.1: no puede ser la actual
	ErrNoLocalPassword  = errors.New("no_local_password") // invitada sin aceptar (o de LDAP, F6)
	ErrResetUnavailable = errors.New("reset_unavailable") // sin correo o apagado por configuración
)

// mailer es la cola de correos (§12), declarada aquí.
type mailer interface {
	Enabled(ctx context.Context) (bool, error)
	Enqueue(ctx context.Context, m mail.Message) error
}

// Email conecta auth con el correo. Sin él (la consola) o sin SMTP configurado, las
// invitaciones y los restablecimientos se entregan a mano (§8.5.3).
type Email struct {
	Mail          mailer
	BaseURL       string        // para armar los enlaces
	PasswordReset bool          // auth.local.password_reset: el "¿Olvidaste tu contraseña?" del login
	ResetTTL      time.Duration // auth.local.reset_ttl
	InviteTTL     time.Duration // auth.local.invite_ttl
	Log           *slog.Logger  // lo que falla fuera de la petición
}

// SetEmail lo arma `serve`; los comandos de consola no mandan correos.
func (s *Service) SetEmail(e Email) {
	e.BaseURL = strings.TrimSuffix(e.BaseURL, "/")
	if e.ResetTTL > 0 {
		s.resetTTL = e.ResetTTL
	}
	if e.InviteTTL > 0 {
		s.inviteTTL = e.InviteTTL
	}
	s.email = e
}

// mailOn dice si hay correo; un error al leer la configuración cuenta como que no.
func (s *Service) mailOn(ctx context.Context) bool {
	if s.email.Mail == nil {
		return false
	}
	ok, err := s.email.Mail.Enabled(ctx)
	if err != nil {
		s.logError("read smtp settings", err)
	}
	return ok && err == nil
}

// ResetAvailable dice si el login ofrece "¿Olvidaste tu contraseña?" (§8.5.1).
func (s *Service) ResetAvailable(ctx context.Context) bool {
	return s.email.PasswordReset && s.mailOn(ctx)
}

// RequestPasswordReset es el formulario público. Responde igual y tarda lo mismo exista o no la
// cuenta: buscarla, crear el enlace y encolar el correo pasa fuera de la petición. Pasado el
// límite tampoco cambia nada visible (§8.5.1).
func (s *Service) RequestPasswordReset(ctx context.Context, ip, email string) error {
	if !s.ResetAvailable(ctx) {
		return ErrResetUnavailable
	}
	email = strings.TrimSpace(email)
	if _, err := s.count("reset-ip|"+ip, resetPerIP, resetWindow); err != nil {
		return nil
	}
	if _, err := s.count("reset|"+strings.ToLower(email), resetPerEmail, resetWindow); err != nil {
		return nil
	}
	// WithoutCancel: la petición ya respondió, pero el actor (la IP) sigue en el contexto.
	bg := context.WithoutCancel(ctx)
	s.async(func() {
		ctx, cancel := context.WithTimeout(bg, asyncTimeout)
		defer cancel()
		u, err := s.store.UserByEmail(ctx, email)
		if errors.Is(err, repo.ErrNotFound) {
			return // nada que mandar, y nada en la auditoría: cualquiera podría llenarla (§11.2)
		}
		if err != nil {
			s.logError("password reset lookup", err)
			return
		}
		if !u.IsActive || u.PasswordHash == "" {
			return
		}
		if err := s.sendReset(ctx, u, "", ip); err != nil {
			s.logError("password reset", err)
		}
	})
	return nil
}

// AdminPasswordReset es "Enviar enlace para restablecer" en el panel (§8.5.3): por correo, o el
// enlace para entregarlo a mano si emailed es false. El superadmin nunca conoce la contraseña.
// Devuelve el token solo en la entrega manual.
func (s *Service) AdminPasswordReset(ctx context.Context, actorID, ip, userID string, emailed bool) (string, error) {
	u, err := s.store.UserByID(ctx, userID)
	if err != nil {
		return "", err
	}
	if !u.IsActive {
		return "", repo.ErrNotFound
	}
	if u.PasswordHash == "" {
		return "", ErrNoLocalPassword // con la invitación pendiente se reenvía la invitación
	}
	if emailed {
		if !s.mailOn(ctx) {
			return "", mail.ErrNotConfigured
		}
		return "", s.sendReset(ctx, u, actorID, ip)
	}
	token, err := s.newLink(ctx, u.ID, "password_reset", actorID, s.resetTTL)
	if err != nil {
		return "", err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.AuthPasswordResetRequest, TargetType: "user", TargetID: u.ID, Meta: map[string]any{"delivery": "manual"}})
	return token, nil
}

// sendReset crea el enlace (y anula el anterior) y encola el correo con la fecha, la hora y la
// IP de la solicitud (§8.5.1). Queda en la auditoría solo porque la cuenta existe.
func (s *Service) sendReset(ctx context.Context, u repo.User, actorID, ip string) error {
	token, err := s.newLink(ctx, u.ID, "password_reset", actorID, s.resetTTL)
	if err != nil {
		return err
	}
	err = s.email.Mail.Enqueue(ctx, mail.Message{To: u.Email, Lang: u.Locale, Template: "password_reset", URL: s.email.BaseURL + "/reset#" + token,
		Vars: map[string]string{"name": u.FullName, "when": s.when(), "ip": ip, "minutes": strconv.Itoa(int(s.resetTTL.Minutes()))}})
	if err != nil {
		return err
	}
	// Sin sesión, el actor del contexto es solo la IP de quien lo pidió.
	s.audit.Record(ctx, audit.Event{Action: audit.AuthPasswordResetRequest, TargetType: "user", TargetID: u.ID, Meta: map[string]any{"delivery": "email"}})
	return nil
}

// CompletePasswordReset fija la contraseña nueva con el enlace. Cierra todas las sesiones, avisa
// a la persona y, si es superadmin, a los demás (§8.5.1, §8.5.3). No inicia sesión: se entra con
// la contraseña nueva. El 2FA, cuando llegue (F6), no se toca.
func (s *Service) CompletePasswordReset(ctx context.Context, token, password string) error {
	if err := CheckPolicy(password, s.relaxed); err != nil {
		return err
	}
	id, err := s.store.ResetTokenUser(ctx, hashToken(token), s.now())
	if errors.Is(err, repo.ErrNotFound) {
		return ErrInvalidReset
	}
	if err != nil {
		return err
	}
	u, err := s.store.UserByID(ctx, id)
	if err != nil {
		return err
	}
	if same, err := VerifyPassword(u.PasswordHash, password); err != nil {
		return err
	} else if same {
		return ErrSamePassword
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	if _, err := s.store.CompleteReset(ctx, hashToken(token), s.now(), hash); errors.Is(err, repo.ErrNotFound) {
		return ErrInvalidReset
	} else if err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.AuthPasswordResetComplete, TargetType: "user", TargetID: u.ID, Actor: actorOf(u)})
	s.notifyPasswordChanged(ctx, u)
	if u.IsGlobalAdmin {
		s.notifySuperadmins(ctx, u)
	}
	return nil
}

// notifyPasswordChanged manda "Tu contraseña cambió" (§12.1, no desactivable), con un enlace
// para escribirles a los superadmins si no fue la persona. La contraseña ya cambió: si el aviso
// no se puede encolar, se registra en el log y nada más.
func (s *Service) notifyPasswordChanged(ctx context.Context, u repo.User) {
	if !s.mailOn(ctx) {
		return
	}
	var report string
	if admins := s.superadminEmails(ctx, u.ID); len(admins) > 0 {
		report = "mailto:" + strings.Join(admins, ",")
	}
	err := s.email.Mail.Enqueue(ctx, mail.Message{To: u.Email, Lang: u.Locale, Template: "password_changed", URL: report,
		Vars: map[string]string{"name": u.FullName, "when": s.when()}})
	if err != nil {
		s.logError("password changed notice", err)
	}
}

// notifySuperadmins avisa a los demás superadmins que uno recuperó su contraseña por correo:
// quien controla su correo controla su cuenta (§8.5.3).
func (s *Service) notifySuperadmins(ctx context.Context, u repo.User) {
	if !s.mailOn(ctx) {
		return
	}
	users, err := s.store.ListUsers(ctx)
	if err != nil {
		s.logError("list superadmins", err)
		return
	}
	for _, a := range users {
		if !a.IsGlobalAdmin || !a.IsActive || a.ID == u.ID {
			continue
		}
		err := s.email.Mail.Enqueue(ctx, mail.Message{To: a.Email, Lang: a.Locale, Template: "superadmin_password_reset", URL: s.email.BaseURL + "/admin/audit",
			Vars: map[string]string{"name": u.FullName, "email": u.Email, "when": s.when()}})
		if err != nil {
			s.logError("superadmin notice", err)
		}
	}
}

func (s *Service) superadminEmails(ctx context.Context, except string) []string {
	users, err := s.store.ListUsers(ctx)
	if err != nil {
		s.logError("list superadmins", err)
		return nil
	}
	var out []string
	for _, a := range users {
		if a.IsGlobalAdmin && a.IsActive && a.ID != except {
			out = append(out, a.Email)
		}
	}
	return out
}

// newLink crea un enlace de un solo uso y anula los anteriores del mismo tipo.
func (s *Service) newLink(ctx context.Context, userID, kind, actorID string, ttl time.Duration) (string, error) {
	token, err := newToken()
	if err != nil {
		return "", err
	}
	err = s.store.ReplaceToken(ctx, userID, kind, repo.AuthToken{TokenHash: hashToken(token), CreatedBy: actorID, ExpiresAt: s.now().Add(ttl)})
	return token, err
}

// when es la hora de la solicitud tal como la lee la persona en el correo, con su zona.
func (s *Service) when() string { return s.now().Local().Format("2006-01-02 15:04 MST") }

func (s *Service) logError(msg string, err error) {
	if s.email.Log != nil {
		s.email.Log.Error(msg, "err", err)
	}
}
