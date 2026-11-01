package repo

import (
	"context"
	"time"
)

// SessionsOf son las sesiones de una persona, vencidas o no: quien pregunta decide cuáles valen
// (§8.2).
func (r *Repo) SessionsOf(ctx context.Context, userID string) ([]Session, error) {
	rows, err := r.db.Read.QueryContext(ctx, `SELECT token_hash, id, created_at, last_seen_at, ip, user_agent
		FROM sessions WHERE user_id = ? ORDER BY last_seen_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		s := Session{UserID: userID}
		if err := rows.Scan(&s.TokenHash, &s.ID, &s.CreatedAt, &s.LastSeenAt, &s.IP, &s.UserAgent); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeleteUserSession cierra una sesión por su id, solo si es de esa persona: la de otra da
// ErrNotFound, igual que una que no existe.
func (r *Repo) DeleteUserSession(ctx context.Context, userID, id string) error {
	res, err := r.db.Write.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND id = ?`, userID, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		err = ErrNotFound
	}
	return err
}

// RecordLogin anota la IP y el navegador de un inicio de sesión y dice si alguno de los dos es
// nuevo para la persona. El primero de todos no cuenta como nuevo: no hay con qué comparar.
func (r *Repo) RecordLogin(ctx context.Context, userID, ip, browser string, at time.Time) (bool, error) {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var any, ipSeen, browserSeen bool
	if err := tx.QueryRowContext(ctx, `SELECT count(*) > 0, coalesce(max(ip = ?), 0), coalesce(max(browser = ?), 0)
		FROM login_history WHERE user_id = ?`, ip, browser, userID).Scan(&any, &ipSeen, &browserSeen); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO login_history (user_id, ip, browser, last_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (user_id, ip, browser) DO UPDATE SET last_at = excluded.last_at`, userID, ip, browser, at.UTC()); err != nil {
		return false, err
	}
	return any && (!ipSeen || !browserSeen), tx.Commit()
}
