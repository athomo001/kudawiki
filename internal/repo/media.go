package repo

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type Media struct {
	ID           string
	Filename     string
	MimeType     string
	Size         int64
	SHA256       string
	HasThumbnail bool
	UploadedBy   string
	CreatedAt    time.Time
}

func (r *Repo) CreateMedia(ctx context.Context, s Scope, m Media) error {
	_, err := r.db.Write.ExecContext(ctx,
		`INSERT INTO media_assets (id, workspace_id, original_filename, mime_type, file_size_bytes, sha256, has_thumbnail, uploaded_by)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, s.WsID, m.Filename, m.MimeType, m.Size, m.SHA256, m.HasThumbnail, m.UploadedBy)
	return err
}

// MediaByID aplica la herencia de nivel de SPEC §3.2: la media de una página se ve con el
// nivel de esa página; la que todavía no está en ninguna (recién pegada) solo la ven los editores.
// La de una página en la papelera, o la huérfana que ya pasó a ella, no la ve nadie (§4.5).
func (r *Repo) MediaByID(ctx context.Context, s Scope, id string) (Media, error) {
	var m Media
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT m.id, m.original_filename, m.mime_type, m.file_size_bytes, m.sha256, m.has_thumbnail, m.uploaded_by, m.created_at
		 FROM media_assets m LEFT JOIN pages p ON p.id = m.page_id
		 WHERE m.id = ? AND m.workspace_id = ? AND m.deleted_at IS NULL
		   AND ((m.page_id IS NULL AND ? >= 2) OR (p.deleted_at IS NULL AND p.min_rank <= ?))`,
		id, s.WsID, s.MaxRank, s.MaxRank).
		Scan(&m.ID, &m.Filename, &m.MimeType, &m.Size, &m.SHA256, &m.HasThumbnail, &m.UploadedBy, &m.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Media{}, ErrNotFound
	}
	return m, err
}

// attachMedia asocia a la página la media sin dueña que su contenido usa. La primera página
// que la usa se queda con ella y le da su nivel; nunca se mueve media de otra página ni de otra área.
// Si la huérfana ya estaba en la papelera, usarla la saca de ahí: sigue en disco hasta la purga.
func attachMedia(ctx context.Context, tx *sql.Tx, s Scope, pageID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	args := []any{pageID, s.WsID}
	for _, id := range ids {
		args = append(args, id)
	}
	_, err := tx.ExecContext(ctx,
		`UPDATE media_assets SET page_id = ?, deleted_at = NULL WHERE workspace_id = ? AND page_id IS NULL AND id IN (?`+
			strings.Repeat(",?", len(ids)-1)+`)`, args...)
	return err
}
