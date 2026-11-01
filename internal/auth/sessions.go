package auth

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/mail"
	"github.com/athomo001/kudawiki/internal/repo"
)

// maxUserAgent: lo que se guarda del user agent. Alcanza para reconocer el navegador, y un
// encabezado gigante no llena la base.
const maxUserAgent = 512

// Client es desde dónde llega un pedido: lo pone la capa HTTP en el contexto y lo usa el inicio
// de sesión para "Mis sesiones" y el aviso de inicio de sesión nuevo (§8.2).
type Client struct{ IP, UserAgent string }

type clientKey struct{}

func WithClient(ctx context.Context, c Client) context.Context {
	return context.WithValue(ctx, clientKey{}, c)
}

func clientFrom(ctx context.Context) Client {
	c, _ := ctx.Value(clientKey{}).(Client)
	if len(c.UserAgent) > maxUserAgent {
		c.UserAgent = c.UserAgent[:maxUserAgent]
	}
	return c
}

// Device es el navegador y el sistema, leídos del user agent. Vacíos si no se reconocen.
type Device struct {
	Browser string `json:"browser"`
	OS      string `json:"os"`
}

// DeviceOf reconoce lo justo para que una persona sepa cuál de sus sesiones es cuál. El orden
// importa: Edge y Opera dicen también "Chrome", Chrome dice también "Safari", el iPad dice
// "Mac OS X" y Android dice "Linux".
func DeviceOf(ua string) Device {
	var d Device
	for _, b := range []struct{ mark, name string }{
		{"Edg/", "Edge"}, {"EdgA/", "Edge"}, {"EdgiOS/", "Edge"}, {"OPR/", "Opera"}, {"Firefox/", "Firefox"}, {"FxiOS/", "Firefox"},
		{"CriOS/", "Chrome"}, {"Chrome/", "Chrome"}, {"Chromium/", "Chrome"}, {"Safari/", "Safari"},
	} {
		if strings.Contains(ua, b.mark) {
			d.Browser = b.name
			break
		}
	}
	for _, o := range []struct{ mark, name string }{
		{"Windows", "Windows"}, {"iPhone", "iOS"}, {"iPad", "iOS"}, {"Android", "Android"}, {"CrOS", "ChromeOS"},
		{"Mac OS X", "macOS"}, {"Macintosh", "macOS"}, {"Linux", "Linux"},
	} {
		if strings.Contains(ua, o.mark) {
			d.OS = o.name
			break
		}
	}
	return d
}

// label es el navegador y el sistema en una línea, igual en todos los idiomas: "Chrome · Windows".
func (d Device) label() string {
	parts := make([]string, 0, 2)
	for _, p := range []string{d.Browser, d.OS} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return "?"
	}
	return strings.Join(parts, " · ")
}

// SessionInfo es una sesión en "Mis sesiones" (§8.2).
type SessionInfo struct {
	ID         string    `json:"id"`
	Device     Device    `json:"device"`
	IP         string    `json:"ip"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	Current    bool      `json:"current"`
}

// Sessions son las sesiones abiertas de la persona, la actual primero y las demás por última
// actividad; las vencidas no se muestran (ya no sirven para entrar).
func (s *Service) Sessions(ctx context.Context, u repo.User, sessionToken string) ([]SessionInfo, error) {
	all, err := s.store.SessionsOf(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	now, current := s.now(), hashToken(sessionToken)
	out := make([]SessionInfo, 0, len(all))
	for _, x := range all {
		if expired(x, now) {
			continue
		}
		out = append(out, SessionInfo{ID: x.ID, Device: DeviceOf(x.UserAgent), IP: x.IP, CreatedAt: x.CreatedAt, LastSeenAt: x.LastSeenAt,
			Current: x.TokenHash == current})
	}
	// La actividad se anota una vez por minuto (touchEvery): sin esto la actual podría quedar abajo.
	slices.SortStableFunc(out, func(a, b SessionInfo) int {
		switch {
		case a.Current == b.Current:
			return 0
		case a.Current:
			return -1
		}
		return 1
	})
	return out, nil
}

func expired(x repo.Session, now time.Time) bool {
	return now.Sub(x.LastSeenAt) > sessionIdle || now.Sub(x.CreatedAt) > sessionAbsolute
}

// RevokeSession cierra una sesión de la persona. La de otra persona es el mismo 404 que una que no
// existe.
func (s *Service) RevokeSession(ctx context.Context, u repo.User, id string) error {
	if err := s.store.DeleteUserSession(ctx, u.ID, id); err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.SessionRevoke, TargetType: "user", TargetID: u.ID, Meta: map[string]any{"sessions": 1}})
	return nil
}

// RevokeOtherSessions cierra todas las sesiones de la persona menos la que está usando.
func (s *Service) RevokeOtherSessions(ctx context.Context, u repo.User, sessionToken string) (int64, error) {
	n, err := s.store.DeleteOtherSessions(ctx, u.ID, hashToken(sessionToken))
	if err != nil {
		return 0, err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.SessionRevoke, TargetType: "user", TargetID: u.ID,
		Meta: map[string]any{"sessions": n, "others": true}})
	return n, nil
}

// AdminRevokeSessions es el superadmin cerrando todas las sesiones de otra persona (§8.2, B16),
// por ejemplo si perdió el notebook.
func (s *Service) AdminRevokeSessions(ctx context.Context, userID string) (int64, error) {
	if _, err := s.store.UserByID(ctx, userID); err != nil {
		return 0, err // sin persona no hay nada que cerrar; y DeleteSessions("") cerraría las de todos
	}
	n, err := s.store.DeleteSessions(ctx, userID)
	if err != nil {
		return 0, err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.SessionRevoke, TargetType: "user", TargetID: userID, Meta: map[string]any{"sessions": n}})
	return n, nil
}

// noticeNewLogin anota desde dónde entró la persona y, si la IP o el navegador son nuevos, le
// manda el aviso (§8.2, §12.1: de seguridad, no desactivable). La sesión ya está abierta: si algo
// falla aquí, se registra en el log y el login sigue.
func (s *Service) noticeNewLogin(ctx context.Context, u repo.User, c Client) {
	d := DeviceOf(c.UserAgent)
	isNew, err := s.store.RecordLogin(ctx, u.ID, c.IP, d.label(), s.now())
	if err != nil {
		s.logError("record login", err)
		return
	}
	if !isNew || !s.mailOn(ctx) {
		return
	}
	err = s.email.Mail.Enqueue(ctx, mail.Message{To: u.Email, Lang: u.Locale, Template: "new_login", URL: s.email.BaseURL + "/me/profile#sessions",
		Vars: map[string]string{"name": u.FullName, "when": s.when(), "device": d.label(), "ip": c.IP}})
	if err != nil {
		s.logError("new login notice", err)
	}
}
