package ldap

import (
	"context"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
)

// Estado y log de depuración (§8.6.5): lo que hace falta para saber por qué falla, sin abrir el
// servidor. Vive en memoria; la última sincronización sale de la auditoría, así también se ve
// la que corrió `kudawiki ldap sync` desde la consola.

// debugFor es cuánto dura el log de depuración: se apaga solo, para que no quede encendido
// registrando cada login.
const debugFor = 30 * time.Minute

// Status es el estado del directorio para el panel y /readyz.
type Status struct {
	Enabled    bool         `json:"enabled"`
	LastLogin  *time.Time   `json:"last_login,omitempty"`
	LastSync   *SyncSummary `json:"last_sync,omitempty"`
	LastError  *StatusError `json:"last_error,omitempty"`
	CertDays   *int         `json:"cert_days,omitempty"`
	DebugUntil *time.Time   `json:"debug_until,omitempty"`
}

// SyncSummary es la última sincronización: cuándo, si se aplicó o se abortó y por qué.
type SyncSummary struct {
	At      time.Time `json:"at"`
	Aborted string    `json:"aborted,omitempty"`
}

// StatusError es el último error del directorio, solo con su código: el detalle (que puede
// traer un DN) va al log técnico.
type StatusError struct {
	At   time.Time `json:"at"`
	Code string    `json:"code"`
}

type status struct {
	lastLogin  time.Time
	lastErr    StatusError
	certAfter  time.Time
	debugUntil time.Time
	lastSync   *SyncSummary
}

func (s *Service) loggedIn() {
	s.mu.Lock()
	s.st.lastLogin = s.now()
	s.mu.Unlock()
}

func (s *Service) failed(code string) {
	s.mu.Lock()
	s.st.lastErr = StatusError{At: s.now(), Code: code}
	s.mu.Unlock()
}

func (s *Service) synced(sum SyncSummary) {
	s.mu.Lock()
	s.st.lastSync = &sum
	s.mu.Unlock()
}

// Status arma el estado. Con fresh, la última sincronización se relee de la auditoría (el
// panel); sin él, vale la que vio este proceso (/readyz, que se consulta seguido).
func (s *Service) Status(ctx context.Context, fresh bool) Status {
	c, _ := s.settings(ctx, nil, false)
	out := Status{Enabled: c.Enabled && c.complete() == nil}
	if fresh {
		if at, aborted, err := s.store.LastLDAPSync(ctx); err == nil && !at.IsZero() {
			s.mu.Lock()
			if s.st.lastSync == nil || s.st.lastSync.At.Before(at) {
				s.st.lastSync = &SyncSummary{At: at, Aborted: aborted}
			}
			s.mu.Unlock()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.st.lastLogin.IsZero() {
		out.LastLogin = new(s.st.lastLogin)
	}
	if s.st.lastSync != nil {
		out.LastSync = new(*s.st.lastSync)
	}
	if !s.st.lastErr.At.IsZero() {
		out.LastError = new(s.st.lastErr)
	}
	if !s.st.certAfter.IsZero() {
		out.CertDays = new(int(s.st.certAfter.Sub(s.now()).Hours() / 24))
	}
	if s.now().Before(s.st.debugUntil) {
		out.DebugUntil = new(s.st.debugUntil)
	}
	return out
}

// SetDebug enciende el log de depuración por 30 minutos, o lo apaga. Queda en la auditoría como
// un cambio de la configuración.
func (s *Service) SetDebug(ctx context.Context, on bool) time.Time {
	var until time.Time
	if on {
		until = s.now().Add(debugFor)
	}
	s.mu.Lock()
	s.st.debugUntil = until
	s.mu.Unlock()
	s.audit.Record(ctx, audit.Event{Action: audit.LDAPConfigUpdate, TargetType: "site",
		Meta: map[string]any{"keys": []string{"debug"}, "on": on}})
	return until
}

// debug registra un paso del login o de la sincronización mientras el log de depuración esté
// encendido. Nunca contraseñas ni atributos que no estén mapeados: quien llama pasa solo el
// filtro, el DN, conteos y códigos.
func (s *Service) debug(msg string, args ...any) {
	s.mu.Lock()
	on := s.now().Before(s.st.debugUntil)
	s.mu.Unlock()
	if on {
		s.log.Info("ldap debug: "+msg, args...)
	}
}
