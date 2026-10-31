package repo

import (
	"context"
	"time"
)

// El inicio personal (§15, B5) junta lo de todas las áreas de una persona. No lleva Scope porque
// cruza áreas: cada consulta filtra por su membresía y su rol en cada una, igual que la
// frontera de área (§3.3), y deja fuera los espacios personales.

// WatchedPage es una página que la persona sigue, con su último cambio.
type WatchedPage struct {
	AreaSlug, AreaName, Slug, Title, UpdatedBy string
	UpdatedAt                                  time.Time
	Subtree                                    bool
}

// WatchedPages son las páginas que userID sigue en sus áreas y todavía puede leer, la última
// cambiada primero. Seguir una página y después perder el nivel no deja ver ni su título (§17).
func (r *Repo) WatchedPages(ctx context.Context, userID string, limit int) ([]WatchedPage, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT w.slug, w.name, p.slug, p.title, u.full_name, p.updated_at, pw.include_children
		 FROM page_watchers pw
		 JOIN pages p ON p.id = pw.page_id
		 JOIN workspaces w ON w.id = p.workspace_id
		 JOIN workspace_members m ON m.workspace_id = p.workspace_id AND m.user_id = pw.user_id
		 JOIN users u ON u.id = p.updated_by
		 WHERE pw.user_id = ? AND w.kind = 'area' AND p.deleted_at IS NULL AND `+rankSQL+` >= p.min_rank
		 ORDER BY p.updated_at DESC, p.id LIMIT ?`, userID, max(1, limit))
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (WatchedPage, error) {
		var p WatchedPage
		err := row.Scan(&p.AreaSlug, &p.AreaName, &p.Slug, &p.Title, &p.UpdatedBy, &p.UpdatedAt, &p.Subtree)
		return p, err
	})
}

// OwnReview es una página vencida de la persona, en cualquiera de sus áreas (§4.8).
type OwnReview struct {
	ReviewItem
	AreaSlug, AreaName string
}

// OwnReviews son las páginas vencidas de las que userID es dueño y que todavía puede leer, las
// más atrasadas primero.
func (r *Repo) OwnReviews(ctx context.Context, userID string, now time.Time) ([]OwnReview, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT w.slug, w.name, p.slug, p.title, p.min_rank, p.verified_at, `+reviewDueSQL+` AS due
		 FROM pages p
		 JOIN workspaces w ON w.id = p.workspace_id
		 JOIN workspace_members m ON m.workspace_id = p.workspace_id AND m.user_id = p.owner_id
		 WHERE p.owner_id = ? AND w.kind = 'area' AND p.deleted_at IS NULL AND `+rankSQL+` >= p.min_rank AND `+reviewDueSQL+` <= ?
		 ORDER BY due, w.slug, p.slug LIMIT 500`, userID, sqlTime(now))
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (OwnReview, error) {
		var it OwnReview
		var due string
		if err := row.Scan(&it.AreaSlug, &it.AreaName, &it.Slug, &it.Title, &it.MinRank, &it.VerifiedAt, &due); err != nil {
			return it, err
		}
		it.DueAt, err = time.Parse(time.DateTime, due)
		return it, err
	})
}
