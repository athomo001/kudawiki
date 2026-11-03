package repo

import (
	"context"
	"database/sql"
	"time"
)

// PersonalSection es una parte de lo que la wiki tiene de una persona (§13.10): un nombre de
// archivo y sus filas, tal como salen de la BD.
type PersonalSection struct {
	Name string
	Rows []map[string]any
}

// PersonalData junta lo que la wiki guarda de una persona, para `kudawiki privacy export`. Sin
// secretos (ni hashes de contraseña o de sesión, ni el secreto TOTP, ni llaves de bóveda) y sin
// contenido de las áreas: sus revisiones son de la organización y solo se cuentan.
func (r *Repo) PersonalData(ctx context.Context, userID string) ([]PersonalSection, error) {
	queries := []struct{ name, sql string }{
		{"profile", `SELECT u.id, u.email, u.full_name, u.auth_source, COALESCE(u.ldap_dn, '') AS ldap_dn, u.is_global_admin, u.is_active, u.locale,
			u.created_at, u.updated_at, u.password_hash IS NOT NULL AS has_password
			FROM users u WHERE u.id = ?`},
		{"memberships", `SELECT w.slug AS area, w.name, m.role FROM workspace_members m JOIN workspaces w ON w.id = m.workspace_id
			WHERE m.user_id = ? AND w.kind = 'area' ORDER BY w.slug`},
		{"sessions", `SELECT id, created_at, last_seen_at, ip, user_agent FROM sessions WHERE user_id = ? ORDER BY created_at`},
		{"login_history", `SELECT ip, browser, last_at FROM login_history WHERE user_id = ? ORDER BY last_at`},
		{"notifications", `SELECT n.kind, n.category, w.slug AS area, n.payload_json, n.created_at, n.read_at, n.emailed_at
			FROM notifications n JOIN workspaces w ON w.id = n.workspace_id WHERE n.user_id = ? ORDER BY n.created_at`},
		{"quiz_attempts", `SELECT w.slug AS area, q.title AS quiz, a.version, a.attempt_number, a.status, a.started_at, a.submitted_at,
			a.score_percent, a.passed FROM quiz_attempts a JOIN quizzes q ON q.id = a.quiz_id JOIN workspaces w ON w.id = a.workspace_id
			WHERE a.user_id = ? AND a.is_preview = 0 ORDER BY a.started_at`},
		{"read_acknowledgements", `SELECT w.slug AS area, p.slug AS page, k.revision_number, k.acknowledged_at
			FROM read_acknowledgements k JOIN read_requests q ON q.id = k.request_id JOIN pages p ON p.id = q.page_id
			JOIN workspaces w ON w.id = q.workspace_id WHERE k.user_id = ? ORDER BY k.acknowledged_at`},
		{"read_views", `SELECT w.slug AS area, p.slug AS page, v.first_opened_at, v.last_opened_at, v.open_count
			FROM read_request_views v JOIN read_requests q ON q.id = v.request_id JOIN pages p ON p.id = q.page_id
			JOIN workspaces w ON w.id = q.workspace_id WHERE v.user_id = ? ORDER BY v.first_opened_at`},
		{"revisions_authored", `SELECT w.slug AS area, COUNT(*) AS revisions FROM page_revisions r JOIN workspaces w ON w.id = r.workspace_id
			WHERE r.created_by = ? AND w.kind = 'area' GROUP BY w.slug ORDER BY w.slug`},
		{"audit", `SELECT a.at, a.action, COALESCE(w.slug, '') AS area, a.target_type, a.target_id, a.meta_json, a.actor_snapshot_json
			FROM audit_log a LEFT JOIN workspaces w ON w.id = a.workspace_id WHERE a.actor_id = ? ORDER BY a.id`},
	}
	out := make([]PersonalSection, 0, len(queries))
	for _, q := range queries {
		rows, err := r.db.Read.QueryContext(ctx, q.sql, userID)
		if err != nil {
			return nil, err
		}
		list, err := mapRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, PersonalSection{Name: q.name, Rows: list})
	}
	return out, nil
}

// mapRows lee filas a mapas columna → valor; los []byte salen como texto.
func mapRows(rows *sql.Rows) ([]map[string]any, error) {
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	list := []map[string]any{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		m := make(map[string]any, len(cols))
		for i, c := range cols {
			switch v := vals[i].(type) {
			case []byte:
				m[c] = string(v)
			case time.Time:
				m[c] = v.UTC()
			default:
				m[c] = v
			}
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

// PersonalWorkspaceOf es el espacio personal de una persona, si tiene.
func (r *Repo) PersonalWorkspaceOf(ctx context.Context, userID string) (Workspace, error) {
	var id string
	err := r.db.Read.QueryRowContext(ctx, `SELECT id FROM workspaces WHERE kind = 'personal' AND owner_user_id = ?`, userID).Scan(&id)
	if err == sql.ErrNoRows {
		return Workspace{}, ErrNotFound
	}
	if err != nil {
		return Workspace{}, err
	}
	return r.WorkspaceByID(ctx, id)
}
