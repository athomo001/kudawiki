package repo

import (
	"context"
	"strings"
)

// ExportPage es una página como sale en la exportación de un área (§13.8): lo que va al front
// matter y el contenido. Nunca un sellado: viven en otra tabla y aquí no se leen.
type ExportPage struct {
	ID, Slug, Title, Description, Lang, Markdown string
	Level                                        int
	Owner                                        string // correo del dueño, si tiene
	Tags                                         []string
}

// ExportPages son las páginas vigentes del área hasta el rango de s, de padres a hijas.
func (r *Repo) ExportPages(ctx context.Context, s Scope) ([]ExportPage, error) {
	rows, err := r.db.Read.QueryContext(ctx, `SELECT p.id, p.slug, p.title, p.description, p.lang, p.content_markdown, p.min_rank,
		  COALESCE(o.email, ''), COALESCE((SELECT group_concat(t.tag, char(31)) FROM page_tags t WHERE t.page_id = p.id), '')
		FROM pages p LEFT JOIN users o ON o.id = p.owner_id
		WHERE p.workspace_id = ? AND p.deleted_at IS NULL AND p.min_rank <= ? ORDER BY p.slug`, s.WsID, s.MaxRank)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (ExportPage, error) {
		var p ExportPage
		var tags string
		err := row.Scan(&p.ID, &p.Slug, &p.Title, &p.Description, &p.Lang, &p.Markdown, &p.Level, &p.Owner, &tags)
		if tags != "" {
			p.Tags = strings.Split(tags, "\x1f")
		}
		return p, err
	})
}

// ExportMedia es la media de las páginas que entran en la exportación: con página vigente y de un
// nivel que alcanza el rango de s.
func (r *Repo) ExportMedia(ctx context.Context, s Scope) ([]Media, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT m.id, m.original_filename, m.mime_type, m.file_size_bytes, m.sha256, m.has_thumbnail, m.uploaded_by, m.created_at
		 FROM media_assets m JOIN pages p ON p.id = m.page_id
		 WHERE m.workspace_id = ? AND m.deleted_at IS NULL AND p.deleted_at IS NULL AND p.min_rank <= ? ORDER BY m.id`,
		s.WsID, s.MaxRank)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (Media, error) {
		var m Media
		return m, row.Scan(&m.ID, &m.Filename, &m.MimeType, &m.Size, &m.SHA256, &m.HasThumbnail, &m.UploadedBy, &m.CreatedAt)
	})
}
