package repo

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// AuditRow es una fila de audit_log tal como se guarda (SPEC §11). Los campos vacíos van como NULL.
type AuditRow struct {
	ID                             int64
	At                             string
	ActorID, WsID                  string
	Action, TargetType, TargetID   string
	Meta, Snapshot, PrevHash, Hash string
}

// AuditEntry es un evento para mostrar.
type AuditEntry struct {
	AuditRow
	WsSlug   string // solo en la vista del superadmin
	PageSlug string // solo en la vista del área, y solo si el evento es de una página que el Scope ve
}

// AuditFilter son los filtros de la pantalla de auditoría. Before pagina hacia atrás por id.
type AuditFilter struct {
	WsSlug, ActorEmail, Action string
	From, To                   string // AAAA-MM-DD
	Before                     int64
	Limit                      int
}

// AppendAudit agrega la fila al final de la cadena. seal calcula el hash a partir del de la
// última fila: leer la última y escribir la nueva van en la misma transacción, y hay un solo
// escritor (§13.4), así dos eventos nunca sellan la misma fila anterior.
func (r *Repo) AppendAudit(ctx context.Context, row AuditRow, seal func(prev string) string) error {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `SELECT hash FROM audit_log ORDER BY id DESC LIMIT 1`).Scan(&row.PrevHash)
	if errors.Is(err, sql.ErrNoRows) {
		row.PrevHash = strings.Repeat("0", 64)
	} else if err != nil {
		return err
	}
	row.Hash = seal(row.PrevHash)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO audit_log (at, actor_id, workspace_id, action, target_type, target_id, meta_json, actor_snapshot_json, prev_hash, hash)
		 VALUES (?, NULLIF(?, ''), NULLIF(?, ''), ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?)`,
		row.At, row.ActorID, row.WsID, row.Action, row.TargetType, row.TargetID, row.Meta, row.Snapshot, row.PrevHash, row.Hash); err != nil {
		return err
	}
	return tx.Commit()
}

const auditColumns = `a.id, a.at, COALESCE(a.actor_id, ''), COALESCE(a.workspace_id, ''), a.action,
	COALESCE(a.target_type, ''), COALESCE(a.target_id, ''), a.meta_json, a.actor_snapshot_json, a.prev_hash, a.hash`

func scanAuditRow(row scanner, extra ...any) (AuditRow, error) {
	var a AuditRow
	err := row.Scan(append([]any{&a.ID, &a.At, &a.ActorID, &a.WsID, &a.Action, &a.TargetType, &a.TargetID,
		&a.Meta, &a.Snapshot, &a.PrevHash, &a.Hash}, extra...)...)
	return a, err
}

// AuditChain recorre la cadena completa en orden, para `audit verify`. Es del sistema: no filtra.
func (r *Repo) AuditChain(ctx context.Context, fn func(AuditRow) error) error {
	rows, err := r.db.Read.QueryContext(ctx, `SELECT `+auditColumns+` FROM audit_log a ORDER BY a.id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		a, err := scanAuditRow(rows)
		if err != nil {
			return err
		}
		if err := fn(a); err != nil {
			return err
		}
	}
	return rows.Err()
}

// AuditEvents es la vista del superadmin: todas las áreas. Muestra ids, nunca rutas ni títulos:
// el superadmin no es miembro de las áreas (§3.1) y la auditoría no se los revela.
func (r *Repo) AuditEvents(ctx context.Context, f AuditFilter) ([]AuditEntry, error) {
	where, args := auditWhere(f)
	if f.WsSlug != "" {
		where += ` AND a.workspace_id = (SELECT id FROM workspaces WHERE slug = ?)`
		args = append(args, f.WsSlug)
	}
	return r.auditEntries(ctx, `SELECT `+auditColumns+`, COALESCE(w.slug, ''), '' FROM audit_log a
		LEFT JOIN workspaces w ON w.id = a.workspace_id WHERE `+where, f, args)
}

// AreaAudit es la vista del admin del área: solo eventos de su área, y la ruta actual de las
// páginas que su Scope ve (§11.2: el filtro va en repo, no en la interfaz). Los eventos de
// espacios personales y de sellados que §11.2 le oculta llegan con F5 y F7.
func (r *Repo) AreaAudit(ctx context.Context, s Scope, f AuditFilter) ([]AuditEntry, error) {
	where, args := auditWhere(f)
	where += ` AND a.workspace_id = ?`
	args = append([]any{s.MaxRank}, append(args, s.WsID)...)
	return r.auditEntries(ctx, `SELECT `+auditColumns+`, '', COALESCE(p.slug, '') FROM audit_log a
		LEFT JOIN pages p ON a.target_type = 'page' AND p.id = a.target_id AND p.workspace_id = a.workspace_id AND p.min_rank <= ?
		WHERE `+where, f, args)
}

func auditWhere(f AuditFilter) (string, []any) {
	where, args := `1 = 1`, []any{}
	if f.ActorEmail != "" {
		where += ` AND a.actor_id IN (SELECT id FROM users WHERE email = ?)`
		args = append(args, f.ActorEmail)
	}
	if f.Action != "" {
		where += ` AND a.action = ?`
		args = append(args, f.Action)
	}
	if f.From != "" {
		where += ` AND a.at >= ?`
		args = append(args, f.From)
	}
	if f.To != "" {
		where += ` AND a.at < date(?, '+1 day')`
		args = append(args, f.To)
	}
	if f.Before > 0 {
		where += ` AND a.id < ?`
		args = append(args, f.Before)
	}
	return where, args
}

func (r *Repo) auditEntries(ctx context.Context, query string, f AuditFilter, args []any) ([]AuditEntry, error) {
	rows, err := r.db.Read.QueryContext(ctx, query+` ORDER BY a.id DESC LIMIT ?`, append(args, f.Limit)...)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (AuditEntry, error) {
		var e AuditEntry
		var err error
		e.AuditRow, err = scanAuditRow(row, &e.WsSlug, &e.PageSlug)
		return e, err
	})
}

// PurgeAudit borra los eventos anteriores a cutoff (retención, §11.1). El trigger de la BD
// rechaza cualquier borrado de los últimos 180 días.
func (r *Repo) PurgeAudit(ctx context.Context, cutoff string) (int64, error) {
	res, err := r.db.Write.ExecContext(ctx, `DELETE FROM audit_log WHERE at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
