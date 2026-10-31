package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Acuse de lectura (§4.14). Los destinatarios de una solicitud (r) son los miembros (m) activos
// que calzan con su audience_json, alcanzan el nivel de la página (p) y no la pidieron. Se
// calculan siempre en el momento: no hay lista copiada que se desactualice (§17).
const audienceSQL = `(json_extract(r.audience_json, '$.all') = 1
	OR m.role IN (SELECT value FROM json_each(r.audience_json, '$.roles'))
	OR m.user_id IN (SELECT value FROM json_each(r.audience_json, '$.users')))`

const recipientSQL = audienceSQL + ` AND ` + rankSQL + ` >= p.min_rank AND m.user_id <> r.created_by
	AND EXISTS (SELECT 1 FROM users ru WHERE ru.id = m.user_id AND ru.is_active = 1)`

// Audience es a quién va una solicitud: todo el área, unos roles o unas personas.
type Audience struct {
	All   bool     `json:"all,omitempty"`
	Roles []string `json:"roles,omitempty"`
	Users []string `json:"users,omitempty"`
}

type ReadRequest struct {
	ID, PageID, PageSlug, PageTitle string
	PageRank, Revision              int
	Statement                       string
	Audience                        Audience
	DueAt, CreatedAt, ClosedAt      time.Time // cero = sin plazo / abierta
	CreatedBy, Creator              string
	QuizID                          string // vacío = solo confirmar; si no, se cumple al aprobar (§4.16)
}

const readRequestColumns = `SELECT r.id, r.page_id, p.slug, p.title, p.min_rank, r.revision_number, COALESCE(r.statement, ''), r.audience_json,
	r.due_at, r.created_at, r.closed_at, r.created_by, u.full_name, COALESCE(r.quiz_id, '')
	FROM read_requests r JOIN pages p ON p.id = r.page_id JOIN users u ON u.id = r.created_by`

func scanReadRequest(row scanner) (ReadRequest, error) {
	var rr ReadRequest
	var aud string
	var due, closed sql.NullTime
	err := row.Scan(&rr.ID, &rr.PageID, &rr.PageSlug, &rr.PageTitle, &rr.PageRank, &rr.Revision, &rr.Statement, &aud,
		&due, &rr.CreatedAt, &closed, &rr.CreatedBy, &rr.Creator, &rr.QuizID)
	if errors.Is(err, sql.ErrNoRows) {
		return ReadRequest{}, ErrNotFound
	}
	if err != nil {
		return rr, err
	}
	rr.DueAt, rr.ClosedAt = due.Time, closed.Time
	return rr, json.Unmarshal([]byte(aud), &rr.Audience)
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return sqlTime(t)
}

// CreateReadRequest guarda la solicitud. Quién puede pedirla y a quién lo decide reading.Service.
func (r *Repo) CreateReadRequest(ctx context.Context, s Scope, rr ReadRequest) error {
	aud, err := json.Marshal(rr.Audience)
	if err != nil {
		return err
	}
	_, err = r.db.Write.ExecContext(ctx,
		`INSERT INTO read_requests (id, workspace_id, page_id, revision_number, statement, audience_json, due_at, created_by, quiz_id)
		 VALUES (?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, NULLIF(?, ''))`,
		rr.ID, s.WsID, rr.PageID, rr.Revision, rr.Statement, string(aud), nullTime(rr.DueAt), rr.CreatedBy, rr.QuizID)
	return err
}

// ReadRequestByID: la de una página que el Scope no ve, o en la papelera, no existe.
func (r *Repo) ReadRequestByID(ctx context.Context, s Scope, id string) (ReadRequest, error) {
	return scanReadRequest(r.db.Read.QueryRowContext(ctx, readRequestColumns+`
		WHERE r.id = ? AND r.workspace_id = ? AND p.deleted_at IS NULL AND p.min_rank <= ?`, id, s.WsID, s.MaxRank))
}

// ReadRequests lista las solicitudes del área que el Scope ve, la más nueva primero. Con
// createdBy, solo las de esa persona (lo que ve un editor, §4.14).
func (r *Repo) ReadRequests(ctx context.Context, s Scope, createdBy string) ([]ReadRequest, error) {
	q := readRequestColumns + ` WHERE r.workspace_id = ? AND p.deleted_at IS NULL AND p.min_rank <= ?`
	args := []any{s.WsID, s.MaxRank}
	if createdBy != "" {
		q += ` AND r.created_by = ?`
		args = append(args, createdBy)
	}
	rows, err := r.db.Read.QueryContext(ctx, q+` ORDER BY r.created_at DESC, r.id DESC LIMIT 200`, args...)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanReadRequest)
}

// Recipient es un destinatario con lo que hizo: cuándo abrió la página y cuándo confirmó.
type Recipient struct {
	UserID, Name, Email, Role  string
	AckAt, FirstOpen, LastOpen time.Time // cero = todavía no
	AckRevision, Opens         int
}

// Recipients son los destinatarios de hoy de la solicitud, por nombre.
func (r *Repo) Recipients(ctx context.Context, s Scope, requestID string) ([]Recipient, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT u.id, u.full_name, u.email, m.role, a.acknowledged_at, COALESCE(a.revision_number, 0),
		   v.first_opened_at, v.last_opened_at, COALESCE(v.open_count, 0)
		 FROM read_requests r
		 JOIN pages p ON p.id = r.page_id
		 JOIN workspace_members m ON m.workspace_id = r.workspace_id
		 JOIN users u ON u.id = m.user_id
		 LEFT JOIN read_acknowledgements a ON a.request_id = r.id AND a.user_id = m.user_id
		 LEFT JOIN read_request_views v ON v.request_id = r.id AND v.user_id = m.user_id
		 WHERE r.id = ? AND r.workspace_id = ? AND `+recipientSQL+`
		 ORDER BY u.full_name, u.id`, requestID, s.WsID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (Recipient, error) {
		var x Recipient
		var ack, first, last sql.NullTime
		err := row.Scan(&x.UserID, &x.Name, &x.Email, &x.Role, &ack, &x.AckRevision, &first, &last, &x.Opens)
		x.AckAt, x.FirstOpen, x.LastOpen = ack.Time, first.Time, last.Time
		return x, err
	})
}

// AudienceSize cuenta cuántos destinatarios tendría hoy una solicitud sobre la página: sirve para
// rechazar, antes de crearla, una que no le llegaría a nadie.
func (r *Repo) AudienceSize(ctx context.Context, s Scope, pageID, createdBy string, a Audience) (int, error) {
	aud, err := json.Marshal(a)
	if err != nil {
		return 0, err
	}
	var n int
	err = r.db.Read.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM (SELECT ? AS audience_json, ? AS created_by) r
		 JOIN pages p ON p.id = ?
		 JOIN workspace_members m ON m.workspace_id = ?
		 WHERE `+recipientSQL, string(aud), createdBy, pageID, s.WsID).Scan(&n)
	return n, err
}

func (r *Repo) CloseReadRequest(ctx context.Context, s Scope, id string) error {
	return affected(r.db.Write.ExecContext(ctx,
		`UPDATE read_requests SET closed_at = CURRENT_TIMESTAMP WHERE id = ? AND workspace_id = ? AND closed_at IS NULL`, id, s.WsID))
}

// MyRead es una solicitud abierta para quien lee la página, con su confirmación si ya la hizo.
type MyRead struct {
	ID, Statement, Creator string
	QuizID                 string
	Revision, AckRevision  int
	DueAt, AckAt           time.Time
}

// MyReads son las solicitudes abiertas sobre la página de las que userID es destinatario.
func (r *Repo) MyReads(ctx context.Context, s Scope, userID, pageID string) ([]MyRead, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT r.id, COALESCE(r.statement, ''), u.full_name, COALESCE(r.quiz_id, ''), r.revision_number, COALESCE(a.revision_number, 0), r.due_at, a.acknowledged_at
		 FROM read_requests r
		 JOIN pages p ON p.id = r.page_id
		 JOIN users u ON u.id = r.created_by
		 JOIN workspace_members m ON m.workspace_id = r.workspace_id AND m.user_id = ?
		 LEFT JOIN read_acknowledgements a ON a.request_id = r.id AND a.user_id = m.user_id
		 WHERE r.workspace_id = ? AND r.page_id = ? AND r.closed_at IS NULL AND p.deleted_at IS NULL AND p.min_rank <= ? AND `+recipientSQL+`
		 ORDER BY r.created_at`, userID, s.WsID, pageID, s.MaxRank)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanMyRead)
}

func scanMyRead(row scanner) (MyRead, error) {
	var x MyRead
	var due, ack sql.NullTime
	err := row.Scan(&x.ID, &x.Statement, &x.Creator, &x.QuizID, &x.Revision, &x.AckRevision, &due, &ack)
	x.DueAt, x.AckAt = due.Time, ack.Time
	return x, err
}

// RecordReadViews anota que userID abrió la página (en la revisión rev) para cada solicitud.
func (r *Repo) RecordReadViews(ctx context.Context, userID string, rev int, requestIDs []string, at time.Time) error {
	for _, id := range requestIDs {
		if _, err := r.db.Write.ExecContext(ctx,
			`INSERT INTO read_request_views (request_id, user_id, first_opened_at, last_opened_at, last_revision_number)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT (request_id, user_id) DO UPDATE SET last_opened_at = excluded.last_opened_at,
			   open_count = open_count + 1, last_revision_number = excluded.last_revision_number`,
			id, userID, sqlTime(at), sqlTime(at), rev); err != nil {
			return err
		}
	}
	return nil
}

// Acknowledge guarda la confirmación con la revisión leída; confirmar dos veces no cambia nada.
// Da por leído el aviso de la solicitud.
func (r *Repo) Acknowledge(ctx context.Context, requestID, userID string, rev int) error {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO read_acknowledgements (request_id, user_id, revision_number) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`,
		requestID, userID, rev); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notifications SET read_at = CURRENT_TIMESTAMP
		WHERE user_id = ? AND group_key = ? AND read_at IS NULL`, userID, "read:"+requestID); err != nil {
		return err
	}
	return tx.Commit()
}

// PendingRead es una lectura por confirmar en cualquiera de las áreas de la persona (/me).
type PendingRead struct {
	MyRead
	AreaSlug, AreaName, PageSlug, PageTitle string
}

// PendingReads son las solicitudes abiertas sin confirmar de userID, en todas sus áreas: las que
// tienen plazo primero, por plazo.
func (r *Repo) PendingReads(ctx context.Context, userID string) ([]PendingRead, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT r.id, COALESCE(r.statement, ''), u.full_name, COALESCE(r.quiz_id, ''), r.revision_number, 0, r.due_at, NULL, w.slug, w.name, p.slug, p.title
		 FROM read_requests r
		 JOIN pages p ON p.id = r.page_id
		 JOIN workspaces w ON w.id = r.workspace_id
		 JOIN users u ON u.id = r.created_by
		 JOIN workspace_members m ON m.workspace_id = r.workspace_id AND m.user_id = ?
		 WHERE r.closed_at IS NULL AND p.deleted_at IS NULL AND w.kind = 'area' AND `+recipientSQL+`
		   AND NOT EXISTS (SELECT 1 FROM read_acknowledgements a WHERE a.request_id = r.id AND a.user_id = m.user_id)
		 ORDER BY r.due_at IS NULL, r.due_at, r.created_at LIMIT 200`, userID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (PendingRead, error) {
		var x PendingRead
		var due, ack sql.NullTime
		err := row.Scan(&x.ID, &x.Statement, &x.Creator, &x.QuizID, &x.Revision, &x.AckRevision, &due, &ack, &x.AreaSlug, &x.AreaName, &x.PageSlug, &x.PageTitle)
		x.DueAt = due.Time
		return x, err
	})
}

// ReadNotice es lo que el worker tiene que avisar de una solicitud: que llegó (Reminder false) o
// que su plazo está cerca. Users son los destinatarios que todavía no confirmaron.
type ReadNotice struct {
	ID, WsID, PageID, Title string
	Quiz                    bool // la solicitud lleva cuestionario: el aviso dice que hay que hacerlo
	DueAt                   time.Time
	Reminder                bool
	Users                   []string
}

// ReadNotices son las solicitudes abiertas sin avisar y las que entran en la ventana del
// recordatorio (faltan menos de window para el plazo). Una que se avisó ya dentro de la ventana no
// se recuerda: serían dos avisos seguidos.
func (r *Repo) ReadNotices(ctx context.Context, now time.Time, window time.Duration) ([]ReadNotice, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT r.id, r.workspace_id, r.page_id, p.title, r.quiz_id IS NOT NULL, r.due_at, r.notified_at IS NOT NULL, m.user_id
		 FROM read_requests r
		 JOIN pages p ON p.id = r.page_id
		 JOIN workspace_members m ON m.workspace_id = r.workspace_id
		 WHERE r.closed_at IS NULL AND p.deleted_at IS NULL AND `+recipientSQL+`
		   AND (r.notified_at IS NULL OR (r.reminded_at IS NULL AND r.due_at > ? AND r.due_at <= ?
		     AND julianday(r.due_at) - julianday(r.notified_at) > ?))
		   AND NOT EXISTS (SELECT 1 FROM read_acknowledgements a WHERE a.request_id = r.id AND a.user_id = m.user_id)
		 ORDER BY r.id, m.user_id`, sqlTime(now), sqlTime(now.Add(window)), window.Hours()/24)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReadNotice
	for rows.Next() {
		var n ReadNotice
		var due sql.NullTime
		var user string
		if err := rows.Scan(&n.ID, &n.WsID, &n.PageID, &n.Title, &n.Quiz, &due, &n.Reminder, &user); err != nil {
			return nil, err
		}
		n.DueAt = due.Time
		if len(out) > 0 && out[len(out)-1].ID == n.ID {
			out[len(out)-1].Users = append(out[len(out)-1].Users, user)
			continue
		}
		n.Users = []string{user}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ApplyReadNotice guarda los avisos y marca la solicitud como avisada (o recordada), en una
// transacción: un aviso se reparte una sola vez.
func (r *Repo) ApplyReadNotice(ctx context.Context, n ReadNotice, ns []NewNotification, at time.Time) error {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertNotifications(ctx, tx, n.WsID, ns); err != nil {
		return err
	}
	col := "notified_at"
	if n.Reminder {
		col = "reminded_at"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE read_requests SET `+col+` = ? WHERE id = ?`, sqlTime(at), n.ID); err != nil {
		return err
	}
	return tx.Commit()
}
