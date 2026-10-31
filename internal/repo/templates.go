package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/oklog/ulid/v2"
)

// Plantillas (SPEC §4.9). Las de un área filtran por área y por nivel, como las páginas: quien no
// alcanza su nivel no la ve ni la usa (§3.2). Las globales del superadmin tienen workspace_id
// NULL y se piden con un Scope sin WsID (GlobalScope), que solo arma el panel del superadmin.

type Template struct {
	ID          string
	Name        string
	Description string
	Title       string
	Markdown    string
	MinRank     int
	Tags        []string
	ReviewDays  int // intervalo de revisión de la página que nace de ella (§4.8); 0 = el del área
	UpdatedAt   time.Time
}

// GlobalScope es el de las plantillas globales: ningún área, todos los niveles.
var GlobalScope = Scope{MaxRank: 3}

// templateArea es el área del Scope para `workspace_id IS ?`: NULL para las globales.
func templateArea(s Scope) any {
	if s.WsID == "" {
		return nil
	}
	return s.WsID
}

const templateColumns = `id, name, description, title, content_markdown, min_rank, tags_json, COALESCE(review_interval_days, 0), updated_at`

func scanTemplate(row scanner) (Template, error) {
	var t Template
	var tags string
	if err := row.Scan(&t.ID, &t.Name, &t.Description, &t.Title, &t.Markdown, &t.MinRank, &tags, &t.ReviewDays, &t.UpdatedAt); err != nil {
		return Template{}, err
	}
	return t, json.Unmarshal([]byte(tags), &t.Tags)
}

// Templates lista las plantillas que quien pregunta ve, por nombre.
func (r *Repo) Templates(ctx context.Context, s Scope) ([]Template, error) {
	rows, err := r.db.Read.QueryContext(ctx, `SELECT `+templateColumns+` FROM page_templates
		WHERE workspace_id IS ? AND min_rank <= ? ORDER BY name COLLATE NOCASE, id`, templateArea(s), s.MaxRank)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanTemplate)
}

func (r *Repo) TemplateByID(ctx context.Context, s Scope, id string) (Template, error) {
	t, err := scanTemplate(r.db.Read.QueryRowContext(ctx, `SELECT `+templateColumns+` FROM page_templates
		WHERE id = ? AND workspace_id IS ? AND min_rank <= ?`, id, templateArea(s), s.MaxRank))
	if errors.Is(err, sql.ErrNoRows) {
		return Template{}, ErrNotFound
	}
	return t, err
}

// SaveTemplate crea la plantilla si no tiene ID y si no la reemplaza. Una que quien guarda no ve
// no existe: ErrNotFound, igual que una de otra área.
func (r *Repo) SaveTemplate(ctx context.Context, s Scope, actorID string, t Template) (string, error) {
	tags, err := json.Marshal(t.Tags)
	if err != nil {
		return "", err
	}
	if t.ID == "" {
		id := ulid.Make().String()
		_, err := r.db.Write.ExecContext(ctx, `INSERT INTO page_templates
			(id, workspace_id, name, description, title, content_markdown, min_rank, tags_json, review_interval_days, created_by)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, 0), ?)`,
			id, templateArea(s), t.Name, t.Description, t.Title, t.Markdown, t.MinRank, string(tags), t.ReviewDays, actorID)
		return id, err
	}
	return t.ID, affected(r.db.Write.ExecContext(ctx, `UPDATE page_templates
		SET name = ?, description = ?, title = ?, content_markdown = ?, min_rank = ?, tags_json = ?, review_interval_days = NULLIF(?, 0),
		updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND workspace_id IS ? AND min_rank <= ?`,
		t.Name, t.Description, t.Title, t.Markdown, t.MinRank, string(tags), t.ReviewDays, t.ID, templateArea(s), s.MaxRank))
}

func (r *Repo) DeleteTemplate(ctx context.Context, s Scope, id string) error {
	return affected(r.db.Write.ExecContext(ctx,
		`DELETE FROM page_templates WHERE id = ? AND workspace_id IS ? AND min_rank <= ?`, id, templateArea(s), s.MaxRank))
}

// copyGlobalTemplates copia las plantillas globales a un área recién creada (§4.9), en la misma
// transacción que la crea: desde ahí son del área y cambiar la global no las toca.
func copyGlobalTemplates(ctx context.Context, tx *sql.Tx, wsID string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM page_templates WHERE workspace_id IS NULL`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `INSERT INTO page_templates
			(id, workspace_id, name, description, title, content_markdown, min_rank, tags_json, review_interval_days, created_by)
			SELECT ?, ?, name, description, title, content_markdown, min_rank, tags_json, review_interval_days, created_by FROM page_templates WHERE id = ?`,
			ulid.Make().String(), wsID, id); err != nil {
			return err
		}
	}
	return nil
}
