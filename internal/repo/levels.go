package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ErrBelowParent: el nivel pedido es menor que el de una página padre (§3.2).
var ErrBelowParent = errors.New("below parent")

// CanRead es la regla única de SPEC §3.2: el rol alcanza si su rango llega al nivel de la
// página. Las consultas la aplican como "min_rank <= MaxRank", con MaxRank = RoleRank(rol), y
// el panel "¿quién puede ver esto?" usa esta misma función, para no decir algo distinto (§3.4).
func CanRead(role string, minRank int) bool { return RoleRank(role) >= minRank }

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ancestors devuelve las rutas de los padres: a/b/c → a, a/b.
func ancestors(slug string) []any {
	parts := strings.Split(slug, "/")
	out := make([]any, 0, len(parts)-1)
	for i := 1; i < len(parts); i++ {
		out = append(out, strings.Join(parts[:i], "/"))
	}
	return out
}

// maxAncestorRank es el nivel más alto entre las páginas padre de slug, de cualquier nivel:
// la herencia es una regla del sistema (§3.2). 0 si no tiene padres. Cuenta también los padres
// que están en la papelera: así una hija creada mientras tanto nace con su nivel, y al
// restaurarlo ninguna queda por debajo de su padre.
func maxAncestorRank(ctx context.Context, q querier, s Scope, slug string) (int, error) {
	anc := ancestors(slug)
	if len(anc) == 0 {
		return 0, nil
	}
	var rank int
	err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(min_rank), 0) FROM pages WHERE workspace_id = ? AND slug IN (?`+
		strings.Repeat(",?", len(anc)-1)+`)`, append([]any{s.WsID}, anc...)...).Scan(&rank)
	return rank, err
}

// SetLevel cambia el nivel de la página y sube a ese nivel a las subpáginas que quedarían por
// debajo, cada cambio con su revisión: una hija nunca tiene menos nivel que su padre (§3.2).
// Las subpáginas que quien cambia no ve ya están sobre su rango y no se tocan. Devuelve el id
// de la página y su nivel anterior.
func (r *Repo) SetLevel(ctx context.Context, s Scope, actorID, slug string, level int) (string, int, error) {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback()
	anc, err := maxAncestorRank(ctx, tx, s, slug)
	if err != nil {
		return "", 0, err
	}
	if level < anc {
		return "", 0, ErrBelowParent
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT id, slug, title, content_markdown, lang, min_rank, current_revision FROM pages
		 WHERE workspace_id = ? AND deleted_at IS NULL AND min_rank <= ? AND (slug = ? OR (slug LIKE ? AND min_rank < ?))`,
		s.WsID, s.MaxRank, slug, slug+"/%", level)
	if err != nil {
		return "", 0, err
	}
	pages, err := collect(rows, scanMoved)
	if err != nil {
		return "", 0, err
	}
	var id string
	var from int
	for _, p := range pages {
		if p.slug == slug {
			id, from = p.id, p.rank
		}
	}
	if id == "" {
		return "", 0, ErrNotFound
	}
	for _, p := range pages {
		if p.rank == level {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE pages SET min_rank = ?, current_revision = current_revision + 1, updated_by = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
			level, actorID, p.id); err != nil {
			return "", 0, err
		}
		if err := insertRevision(ctx, tx, s, p.id, p.revision+1, PageWrite{
			Title: p.title, Markdown: p.markdown, Kind: "level_changed", Summary: levelName(p.rank) + " → " + levelName(level), AuthorID: actorID,
		}); err != nil {
			return "", 0, err
		}
	}
	return id, from, tx.Commit()
}

func levelName(rank int) string { return fmt.Sprintf("N%d", rank) }

func scanMoved(row scanner) (movedPage, error) {
	var p movedPage
	return p, row.Scan(&p.id, &p.slug, &p.title, &p.markdown, &p.lang, &p.rank, &p.revision)
}
