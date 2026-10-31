package repo

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Cuestionarios (§4.16). Un cuestionario existe para el Scope solo si su página existe para él:
// misma área, fuera de la papelera y con un nivel que alcanza (§4.16.7, §3.3). Las respuestas
// correctas viven en quiz_versions.questions_json y en draft_json; armar lo que ve quien responde
// es cosa de quiz.Service, que nunca las manda.

type Quiz struct {
	ID, PageID, PageSlug, PageTitle, PageOwnerID string
	PageRank, PageRevision                       int
	Title, Status                                string
	PublishedVersion                             int // 0 = nunca se publicó
	Draft                                        string
	OpenToReaders                                bool
	CreatedBy, Creator                           string
	CreatedAt, UpdatedAt                         time.Time
}

const quizColumns = `SELECT q.id, q.page_id, p.slug, p.title, COALESCE(p.owner_id, ''), p.min_rank, p.current_revision,
	q.title, q.status, COALESCE(q.published_version, 0), q.draft_json, q.open_to_readers, q.created_by, u.full_name, q.created_at, q.updated_at
	FROM quizzes q JOIN pages p ON p.id = q.page_id JOIN users u ON u.id = q.created_by`

// quizVisible es la condición de la frontera: la página del cuestionario existe para el Scope.
const quizVisible = ` q.workspace_id = ? AND p.deleted_at IS NULL AND p.min_rank <= ?`

func scanQuiz(row scanner) (Quiz, error) {
	var q Quiz
	err := row.Scan(&q.ID, &q.PageID, &q.PageSlug, &q.PageTitle, &q.PageOwnerID, &q.PageRank, &q.PageRevision,
		&q.Title, &q.Status, &q.PublishedVersion, &q.Draft, &q.OpenToReaders, &q.CreatedBy, &q.Creator, &q.CreatedAt, &q.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Quiz{}, ErrNotFound
	}
	return q, err
}

func (r *Repo) CreateQuiz(ctx context.Context, s Scope, q Quiz) error {
	_, err := r.db.Write.ExecContext(ctx,
		`INSERT INTO quizzes (id, workspace_id, page_id, title, draft_json, open_to_readers, created_by) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		q.ID, s.WsID, q.PageID, q.Title, q.Draft, q.OpenToReaders, q.CreatedBy)
	return err
}

func (r *Repo) QuizByID(ctx context.Context, s Scope, id string) (Quiz, error) {
	return scanQuiz(r.db.Read.QueryRowContext(ctx, quizColumns+` WHERE q.id = ? AND`+quizVisible, id, s.WsID, s.MaxRank))
}

// PageQuizzes son los cuestionarios de la página, el más nuevo primero.
func (r *Repo) PageQuizzes(ctx context.Context, s Scope, pageID string) ([]Quiz, error) {
	rows, err := r.db.Read.QueryContext(ctx, quizColumns+` WHERE q.page_id = ? AND`+quizVisible+` ORDER BY q.created_at DESC, q.id DESC`,
		pageID, s.WsID, s.MaxRank)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanQuiz)
}

// UpdateQuiz guarda el borrador: el título, si es abierto y las preguntas con su configuración.
func (r *Repo) UpdateQuiz(ctx context.Context, s Scope, q Quiz) error {
	return affected(r.db.Write.ExecContext(ctx,
		`UPDATE quizzes SET title = ?, draft_json = ?, open_to_readers = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND workspace_id = ?`,
		q.Title, q.Draft, q.OpenToReaders, q.ID, s.WsID))
}

func (r *Repo) SetQuizStatus(ctx context.Context, s Scope, id, status string) error {
	return affected(r.db.Write.ExecContext(ctx,
		`UPDATE quizzes SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND workspace_id = ?`, status, id, s.WsID))
}

// DeleteQuiz borra un cuestionario que nunca se publicó: uno publicado tiene intentos y
// resultados, y se archiva (quiz.Service lo decide).
func (r *Repo) DeleteQuiz(ctx context.Context, s Scope, id string) error {
	return affected(r.db.Write.ExecContext(ctx,
		`DELETE FROM quizzes WHERE id = ? AND workspace_id = ? AND published_version IS NULL`, id, s.WsID))
}

type QuizVersion struct {
	Version, PageRevision int
	Settings, Questions   string
	PublishedAt           time.Time
}

// PublishQuiz congela el borrador como la versión siguiente y la deja como la que se responde.
func (r *Repo) PublishQuiz(ctx context.Context, s Scope, id, by string, v QuizVersion) (int, error) {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM quiz_versions WHERE quiz_id = ?`, id).Scan(&n); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO quiz_versions (quiz_id, version, page_revision_number, settings_json, questions_json, published_by) VALUES (?, ?, ?, ?, ?, ?)`,
		id, n, v.PageRevision, v.Settings, v.Questions, by); err != nil {
		return 0, err
	}
	if err := affected(tx.ExecContext(ctx, `UPDATE quizzes SET status = 'published', published_version = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND workspace_id = ?`, n, id, s.WsID)); err != nil {
		return 0, err
	}
	return n, tx.Commit()
}

// SavePreviewVersion deja el borrador como versión 0, la que responde quien lo prueba.
func (r *Repo) SavePreviewVersion(ctx context.Context, s Scope, id, by string, v QuizVersion) error {
	_, err := r.db.Write.ExecContext(ctx,
		`INSERT INTO quiz_versions (quiz_id, version, page_revision_number, settings_json, questions_json, published_by)
		 SELECT id, 0, ?, ?, ?, ? FROM quizzes WHERE id = ? AND workspace_id = ?
		 ON CONFLICT (quiz_id, version) DO UPDATE SET page_revision_number = excluded.page_revision_number,
		   settings_json = excluded.settings_json, questions_json = excluded.questions_json,
		   published_by = excluded.published_by, published_at = CURRENT_TIMESTAMP`,
		v.PageRevision, v.Settings, v.Questions, by, id, s.WsID)
	return err
}

func (r *Repo) QuizVersion(ctx context.Context, s Scope, quizID string, version int) (QuizVersion, error) {
	var v QuizVersion
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT v.version, v.page_revision_number, v.settings_json, v.questions_json, v.published_at
		 FROM quiz_versions v JOIN quizzes q ON q.id = v.quiz_id
		 WHERE v.quiz_id = ? AND v.version = ? AND q.workspace_id = ?`, quizID, version, s.WsID).
		Scan(&v.Version, &v.PageRevision, &v.Settings, &v.Questions, &v.PublishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}

// QuizAttempt es un intento, con el nombre de quien lo hizo para los resultados.
type QuizAttempt struct {
	ID, QuizID, UserID, Name, Email, RequestID string
	Version, Number                            int
	Preview                                    bool
	Layout, Answers, Status                    string
	StartedAt, DeadlineAt, SubmittedAt         time.Time // cero = sin límite / sin enviar
	Score, Max, Percent                        float64
	Passed                                     bool
}

const attemptColumns = `SELECT a.id, a.quiz_id, a.user_id, u.full_name, u.email, COALESCE(a.request_id, ''), a.version, a.attempt_number,
	a.is_preview, a.layout_json, a.answers_json, a.status, a.started_at, a.deadline_at, a.submitted_at,
	COALESCE(a.score_points, 0), COALESCE(a.max_points, 0), COALESCE(a.score_percent, 0), COALESCE(a.passed, 0)
	FROM quiz_attempts a JOIN users u ON u.id = a.user_id`

func scanAttempt(row scanner) (QuizAttempt, error) {
	var a QuizAttempt
	var deadline, submitted sql.NullTime
	err := row.Scan(&a.ID, &a.QuizID, &a.UserID, &a.Name, &a.Email, &a.RequestID, &a.Version, &a.Number,
		&a.Preview, &a.Layout, &a.Answers, &a.Status, &a.StartedAt, &deadline, &submitted, &a.Score, &a.Max, &a.Percent, &a.Passed)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	a.DeadlineAt, a.SubmittedAt = deadline.Time, submitted.Time
	return a, err
}

// InsertAttempt empieza un intento. Si la persona ya tiene uno abierto (dos "empezar" a la vez),
// el índice único lo frena: ErrConflict.
func (r *Repo) InsertAttempt(ctx context.Context, s Scope, a QuizAttempt) error {
	_, err := r.db.Write.ExecContext(ctx,
		`INSERT INTO quiz_attempts (id, workspace_id, quiz_id, version, user_id, request_id, attempt_number, is_preview, layout_json, started_at, deadline_at)
		 VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?)`,
		a.ID, s.WsID, a.QuizID, a.Version, a.UserID, a.RequestID, a.Number, a.Preview, a.Layout, sqlTime(a.StartedAt), nullTime(a.DeadlineAt))
	return conflict(err)
}

func (r *Repo) AttemptByID(ctx context.Context, s Scope, id string) (QuizAttempt, error) {
	return scanAttempt(r.db.Read.QueryRowContext(ctx, attemptColumns+` WHERE a.id = ? AND a.workspace_id = ?`, id, s.WsID))
}

// OpenAttempt es el intento en curso de la persona en el cuestionario, de prueba o no.
func (r *Repo) OpenAttempt(ctx context.Context, s Scope, quizID, userID string) (QuizAttempt, error) {
	return scanAttempt(r.db.Read.QueryRowContext(ctx, attemptColumns+`
		WHERE a.quiz_id = ? AND a.user_id = ? AND a.workspace_id = ? AND a.status = 'in_progress'`, quizID, userID, s.WsID))
}

// DiscardPreview borra la prueba en curso de la persona: no cuenta, y deja lugar al intento nuevo.
func (r *Repo) DiscardPreview(ctx context.Context, s Scope, quizID, userID string) error {
	_, err := r.db.Write.ExecContext(ctx, `DELETE FROM quiz_attempts
		WHERE quiz_id = ? AND user_id = ? AND workspace_id = ? AND is_preview = 1 AND status = 'in_progress'`, quizID, userID, s.WsID)
	return err
}

// SaveAnswers guarda lo respondido hasta ahora; un intento ya cerrado no cambia.
func (r *Repo) SaveAnswers(ctx context.Context, s Scope, id, answers string) error {
	return affected(r.db.Write.ExecContext(ctx,
		`UPDATE quiz_attempts SET answers_json = ? WHERE id = ? AND workspace_id = ? AND status = 'in_progress'`, answers, id, s.WsID))
}

// AttemptOutcome es el cierre de un intento y lo que trae: el acuse de la solicitud si aprobó y
// los avisos (a quien asignó, si se quedó sin intentos).
type AttemptOutcome struct {
	Attempt       QuizAttempt
	AckRevision   int // > 0: confirma la solicitud del intento con esta revisión
	Notifications []NewNotification
}

// FinishAttempt cierra el intento, en una transacción con su acuse y sus avisos. Si ya estaba
// cerrado (dos envíos a la vez), ErrConflict y no cambia nada.
func (r *Repo) FinishAttempt(ctx context.Context, s Scope, o AttemptOutcome) error {
	a := o.Attempt
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`UPDATE quiz_attempts SET answers_json = ?, status = ?, submitted_at = ?, score_points = ?, max_points = ?, score_percent = ?, passed = ?
		 WHERE id = ? AND workspace_id = ? AND status = 'in_progress'`,
		a.Answers, a.Status, sqlTime(a.SubmittedAt), a.Score, a.Max, a.Percent, a.Passed, a.ID, s.WsID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrConflict
	}
	if o.AckRevision > 0 && a.RequestID != "" {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO read_acknowledgements (request_id, user_id, revision_number) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`,
			a.RequestID, a.UserID, o.AckRevision); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE notifications SET read_at = CURRENT_TIMESTAMP
			WHERE user_id = ? AND group_key = ? AND read_at IS NULL`, a.UserID, "read:"+a.RequestID); err != nil {
			return err
		}
	}
	if err := insertNotifications(ctx, tx, s.WsID, o.Notifications); err != nil {
		return err
	}
	return tx.Commit()
}

// AttemptStats es lo que la persona lleva en un cuestionario por una solicitud (o en el
// cuestionario abierto, requestID vacío): intentos hechos, extra que le dieron y cuándo cerró
// el último.
type AttemptStats struct {
	Used, Extra  int
	LastFinished time.Time
	Passed       bool
}

func (r *Repo) AttemptStats(ctx context.Context, s Scope, quizID, userID, requestID string) (AttemptStats, error) {
	var st AttemptStats
	var last sql.NullString
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT COUNT(*), MAX(submitted_at), COALESCE(MAX(passed), 0),
		   (SELECT COALESCE(SUM(extra), 0) FROM quiz_extra_attempts e WHERE e.quiz_id = ? AND e.user_id = ? AND e.request_id IS NULLIF(?, ''))
		 FROM quiz_attempts WHERE quiz_id = ? AND user_id = ? AND workspace_id = ? AND is_preview = 0 AND request_id IS NULLIF(?, '')`,
		quizID, userID, requestID, quizID, userID, s.WsID, requestID).Scan(&st.Used, &last, &st.Passed, &st.Extra)
	if err == nil && last.Valid {
		st.LastFinished, err = time.Parse(time.DateTime, last.String)
	}
	return st, err
}

// UserAttempts es el historial de la persona en el cuestionario, sin las pruebas, el más nuevo
// primero.
func (r *Repo) UserAttempts(ctx context.Context, s Scope, quizID, userID string) ([]QuizAttempt, error) {
	rows, err := r.db.Read.QueryContext(ctx, attemptColumns+`
		WHERE a.quiz_id = ? AND a.user_id = ? AND a.workspace_id = ? AND a.is_preview = 0 ORDER BY a.started_at DESC, a.id DESC`,
		quizID, userID, s.WsID)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanAttempt)
}

// QuizAttempts son todos los intentos que cuentan del cuestionario, para los resultados.
func (r *Repo) QuizAttempts(ctx context.Context, s Scope, quizID string) ([]QuizAttempt, error) {
	rows, err := r.db.Read.QueryContext(ctx, attemptColumns+`
		WHERE a.quiz_id = ? AND a.workspace_id = ? AND a.is_preview = 0 ORDER BY u.full_name, a.user_id, a.started_at`, quizID, s.WsID)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanAttempt)
}

// ExpiredAttempts son los intentos en curso del cuestionario a los que se les acabó el tiempo:
// se corrigen con lo que alcanzaron a responder (§4.16.4).
func (r *Repo) ExpiredAttempts(ctx context.Context, s Scope, quizID string, now time.Time) ([]QuizAttempt, error) {
	rows, err := r.db.Read.QueryContext(ctx, attemptColumns+`
		WHERE a.quiz_id = ? AND a.workspace_id = ? AND a.status = 'in_progress' AND a.deadline_at IS NOT NULL AND a.deadline_at <= ?`,
		quizID, s.WsID, sqlTime(now))
	if err != nil {
		return nil, err
	}
	return collect(rows, scanAttempt)
}

// GrantExtraAttempts da intentos extra a una persona, con su aviso, en una transacción.
func (r *Repo) GrantExtraAttempts(ctx context.Context, s Scope, quizID, userID, requestID string, extra int, reason, by string, n NewNotification) error {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO quiz_extra_attempts (quiz_id, user_id, request_id, extra, reason, granted_by) VALUES (?, ?, NULLIF(?, ''), ?, ?, ?)`,
		quizID, userID, requestID, extra, reason, by); err != nil {
		return err
	}
	if err := insertNotifications(ctx, tx, s.WsID, []NewNotification{n}); err != nil {
		return err
	}
	return tx.Commit()
}

// OpenQuizRequest es la solicitud abierta, todavía sin cumplir, por la que userID tiene que hacer
// el cuestionario; la más reciente si hay varias.
func (r *Repo) OpenQuizRequest(ctx context.Context, s Scope, quizID, userID string) (ReadRequest, error) {
	return scanReadRequest(r.db.Read.QueryRowContext(ctx, readRequestColumns+`
		JOIN workspace_members m ON m.workspace_id = r.workspace_id AND m.user_id = ?
		WHERE r.quiz_id = ? AND r.workspace_id = ? AND r.closed_at IS NULL AND p.deleted_at IS NULL AND p.min_rank <= ? AND `+recipientSQL+`
		  AND NOT EXISTS (SELECT 1 FROM read_acknowledgements a WHERE a.request_id = r.id AND a.user_id = m.user_id)
		ORDER BY r.created_at DESC, r.id DESC LIMIT 1`, userID, quizID, s.WsID, s.MaxRank))
}

// AckedQuizRequest es la última solicitud que userID ya cumplió aprobando el cuestionario: le deja
// ver su resultado y su historial aunque la solicitud se haya cerrado (§4.16.5). El acuse prueba
// que fue destinatario; el nivel de la página se exige igual.
func (r *Repo) AckedQuizRequest(ctx context.Context, s Scope, quizID, userID string) (ReadRequest, error) {
	return scanReadRequest(r.db.Read.QueryRowContext(ctx, readRequestColumns+`
		JOIN read_acknowledgements a ON a.request_id = r.id AND a.user_id = ?
		WHERE r.quiz_id = ? AND r.workspace_id = ? AND p.deleted_at IS NULL AND p.min_rank <= ?
		ORDER BY r.created_at DESC, r.id DESC LIMIT 1`, userID, quizID, s.WsID, s.MaxRank))
}

// AssignedQuiz: userID pidió alguna lectura con este cuestionario (quien asignó, §4.16.5).
func (r *Repo) AssignedQuiz(ctx context.Context, s Scope, quizID, userID string) (bool, error) {
	var ok bool
	err := r.db.Read.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM read_requests WHERE quiz_id = ? AND workspace_id = ? AND created_by = ?)`,
		quizID, s.WsID, userID).Scan(&ok)
	return ok, err
}

// QuizProgress es lo que lleva cada destinatario de una solicitud con cuestionario.
type QuizProgress struct {
	Used, Extra int
	InProgress  bool
	BestPercent float64
	Passed      bool
}

// RequestQuizProgress es el avance de cada persona en el cuestionario de la solicitud.
func (r *Repo) RequestQuizProgress(ctx context.Context, s Scope, requestID string) (map[string]QuizProgress, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT a.user_id, COUNT(*), MAX(a.status = 'in_progress'), COALESCE(MAX(a.score_percent), 0), COALESCE(MAX(a.passed), 0)
		 FROM quiz_attempts a WHERE a.request_id = ? AND a.workspace_id = ? AND a.is_preview = 0 GROUP BY a.user_id`, requestID, s.WsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]QuizProgress{}
	for rows.Next() {
		var u string
		var p QuizProgress
		if err := rows.Scan(&u, &p.Used, &p.InProgress, &p.BestPercent, &p.Passed); err != nil {
			return nil, err
		}
		out[u] = p
	}
	// Los intentos extra van aparte: también los tiene quien todavía no empezó.
	extra, err := r.db.Read.QueryContext(ctx, `SELECT user_id, SUM(extra) FROM quiz_extra_attempts WHERE request_id = ? GROUP BY user_id`, requestID)
	if err != nil {
		return nil, err
	}
	defer extra.Close()
	for extra.Next() {
		var u string
		var n int
		if err := extra.Scan(&u, &n); err != nil {
			return nil, err
		}
		p := out[u]
		p.Extra = n
		out[u] = p
	}
	return out, errors.Join(rows.Err(), extra.Err())
}

// MyQuizAttempt es un intento propio en cualquiera de las áreas de la persona (/me/quizzes).
type MyQuizAttempt struct {
	QuizAttempt
	QuizTitle, AreaSlug, AreaName, PageSlug string
}

// MyQuizAttempts es el historial de la persona en todas sus áreas, sin pruebas, mientras pueda
// leer la página.
func (r *Repo) MyQuizAttempts(ctx context.Context, userID string) ([]MyQuizAttempt, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT a.id, a.quiz_id, a.status, a.started_at, a.submitted_at, COALESCE(a.score_percent, 0), COALESCE(a.passed, 0),
		   q.title, w.slug, w.name, p.slug
		 FROM quiz_attempts a
		 JOIN quizzes q ON q.id = a.quiz_id
		 JOIN pages p ON p.id = q.page_id
		 JOIN workspaces w ON w.id = a.workspace_id
		 JOIN workspace_members m ON m.workspace_id = a.workspace_id AND m.user_id = a.user_id
		 WHERE a.user_id = ? AND a.is_preview = 0 AND w.kind = 'area' AND w.quizzes_enabled = 1 AND p.deleted_at IS NULL AND `+rankSQL+` >= p.min_rank
		 ORDER BY a.started_at DESC, a.id DESC LIMIT 100`, userID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (MyQuizAttempt, error) {
		var x MyQuizAttempt
		var submitted sql.NullTime
		err := row.Scan(&x.ID, &x.QuizID, &x.Status, &x.StartedAt, &submitted, &x.Percent, &x.Passed, &x.QuizTitle, &x.AreaSlug, &x.AreaName, &x.PageSlug)
		x.SubmittedAt = submitted.Time
		return x, err
	})
}
