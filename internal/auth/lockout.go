package auth

import "time"

// Bloqueo escalonado del login con contraseña (SPEC §8.2, decisión del dueño 2026-11-19, QA-04),
// por IP + correo: cada 3 intentos fallidos hay que esperar 1 minuto, y desde el 9.º, 5 minutos.
// La pantalla de login muestra cuánto falta (Retry-After). Es por IP + correo y no solo por correo
// para que nadie pueda dejar afuera a una persona escribiendo mal su contraseña desde otro lado.
const (
	loginBurst      = 3
	loginShortWait  = time.Minute
	loginLongWait   = 5 * time.Minute
	loginLongAfter  = 9                // desde este intento fallido la espera es la larga
	loginForgetIdle = 15 * time.Minute // sin intentos por este tiempo, la serie empieza de cero
)

type lockout struct {
	fails int
	last  time.Time // último intento de la serie
	until time.Time // bloqueado hasta
}

// loginWait es la espera que deja el intento número n, o cero si todavía no toca.
func loginWait(n int) time.Duration {
	switch {
	case n%loginBurst != 0:
		return 0
	case n >= loginLongAfter:
		return loginLongWait
	default:
		return loginShortWait
	}
}

// tryLogin reserva un intento antes de verificar la contraseña, así una ráfaga de pedidos
// simultáneos no se cuela antes de que el primero termine. Devuelve el número del intento en la
// serie y la espera que deja (cero si no bloquea). Un acierto borra la serie (forgetLogin).
func (s *Service) tryLogin(key string) (int, time.Duration, error) {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.lockouts) > 10_000 { // barrido perezoso, como el de los demás contadores
		for k, l := range s.lockouts {
			if now.Sub(l.last) >= loginForgetIdle && !now.Before(l.until) {
				delete(s.lockouts, k)
			}
		}
	}
	l := s.lockouts[key]
	if now.Before(l.until) {
		return 0, 0, RateLimitedError{RetryAfter: l.until.Sub(now)}
	}
	if now.Sub(l.last) >= loginForgetIdle {
		l = lockout{}
	}
	l.fails++
	l.last = now
	wait := loginWait(l.fails)
	if wait > 0 {
		l.until = now.Add(wait)
	}
	s.lockouts[key] = l
	return l.fails, wait, nil
}

func (s *Service) forgetLogin(key string) {
	s.mu.Lock()
	delete(s.lockouts, key)
	s.mu.Unlock()
}

// liftLogin olvida la serie si `kudawiki admin unlock` corrió después de su último intento.
func (s *Service) liftLogin(key string, unlockedAt time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.lockouts[key]
	if !ok || !unlockedAt.After(l.last) {
		return false
	}
	delete(s.lockouts, key)
	return true
}
