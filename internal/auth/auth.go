// Package auth implementa el login local y las sesiones (SPEC §8.1, §8.2).
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/oklog/ulid/v2"
)

const (
	sessionIdle     = 12 * time.Hour     // SPEC §8.2
	sessionAbsolute = 7 * 24 * time.Hour // SPEC §8.2
	touchEvery      = time.Minute        // no escribir en cada request: hay un solo escritor (§13.4)

	loginMaxAttempts = 10 // SPEC §8.2: 10 intentos / 15 min por IP+email
	loginWindow      = 15 * time.Minute
)

var (
	ErrInvalidCredentials = errors.New("invalid_credentials")
	ErrNoSession          = errors.New("no_session")
)

// RateLimitedError lleva cuánto esperar, para el encabezado Retry-After.
type RateLimitedError struct{ RetryAfter time.Duration }

func (e RateLimitedError) Error() string { return "rate_limited" }

// store es lo que auth usa de repo, declarado aquí (SPEC §16.2).
type store interface {
	CreateUser(ctx context.Context, u repo.User) error
	UserByEmail(ctx context.Context, email string) (repo.User, error)
	UserByID(ctx context.Context, id string) (repo.User, error)
	CreateSession(ctx context.Context, s repo.Session) error
	SessionByHash(ctx context.Context, tokenHash string) (repo.Session, error)
	TouchSession(ctx context.Context, tokenHash string, at time.Time) error
	DeleteSession(ctx context.Context, tokenHash string) error
	CreateInvitedUser(ctx context.Context, u repo.User, t repo.AuthToken) error
	AcceptInvite(ctx context.Context, tokenHash string, now time.Time, passwordHash string) (string, error)
	ReplaceInvite(ctx context.Context, userID string, t repo.AuthToken) error
	SetPreferences(ctx context.Context, userID, locale string, prefs repo.ReadingPrefs) error
}

type Service struct {
	store     store
	audit     *audit.Recorder
	now       func() time.Time
	dummyHash string // se verifica contra él cuando el email no existe, para no revelarlo por el tiempo
	relaxed   bool   // política de contraseñas de desarrollo (SPEC §8.1)

	mu       sync.Mutex
	attempts map[string]attempt
}

type attempt struct {
	count int
	since time.Time
}

func NewService(s store, rec *audit.Recorder, now func() time.Time, relaxedPolicy bool) (*Service, error) {
	dummy, err := HashPassword("kudawiki-timing-equalizer")
	if err != nil {
		return nil, err
	}
	return &Service{store: s, audit: rec, now: now, dummyHash: dummy, relaxed: relaxedPolicy, attempts: map[string]attempt{}}, nil
}

// CreateSuperadmin es el único alta de usuarios de F1: la usa `kudawiki init` (SPEC §13.6).
func (s *Service) CreateSuperadmin(ctx context.Context, email, fullName, password string) error {
	if err := CheckPolicy(password, s.relaxed); err != nil {
		return err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	return s.store.CreateUser(ctx, repo.User{
		ID: ulid.Make().String(), Email: strings.TrimSpace(email), FullName: strings.TrimSpace(fullName),
		PasswordHash: hash, IsGlobalAdmin: true, IsActive: true, Locale: "es",
	})
}

// Login devuelve el token en claro, que solo viaja en la cookie. Email inexistente, cuenta
// desactivada o contraseña incorrecta dan el mismo error y tardan lo mismo.
func (s *Service) Login(ctx context.Context, ip, email, password string) (string, error) {
	key := ip + "|" + strings.ToLower(strings.TrimSpace(email))
	n, err := s.allow(key)
	if err != nil {
		return "", err
	}

	u, err := s.store.UserByEmail(ctx, strings.TrimSpace(email))
	if err != nil && !errors.Is(err, repo.ErrNotFound) {
		return "", err
	}
	hash := u.PasswordHash
	if hash == "" {
		hash = s.dummyHash
	}
	ok, err := VerifyPassword(hash, password)
	if err != nil {
		return "", err
	}
	if !ok || u.ID == "" || u.PasswordHash == "" || !u.IsActive {
		s.auditFailure(ctx, u, n)
		return "", ErrInvalidCredentials
	}

	s.mu.Lock()
	delete(s.attempts, key)
	s.mu.Unlock()

	token, err := newToken()
	if err != nil {
		return "", err
	}
	now := s.now()
	if err := s.store.CreateSession(ctx, repo.Session{TokenHash: hashToken(token), UserID: u.ID, CreatedAt: now, LastSeenAt: now}); err != nil {
		return "", err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.AuthLogin, TargetType: "user", TargetID: u.ID, Actor: actorOf(u)})
	return token, nil
}

// auditFailure no registra cada intento: el login es público y cualquiera podría llenar la
// auditoría (§11.2). Por persona existente e IP se registra el primer fallo de cada ventana de
// 15 minutos y el bloqueo, con el conteo. Los intentos contra emails que no existen no se registran.
func (s *Service) auditFailure(ctx context.Context, u repo.User, attempt int) {
	if u.ID == "" {
		return
	}
	e := audit.Event{TargetType: "user", TargetID: u.ID, Actor: actorOf(u),
		Meta: map[string]any{"attempts": attempt, "window_minutes": int(loginWindow.Minutes())}}
	switch attempt {
	case 1:
		e.Action = audit.AuthLoginFailed
	case loginMaxAttempts:
		e.Action = audit.AuthLockout
	default:
		return
	}
	s.audit.Record(ctx, e)
}

func actorOf(u repo.User) *audit.Actor {
	return &audit.Actor{ID: u.ID, Email: u.Email, Name: u.FullName}
}

// allow cuenta intentos por ventana fija; al pasar el tope, bloquea hasta que la ventana venza.
// Devuelve el número de este intento dentro de la ventana.
func (s *Service) allow(key string) (int, error) {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.attempts) > 10_000 { // barrido perezoso para que el mapa no crezca sin límite
		for k, a := range s.attempts {
			if now.Sub(a.since) >= loginWindow {
				delete(s.attempts, k)
			}
		}
	}
	a := s.attempts[key]
	if now.Sub(a.since) >= loginWindow {
		a = attempt{since: now}
	}
	if a.count >= loginMaxAttempts {
		return 0, RateLimitedError{RetryAfter: a.since.Add(loginWindow).Sub(now)}
	}
	a.count++
	s.attempts[key] = a
	return a.count, nil
}

// Authenticate valida la sesión y devuelve su usuario. Una sesión vencida se borra al verla.
func (s *Service) Authenticate(ctx context.Context, token string) (repo.User, error) {
	if token == "" {
		return repo.User{}, ErrNoSession
	}
	h := hashToken(token)
	sess, err := s.store.SessionByHash(ctx, h)
	if errors.Is(err, repo.ErrNotFound) {
		return repo.User{}, ErrNoSession
	}
	if err != nil {
		return repo.User{}, err
	}
	now := s.now()
	if now.Sub(sess.LastSeenAt) > sessionIdle || now.Sub(sess.CreatedAt) > sessionAbsolute {
		return repo.User{}, errors.Join(ErrNoSession, s.store.DeleteSession(ctx, h))
	}
	u, err := s.store.UserByID(ctx, sess.UserID)
	if err != nil {
		return repo.User{}, err
	}
	if !u.IsActive {
		return repo.User{}, ErrNoSession
	}
	if now.Sub(sess.LastSeenAt) > touchEvery {
		if err := s.store.TouchSession(ctx, h, now); err != nil {
			return repo.User{}, err
		}
	}
	return u, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if err := s.store.DeleteSession(ctx, hashToken(token)); err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.AuthLogout})
	return nil
}

// newToken: 32 bytes aleatorios, para sesiones e invitaciones (SPEC §8.2, §8.5).
func newToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// hashToken: en la BD solo queda el SHA-256 del token (SPEC §8.2).
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
