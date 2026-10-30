package repo

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/oklog/ulid/v2"
)

// Inicio del área (SPEC §4.17). Los anuncios son de todos los miembros: filtran por área, no
// por nivel. Las estadísticas y las visitas unen con pages y filtran por s.MaxRank, como el
// árbol: una página que quien mira no ve no suma en ningún número (§17).

type Announcement struct {
	ID        string
	Title     string
	Markdown  string
	AuthorID  string
	Author    string // nombre de quien lo escribió
	CreatedAt time.Time
}

func (r *Repo) CreateAnnouncement(ctx context.Context, s Scope, a Announcement) (string, error) {
	id := ulid.Make().String()
	_, err := r.db.Write.ExecContext(ctx,
		`INSERT INTO announcements (id, workspace_id, title, body_markdown, created_by) VALUES (?, ?, ?, ?, ?)`,
		id, s.WsID, a.Title, a.Markdown, a.AuthorID)
	return id, err
}

// Announcements devuelve los últimos limit anuncios del área, el más nuevo primero.
func (r *Repo) Announcements(ctx context.Context, s Scope, limit int) ([]Announcement, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT a.id, a.title, a.body_markdown, a.created_by, u.full_name, a.created_at
		 FROM announcements a JOIN users u ON u.id = a.created_by
		 WHERE a.workspace_id = ? ORDER BY a.created_at DESC, a.id DESC LIMIT ?`, s.WsID, limit)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (Announcement, error) {
		var a Announcement
		return a, row.Scan(&a.ID, &a.Title, &a.Markdown, &a.AuthorID, &a.Author, &a.CreatedAt)
	})
}

// AnnouncementByID es para decidir quién puede borrarlo; uno de otra área no existe.
func (r *Repo) AnnouncementByID(ctx context.Context, s Scope, id string) (Announcement, error) {
	var a Announcement
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT id, title, created_by, created_at FROM announcements WHERE id = ? AND workspace_id = ?`, id, s.WsID).
		Scan(&a.ID, &a.Title, &a.AuthorID, &a.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Announcement{}, ErrNotFound
	}
	return a, err
}

func (r *Repo) DeleteAnnouncement(ctx context.Context, s Scope, id string) error {
	res, err := r.db.Write.ExecContext(ctx, `DELETE FROM announcements WHERE id = ? AND workspace_id = ?`, id, s.WsID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RecordView suma una visita del día a una página que quien la abre puede ver: sin quién
// (§20.3). Una página oculta o borrada no suma y responde ErrNotFound.
func (r *Repo) RecordView(ctx context.Context, s Scope, slug string) error {
	res, err := r.db.Write.ExecContext(ctx,
		`INSERT INTO page_views (page_id, workspace_id, day)
		 SELECT id, workspace_id, date('now') FROM pages
		 WHERE workspace_id = ? AND slug = ? AND deleted_at IS NULL AND min_rank <= ?
		 ON CONFLICT (page_id, day) DO UPDATE SET views = views + 1`, s.WsID, slug, s.MaxRank)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

type RecentPage struct {
	PageRef
	UpdatedAt time.Time
}

type ViewedPage struct {
	PageRef
	Views int
}

// AreaStats es la actividad del área en los últimos 30 días, contada con el nivel de quien
// mira (§4.17). Las listas traen hasta limit páginas.
type AreaStats struct {
	Pages     int
	Changes   int // revisiones nuevas
	New       int // páginas creadas
	Recent    []RecentPage
	TopViewed []ViewedPage
}

const statsWindow = "-30 day"

func (r *Repo) AreaStats(ctx context.Context, s Scope, limit int) (AreaStats, error) {
	var st AreaStats
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(p.created_at >= datetime('now', ?2)), 0),
		        (SELECT COUNT(*) FROM page_revisions v JOIN pages q ON q.id = v.page_id
		         WHERE q.workspace_id = ?1 AND q.deleted_at IS NULL AND q.min_rank <= ?3 AND v.created_at >= datetime('now', ?2))
		 FROM pages p WHERE p.workspace_id = ?1 AND p.deleted_at IS NULL AND p.min_rank <= ?3`,
		s.WsID, statsWindow, s.MaxRank).Scan(&st.Pages, &st.New, &st.Changes)
	if err != nil {
		return st, err
	}
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT slug, title, min_rank, description, icon, updated_at FROM pages
		 WHERE workspace_id = ? AND deleted_at IS NULL AND min_rank <= ? ORDER BY updated_at DESC, slug LIMIT ?`,
		s.WsID, s.MaxRank, limit)
	if err != nil {
		return st, err
	}
	if st.Recent, err = collect(rows, func(row scanner) (RecentPage, error) {
		var p RecentPage
		return p, row.Scan(&p.Slug, &p.Title, &p.MinRank, &p.Description, &p.Icon, &p.UpdatedAt)
	}); err != nil {
		return st, err
	}
	rows, err = r.db.Read.QueryContext(ctx,
		`SELECT p.slug, p.title, p.min_rank, p.description, p.icon, SUM(v.views) AS total
		 FROM page_views v JOIN pages p ON p.id = v.page_id
		 WHERE v.workspace_id = ?1 AND p.workspace_id = ?1 AND p.deleted_at IS NULL AND p.min_rank <= ?2
		   AND v.day >= date('now', ?3)
		 GROUP BY p.id ORDER BY total DESC, p.slug LIMIT ?4`, s.WsID, s.MaxRank, statsWindow, limit)
	if err != nil {
		return st, err
	}
	st.TopViewed, err = collect(rows, func(row scanner) (ViewedPage, error) {
		var p ViewedPage
		return p, row.Scan(&p.Slug, &p.Title, &p.MinRank, &p.Description, &p.Icon, &p.Views)
	})
	return st, err
}

// PurgePageViews borra las visitas de antes de cutoff (§4.17: 90 días). Operación del sistema.
func (r *Repo) PurgePageViews(ctx context.Context, cutoff time.Time) error {
	_, err := r.db.Write.ExecContext(ctx, `DELETE FROM page_views WHERE day < ?`, cutoff.UTC().Format(time.DateOnly))
	return err
}
