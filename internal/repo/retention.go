package repo

import (
	"context"
	"time"
)

// Retention son los cortes de los datos personales con fecha de vencimiento (§13.10): lo que
// tiene fecha anterior al corte se borra.
type Retention struct {
	SessionSeen, SessionCreated time.Time // sesiones vencidas por inactividad o por edad (§8.2)
	NotificationRead            time.Time // notificaciones ya leídas
	LoginHistory                time.Time // lo visto para el aviso de inicio de sesión nuevo (§8.2)
	QuizResults                 time.Time // intentos de cuestionarios y aperturas de páginas con acuse (§4.14, §4.16)
}

// PurgePersonal borra lo vencido y devuelve cuántas filas por tipo, para la auditoría.
func (r *Repo) PurgePersonal(ctx context.Context, c Retention) (map[string]int64, error) {
	out := map[string]int64{}
	for _, q := range []struct {
		kind, sql string
		args      []any
	}{
		{"sessions", `DELETE FROM sessions WHERE last_seen_at < ? OR created_at < ?`, []any{c.SessionSeen.UTC(), c.SessionCreated.UTC()}},
		{"notifications", `DELETE FROM notifications WHERE read_at IS NOT NULL AND read_at < ?`, []any{c.NotificationRead.UTC()}},
		{"login_history", `DELETE FROM login_history WHERE last_at < ?`, []any{c.LoginHistory.UTC()}},
		// Un intento en curso no se toca: lo cierra su tiempo límite. Las pruebas de quien
		// administra el cuestionario tampoco son de nadie, pero vencen igual.
		{"quiz_attempts", `DELETE FROM quiz_attempts WHERE status != 'in_progress' AND COALESCE(submitted_at, started_at) < ?`, []any{c.QuizResults.UTC()}},
		{"read_views", `DELETE FROM read_request_views WHERE last_opened_at < ?`, []any{c.QuizResults.UTC()}},
	} {
		res, err := r.db.Write.ExecContext(ctx, q.sql, q.args...)
		if err != nil {
			return out, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			out[q.kind] = n
		}
	}
	return out, nil
}
