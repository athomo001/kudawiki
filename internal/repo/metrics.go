package repo

import (
	"context"
	"time"
)

// Gauges son los valores de la instalación que /metrics publica (§13.5). Conteos globales:
// nada por área, y nunca un título ni una ruta.
type Gauges struct {
	EmailPending  int64 // correos en la cola, sin entregar ni rendidos
	Sessions      int64 // sesiones abiertas
	ActiveUsers   int64
	DatabaseBytes int64
}

func (r *Repo) Gauges(ctx context.Context, now time.Time) (Gauges, error) {
	var g Gauges
	err := r.db.Read.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM email_outbox WHERE sent_at IS NULL AND failed_at IS NULL),
		(SELECT COUNT(*) FROM sessions WHERE last_seen_at >= ?),
		(SELECT COUNT(*) FROM users WHERE is_active = 1),
		(SELECT page_count * page_size FROM pragma_page_count(), pragma_page_size())`, now.Add(-12*time.Hour).UTC()).
		Scan(&g.EmailPending, &g.Sessions, &g.ActiveUsers, &g.DatabaseBytes)
	return g, err
}
