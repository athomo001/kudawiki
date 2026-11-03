package repo

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"

	"github.com/oklog/ulid/v2"
)

// AccessReview es una confirmación de la revisión de accesos (§21.2.7).
type AccessReview struct {
	At           time.Time
	ReviewerName string
}

// LastAreaAccessReview es la última confirmación del área; ok es false si nunca se confirmó.
// Since es desde cuándo corre el plazo: esa confirmación o, si no hay, la creación del área.
func (r *Repo) LastAreaAccessReview(ctx context.Context, s Scope) (last AccessReview, ok bool, since time.Time, err error) {
	err = r.db.Read.QueryRowContext(ctx, `SELECT confirmed_at, reviewer_name FROM access_reviews
		WHERE workspace_id = ? ORDER BY confirmed_at DESC LIMIT 1`, s.WsID).Scan(&last.At, &last.ReviewerName)
	if err == nil {
		return last, true, last.At, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return last, false, since, err
	}
	err = r.db.Read.QueryRowContext(ctx, `SELECT created_at FROM workspaces WHERE id = ?`, s.WsID).Scan(&since)
	return last, false, since, err
}

// LastGlobalAccessReview es la última revisión del superadmin; sin ninguna, el plazo corre desde
// la primera cuenta de la instalación.
func (r *Repo) LastGlobalAccessReview(ctx context.Context) (last AccessReview, ok bool, since time.Time, err error) {
	err = r.db.Read.QueryRowContext(ctx, `SELECT confirmed_at, reviewer_name FROM access_reviews
		WHERE workspace_id IS NULL ORDER BY confirmed_at DESC LIMIT 1`).Scan(&last.At, &last.ReviewerName)
	if err == nil {
		return last, true, last.At, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return last, false, since, err
	}
	err = r.db.Read.QueryRowContext(ctx, `SELECT created_at FROM users ORDER BY created_at LIMIT 1`).Scan(&since)
	if errors.Is(err, sql.ErrNoRows) {
		return last, false, time.Time{}, nil
	}
	return last, false, since, err
}

// AddAccessReview guarda una confirmación. wsID "" es la revisión global.
func (r *Repo) AddAccessReview(ctx context.Context, wsID, reviewerID, reviewerName, snapshot string, at time.Time) error {
	_, err := r.db.Write.ExecContext(ctx, `INSERT INTO access_reviews (id, workspace_id, reviewer_id, reviewer_name, snapshot_json, confirmed_at)
		VALUES (?, NULLIF(?, ''), ?, ?, ?, ?)`, ulid.Make().String(), wsID, reviewerID, reviewerName, snapshot, at.UTC())
	return err
}

// AdminAccess es una persona con acceso de administración: superadmin, admin de áreas o ambos.
// Lo que revisa el superadmin (§21.2.7).
type AdminAccess struct {
	UserID, Email, FullName string
	Superadmin, Active      bool
	Areas                   []string // nombres de las áreas que administra
}

// AdminAccessList son los superadmins y los admins de área, también los desactivados que
// siguen con el rol: justamente lo que la revisión tiene que ver.
func (r *Repo) AdminAccessList(ctx context.Context) ([]AdminAccess, error) {
	rows, err := r.db.Read.QueryContext(ctx, `SELECT u.id, u.email, u.full_name, u.is_global_admin, u.is_active, COALESCE(w.name, '')
		FROM users u
		LEFT JOIN workspace_members m ON m.user_id = u.id AND m.role = 'admin'
		LEFT JOIN workspaces w ON w.id = m.workspace_id AND w.kind = 'area'
		WHERE u.is_global_admin = 1 OR w.id IS NOT NULL
		ORDER BY u.full_name, u.id, w.name`)
	if err != nil {
		return nil, err
	}
	type row struct {
		AdminAccess
		area string
	}
	all, err := collect(rows, func(s scanner) (row, error) {
		var x row
		return x, s.Scan(&x.UserID, &x.Email, &x.FullName, &x.Superadmin, &x.Active, &x.area)
	})
	if err != nil {
		return nil, err
	}
	var out []AdminAccess
	for _, x := range all {
		if n := len(out); n == 0 || out[n-1].UserID != x.UserID {
			out = append(out, x.AdminAccess)
		}
		if x.area != "" {
			last := &out[len(out)-1]
			last.Areas = append(last.Areas, x.area)
		}
	}
	return out, nil
}

// AreaReview es el estado de la revisión de un área, para la vista del superadmin.
type AreaReview struct {
	ID, Slug, Name string
	Last           *AccessReview
	Since          time.Time
}

// AreaAccessReviews es el estado de la revisión de cada área (sin los espacios personales: no
// tienen a quién revisar, §7; ni las desactivadas o por borrar, que nadie ve, §3.5; las
// archivadas sí: sus miembros siguen leyendo).
func (r *Repo) AreaAccessReviews(ctx context.Context) ([]AreaReview, error) {
	rows, err := r.db.Read.QueryContext(ctx, `SELECT w.id, w.slug, w.name, w.created_at FROM workspaces w WHERE w.kind = 'area'`+visibleArea+` ORDER BY w.name`)
	if err != nil {
		return nil, err
	}
	areas, err := collect(rows, func(s scanner) (AreaReview, error) {
		var a AreaReview
		return a, s.Scan(&a.ID, &a.Slug, &a.Name, &a.Since)
	})
	if err != nil {
		return nil, err
	}
	// La fila más reciente de cada área, no MAX(confirmed_at): así llega como fecha.
	if rows, err = r.db.Read.QueryContext(ctx, `SELECT a.workspace_id, a.confirmed_at, a.reviewer_name FROM access_reviews a
		WHERE a.id = (SELECT id FROM access_reviews WHERE workspace_id = a.workspace_id ORDER BY confirmed_at DESC LIMIT 1)`); err != nil {
		return nil, err
	}
	type last struct {
		ws string
		AccessReview
	}
	lasts, err := collect(rows, func(s scanner) (last, error) {
		var l last
		return l, s.Scan(&l.ws, &l.At, &l.ReviewerName)
	})
	if err != nil {
		return nil, err
	}
	for _, l := range lasts {
		if i := slices.IndexFunc(areas, func(a AreaReview) bool { return a.ID == l.ws }); i >= 0 {
			areas[i].Last, areas[i].Since = &l.AccessReview, l.At
		}
	}
	return areas, nil
}
