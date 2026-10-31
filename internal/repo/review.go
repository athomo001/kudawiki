package repo

import (
	"context"
	"database/sql"
	"time"
)

// Vigencia del contenido (SPEC §4.8). Las consultas de una persona filtran por el Scope, como
// las de las páginas; las del worker y las de la salud son del sistema.

// reviewDueSQL es el vencimiento de la página p: su última verificación más su intervalo o, sin
// él, el del área. NULL si no vence. Va con una subconsulta y no con un JOIN para poder sumarla a
// cualquier consulta sobre pages (la búsqueda, la cola, el barrido). En el espacio personal no
// vence nada (§7): son borradores, y la vigencia empieza a correr al publicarlos en un área.
const reviewDueSQL = `(SELECT CASE WHEN w.kind = 'area' THEN datetime(p.verified_at, '+' || COALESCE(p.review_interval_days, w.default_review_days) || ' days') END
	FROM workspaces w WHERE w.id = p.workspace_id)`

// verify deja la página verificada por userID, ahora, y da por leídos los avisos de vencida
// que tenía: ya no hay nada que revisar.
func verify(ctx context.Context, tx *sql.Tx, pageID, userID string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE pages SET verified_at = CURRENT_TIMESTAMP, verified_by = ? WHERE id = ?`, userID, pageID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE notifications SET read_at = CURRENT_TIMESTAMP
		WHERE page_id = ? AND category = 'review' AND read_at IS NULL`, pageID)
	return err
}

// VerifyPage es "Marcar como vigente": confirma el contenido sin editarlo.
func (r *Repo) VerifyPage(ctx context.Context, s Scope, pageID, userID string) error {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRowContext(ctx, `SELECT id FROM pages WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL AND min_rank <= ?`,
		pageID, s.WsID, s.MaxRank).Scan(&id)
	if err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if err := verify(ctx, tx, id, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// SetPageReview cambia el dueño y el intervalo de la página (0 = el del área).
func (r *Repo) SetPageReview(ctx context.Context, s Scope, pageID, ownerID string, days int) error {
	res, err := r.db.Write.ExecContext(ctx, `UPDATE pages SET owner_id = ?, review_interval_days = NULLIF(?, 0)
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL AND min_rank <= ?`, ownerID, days, pageID, s.WsID, s.MaxRank)
	return affected(res, err)
}

// OwnerRank es el rango en el área de quien sería dueño: 0 si no es miembro o está desactivado.
func (r *Repo) OwnerRank(ctx context.Context, s Scope, userID string) (int, error) {
	var role string
	err := r.db.Read.QueryRowContext(ctx, `SELECT m.role FROM workspace_members m JOIN users u ON u.id = m.user_id
		WHERE m.workspace_id = ? AND m.user_id = ? AND u.is_active = 1`, s.WsID, userID).Scan(&role)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return RoleRank(role), err
}

// ReviewItem es una página de la cola de revisión.
type ReviewItem struct {
	Slug, Title, Owner string
	MinRank            int
	VerifiedAt, DueAt  time.Time
}

// ReviewQueue son las páginas vencidas del área que el Scope ve, las más atrasadas primero. Con
// ownerID, solo las de esa persona ("Mis páginas por revisar").
func (r *Repo) ReviewQueue(ctx context.Context, s Scope, ownerID string, now time.Time) ([]ReviewItem, error) {
	q := `SELECT p.slug, p.title, COALESCE(o.full_name, ''), p.min_rank, p.verified_at, ` + reviewDueSQL + ` AS due
		FROM pages p LEFT JOIN users o ON o.id = p.owner_id
		WHERE p.workspace_id = ? AND p.deleted_at IS NULL AND p.min_rank <= ? AND ` + reviewDueSQL + ` <= ?`
	args := []any{s.WsID, s.MaxRank, sqlTime(now)}
	if ownerID != "" {
		q += ` AND p.owner_id = ?`
		args = append(args, ownerID)
	}
	rows, err := r.db.Read.QueryContext(ctx, q+` ORDER BY due, p.slug LIMIT 500`, args...)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (ReviewItem, error) {
		var it ReviewItem
		var due string
		if err := row.Scan(&it.Slug, &it.Title, &it.Owner, &it.MinRank, &it.VerifiedAt, &due); err != nil {
			return it, err
		}
		it.DueAt, err = time.Parse(time.DateTime, due)
		return it, err
	})
}

// DueReview es una página vencida cuyo dueño puede leerla, para el aviso al dueño (§4.8).
// Noticed dice si ya se le avisó en este ciclo (desde la última verificación) y NoticedAt, cuándo.
type DueReview struct {
	PageID, WsID, Title, OwnerID string
	DueAt                        time.Time
	Noticed                      bool
	NoticedAt                    time.Time
}

// DueReviews son las páginas vencidas de todas las áreas con un dueño activo que todavía puede
// leerlas: a quien ya no la ve no le llega ni el título (§17). Es del worker y no lleva Scope.
func (r *Repo) DueReviews(ctx context.Context, now time.Time) ([]DueReview, error) {
	rows, err := r.db.Read.QueryContext(ctx, `SELECT p.id, p.workspace_id, p.title, p.owner_id, `+reviewDueSQL+`,
		COALESCE(julianday(p.review_notified_at) >= julianday(p.verified_at), 0), COALESCE(p.review_notified_at, '')
		FROM pages p
		JOIN users u ON u.id = p.owner_id AND u.is_active = 1
		JOIN workspace_members m ON m.workspace_id = p.workspace_id AND m.user_id = p.owner_id
		WHERE p.deleted_at IS NULL AND `+rankSQL+` >= p.min_rank AND `+reviewDueSQL+` <= ?
		ORDER BY p.workspace_id, p.owner_id, p.slug`, sqlTime(now))
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (DueReview, error) {
		var d DueReview
		var due, noticed string
		if err := row.Scan(&d.PageID, &d.WsID, &d.Title, &d.OwnerID, &due, &d.Noticed, &noticed); err != nil {
			return d, err
		}
		if d.Noticed {
			// Lo escribe ApplyReviewNotices con sqlTime: siempre en ese formato.
			if d.NoticedAt, err = time.Parse(time.DateTime, noticed); err != nil {
				return d, err
			}
		}
		d.DueAt, err = time.Parse(time.DateTime, due)
		return d, err
	})
}

// ApplyReviewNotices guarda los avisos y anota cuándo se avisó de cada página, en una transacción.
func (r *Repo) ApplyReviewNotices(ctx context.Context, wsID string, pageIDs []string, ns []NewNotification, at time.Time) error {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertNotifications(ctx, tx, wsID, ns); err != nil {
		return err
	}
	for _, id := range pageIDs {
		if _, err := tx.ExecContext(ctx, `UPDATE pages SET review_notified_at = ? WHERE id = ?`, sqlTime(at), id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ReviewProblem es una página del área con la vigencia desatendida (§13.7). Owner es "" si el
// dueño está bien, "none" si no tiene (o ya no puede editarla en el área) e "inactive" si está
// desactivado.
type ReviewProblem struct {
	Slug, Owner string
	Overdue     bool
}

func (r *Repo) ReviewProblems(ctx context.Context, s Scope, now time.Time) ([]ReviewProblem, error) {
	rows, err := r.db.Read.QueryContext(ctx, `SELECT p.slug,
		CASE WHEN m.user_id IS NULL OR `+rankSQL+` < MAX(2, p.min_rank) THEN 'none'
		     WHEN u.is_active = 0 THEN 'inactive' ELSE '' END AS owner,
		COALESCE(`+reviewDueSQL+` <= ?, 0) AS overdue
		FROM pages p
		LEFT JOIN users u ON u.id = p.owner_id
		LEFT JOIN workspace_members m ON m.workspace_id = p.workspace_id AND m.user_id = p.owner_id
		WHERE p.workspace_id = ? AND p.deleted_at IS NULL AND p.min_rank <= ? AND (owner != '' OR overdue)
		ORDER BY p.slug`, sqlTime(now), s.WsID, s.MaxRank)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (ReviewProblem, error) {
		var p ReviewProblem
		return p, row.Scan(&p.Slug, &p.Owner, &p.Overdue)
	})
}
