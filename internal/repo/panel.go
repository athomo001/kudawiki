package repo

import (
	"context"
	"time"
)

// Lo que muestran las tablas de Personas y Áreas en la administración (SPEC §15, diseño
// "Kudawiki — Administración"). Son del panel del superadmin, que ve todas las áreas: no pasan
// por Scope porque no hay un área que contener. Los espacios personales quedan afuera (§7).

// UserArea es un área de una persona con su rol, para la columna Áreas de Personas.
type UserArea struct {
	UserID string
	Name   string
	Role   string
}

func (r *Repo) UserAreas(ctx context.Context) ([]UserArea, error) {
	rows, err := r.db.Read.QueryContext(ctx, `SELECT m.user_id, w.name, m.role FROM workspace_members m
		JOIN workspaces w ON w.id = m.workspace_id WHERE w.kind = 'area' ORDER BY w.name`)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(s scanner) (UserArea, error) {
		var a UserArea
		return a, s.Scan(&a.UserID, &a.Name, &a.Role)
	})
}

// PanelArea son los números de un área en la tabla de Áreas. Las páginas de la papelera no
// cuentan, y Admins son solo los activos: quien está en baja ya no administra.
type PanelArea struct {
	Members  int
	Pages    int
	Admins   []string
	LastEdit time.Time
}

func (r *Repo) PanelAreas(ctx context.Context) (map[string]*PanelArea, error) {
	out := map[string]*PanelArea{}
	get := func(id string) *PanelArea {
		if out[id] == nil {
			out[id] = &PanelArea{}
		}
		return out[id]
	}
	type row struct {
		id string
		n  int
	}
	scanCount := func(s scanner) (row, error) {
		var x row
		return x, s.Scan(&x.id, &x.n)
	}
	rows, err := r.db.Read.QueryContext(ctx, `SELECT workspace_id, count(*) FROM workspace_members GROUP BY workspace_id`)
	if err != nil {
		return nil, err
	}
	members, err := collect(rows, scanCount)
	if err != nil {
		return nil, err
	}
	for _, m := range members {
		get(m.id).Members = m.n
	}
	if rows, err = r.db.Read.QueryContext(ctx, `SELECT workspace_id, count(*) FROM pages WHERE deleted_at IS NULL GROUP BY workspace_id`); err != nil {
		return nil, err
	}
	pages, err := collect(rows, scanCount)
	if err != nil {
		return nil, err
	}
	for _, p := range pages {
		get(p.id).Pages = p.n
	}
	type admin struct{ id, name string }
	if rows, err = r.db.Read.QueryContext(ctx, `SELECT m.workspace_id, u.full_name FROM workspace_members m
		JOIN users u ON u.id = m.user_id WHERE m.role = 'admin' AND u.is_active ORDER BY u.full_name`); err != nil {
		return nil, err
	}
	admins, err := collect(rows, func(s scanner) (admin, error) {
		var a admin
		return a, s.Scan(&a.id, &a.name)
	})
	if err != nil {
		return nil, err
	}
	for _, a := range admins {
		st := get(a.id)
		st.Admins = append(st.Admins, a.name)
	}
	// La fila de la página más reciente, no MAX(updated_at): así la columna conserva su tipo y
	// llega como fecha.
	type edit struct {
		id string
		at time.Time
	}
	if rows, err = r.db.Read.QueryContext(ctx, `SELECT w.id, p.updated_at FROM workspaces w
		JOIN pages p ON p.id = (SELECT id FROM pages WHERE workspace_id = w.id AND deleted_at IS NULL ORDER BY updated_at DESC LIMIT 1)`); err != nil {
		return nil, err
	}
	edits, err := collect(rows, func(s scanner) (edit, error) {
		var e edit
		return e, s.Scan(&e.id, &e.at)
	})
	if err != nil {
		return nil, err
	}
	for _, e := range edits {
		get(e.id).LastEdit = e.at
	}
	return out, nil
}
