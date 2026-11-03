package repo

import (
	"context"
	"database/sql"
	"errors"
)

// JobState devuelve el estado guardado de un job del sistema (JSON), o "" si nunca corrió.
func (r *Repo) JobState(ctx context.Context, name string) (string, error) {
	var v string
	err := r.db.Read.QueryRowContext(ctx, `SELECT value_json FROM job_state WHERE name = ?`, name).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetJobState guarda el estado de un job del sistema.
func (r *Repo) SetJobState(ctx context.Context, name, valueJSON string) error {
	_, err := r.db.Write.ExecContext(ctx, `INSERT INTO job_state (name, value_json, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT (name) DO UPDATE SET value_json = excluded.value_json, updated_at = excluded.updated_at`, name, valueJSON)
	return err
}
