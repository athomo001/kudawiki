package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Baja de usuarios (SPEC §8.4). Son operaciones del panel del superadmin y del job diario sobre
// una persona en todas sus áreas: no llevan Scope, como el resto de lo de usuarios. Las que
// tocan un área sola (quitar a alguien de un área) sí lo llevan.

// AreaAdmin es un admin activo de un área, candidato a recibir lo de quien se va.
type AreaAdmin struct{ ID, FullName string }

// OffboardingArea es lo que la persona tiene en un área y a quién se le puede pasar. Admins va
// del más antiguo en el área al más nuevo, sin la persona: el primero es el receptor por defecto.
type OffboardingArea struct {
	WsID, Slug, Name string
	Role             string // "" si ya no es miembro pero algo suyo quedó ahí
	Pages            int    // de las que es dueña, también las de la papelera
	Quizzes          int
	ReadRequests     int
	Admins           []AreaAdmin
}

// OffboardingPlan es el resumen que ve el superadmin antes de la baja.
type OffboardingPlan struct {
	Areas         []OffboardingArea
	PersonalPages int
	PersonalMedia int
	Drafts        int // autoguardados en las áreas; los del espacio personal se van con él
	Proposals     int // devueltas o retiradas: contenido que nunca se publicó
}

// Offboarding es una baja en curso.
type Offboarding struct {
	UserID    string
	Until     time.Time         // fin del período de gracia
	Receivers map[string]string // workspace_id → quien recibe
	By        string            // el superadmin que la inició
}

func (r *Repo) OffboardingPlan(ctx context.Context, userID string) (OffboardingPlan, error) {
	var p OffboardingPlan
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT w.id, w.slug, w.name, COALESCE(m.role, ''),
		   (SELECT COUNT(*) FROM pages WHERE workspace_id = w.id AND owner_id = ?1),
		   (SELECT COUNT(*) FROM quizzes WHERE workspace_id = w.id AND created_by = ?1),
		   (SELECT COUNT(*) FROM read_requests WHERE workspace_id = w.id AND created_by = ?1)
		 FROM workspaces w LEFT JOIN workspace_members m ON m.workspace_id = w.id AND m.user_id = ?1
		 WHERE w.kind = 'area' AND (m.user_id IS NOT NULL
		   OR EXISTS (SELECT 1 FROM pages WHERE workspace_id = w.id AND owner_id = ?1)
		   OR EXISTS (SELECT 1 FROM quizzes WHERE workspace_id = w.id AND created_by = ?1)
		   OR EXISTS (SELECT 1 FROM read_requests WHERE workspace_id = w.id AND created_by = ?1))
		 ORDER BY w.name`, userID)
	if err != nil {
		return p, err
	}
	p.Areas, err = collect(rows, func(row scanner) (OffboardingArea, error) {
		var a OffboardingArea
		return a, row.Scan(&a.WsID, &a.Slug, &a.Name, &a.Role, &a.Pages, &a.Quizzes, &a.ReadRequests)
	})
	if err != nil {
		return p, err
	}
	for i := range p.Areas {
		if p.Areas[i].Admins, err = r.areaAdmins(ctx, p.Areas[i].WsID, userID); err != nil {
			return p, err
		}
	}
	err = r.db.Read.QueryRowContext(ctx,
		`SELECT (SELECT COUNT(*) FROM pages p JOIN workspaces w ON w.id = p.workspace_id WHERE w.kind = 'personal' AND w.owner_user_id = ?1),
		   (SELECT COUNT(*) FROM media_assets a JOIN workspaces w ON w.id = a.workspace_id WHERE w.kind = 'personal' AND w.owner_user_id = ?1),
		   (SELECT COUNT(*) FROM page_drafts d JOIN workspaces w ON w.id = d.workspace_id WHERE w.kind = 'area' AND d.user_id = ?1),
		   (SELECT COUNT(*) FROM proposals WHERE author_id = ?1 AND status IN ('rejected', 'withdrawn'))`, userID).
		Scan(&p.PersonalPages, &p.PersonalMedia, &p.Drafts, &p.Proposals)
	return p, err
}

// areaAdmins son los admins activos del área menos except, del más antiguo al más nuevo.
func (r *Repo) areaAdmins(ctx context.Context, wsID, except string) ([]AreaAdmin, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT u.id, u.full_name FROM workspace_members m JOIN users u ON u.id = m.user_id
		 WHERE m.workspace_id = ? AND m.role = 'admin' AND u.is_active = 1 AND u.id != ?
		 ORDER BY m.created_at, u.id`, wsID, except)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (AreaAdmin, error) {
		var a AreaAdmin
		return a, row.Scan(&a.ID, &a.FullName)
	})
}

// OldestAdmin es el admin activo más antiguo del área sin contar a except; "" si no queda ninguno.
func (r *Repo) OldestAdmin(ctx context.Context, s Scope, except string) (string, error) {
	admins, err := r.areaAdmins(ctx, s.WsID, except)
	if err != nil || len(admins) == 0 {
		return "", err
	}
	return admins[0].ID, nil
}

// StartOffboarding desactiva la cuenta ya, sin esperar la gracia: cierra sus sesiones y anula
// los enlaces de invitación o de contraseña que tuviera. Devuelve cuántas sesiones cerró.
func (r *Repo) StartOffboarding(ctx context.Context, o Offboarding) (int64, error) {
	receivers, err := json.Marshal(o.Receivers)
	if err != nil {
		return 0, err
	}
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`UPDATE users SET is_active = 0, offboarding_at = ?, offboarding_receiver_json = ?, offboarding_by = NULLIF(?, ''), updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND offboarded_at IS NULL`, sqlTime(o.Until), string(receivers), o.By, o.UserID)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM auth_tokens WHERE user_id = ?`, o.UserID); err != nil {
		return 0, err
	}
	res, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, o.UserID)
	if err != nil {
		return 0, err
	}
	closed, _ := res.RowsAffected()
	return closed, tx.Commit()
}

// OffboardingOf es la baja en curso de la persona; ErrNotFound si no tiene una.
func (r *Repo) OffboardingOf(ctx context.Context, userID string) (Offboarding, error) {
	o := Offboarding{UserID: userID}
	var receivers string
	var by sql.NullString
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT offboarding_at, offboarding_receiver_json, offboarding_by FROM users
		 WHERE id = ? AND offboarding_at IS NOT NULL AND offboarded_at IS NULL`, userID).Scan(&o.Until, &receivers, &by)
	if errors.Is(err, sql.ErrNoRows) {
		return o, ErrNotFound
	}
	if err != nil {
		return o, err
	}
	o.By = by.String
	return o, json.Unmarshal([]byte(receivers), &o.Receivers)
}

// DueOffboardings son las personas cuya gracia venció antes de now.
func (r *Repo) DueOffboardings(ctx context.Context, now time.Time) ([]string, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT id FROM users WHERE offboarding_at <= ? AND offboarded_at IS NULL ORDER BY offboarding_at`, sqlTime(now))
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (string, error) {
		var id string
		return id, row.Scan(&id)
	})
}

// Reactivate vuelve a activar la cuenta. Antes de completar la baja la anula y no se pierde
// nada; después, la persona entra sin áreas ni espacio personal, que ya se transfirieron o
// borraron. Devuelve ErrConflict si la cuenta ya estaba activa.
func (r *Repo) Reactivate(ctx context.Context, userID string) error {
	res, err := r.db.Write.ExecContext(ctx,
		`UPDATE users SET is_active = 1, offboarding_at = NULL, offboarding_receiver_json = NULL, offboarding_by = NULL,
		   offboarded_at = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND is_active = 0`, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := r.UserByID(ctx, userID); err != nil {
			return err
		}
		return ErrConflict
	}
	return nil
}

// SetGlobalAdmin da o quita el rol de superadmin (B15).
func (r *Repo) SetGlobalAdmin(ctx context.Context, userID string, on bool) error {
	return r.updateUser(ctx, `UPDATE users SET is_global_admin = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, on, userID)
}

// Transfer es lo que pasó de una persona a otra en un área (§8.4).
type Transfer struct {
	WsID, To     string
	PageIDs      []string
	Quizzes      int
	ReadRequests int
}

func (t Transfer) Empty() bool { return t.Count() == 0 }

func (t Transfer) Count() int { return len(t.PageIDs) + t.Quizzes + t.ReadRequests }

// Notice es el aviso a quien recibe (§8.4): de quién y cuánto. El payload tiene la forma de los
// de notify ({"actor": …}); el detalle está en la auditoría del área.
func (t Transfer) Notice(fromName string) NewNotification {
	p, _ := json.Marshal(map[string]string{"actor": fromName})
	return NewNotification{UserID: t.To, Kind: "ownership_received", Category: "review", Payload: string(p), Count: t.Count()}
}

// transfer pasa a to las páginas de las que from es dueño, sus cuestionarios y sus solicitudes
// de lectura. Las revisiones conservan al autor: la historia no se reescribe.
func transfer(ctx context.Context, tx *sql.Tx, wsID, from, to string) (Transfer, error) {
	t := Transfer{WsID: wsID, To: to}
	rows, err := tx.QueryContext(ctx,
		`UPDATE pages SET owner_id = ? WHERE workspace_id = ? AND owner_id = ? RETURNING id`, to, wsID, from)
	if err != nil {
		return t, err
	}
	if t.PageIDs, err = collect(rows, func(row scanner) (string, error) {
		var id string
		return id, row.Scan(&id)
	}); err != nil {
		return t, err
	}
	for _, q := range []struct {
		sql string
		n   *int
	}{
		{`UPDATE quizzes SET created_by = ? WHERE workspace_id = ? AND created_by = ?`, &t.Quizzes},
		{`UPDATE read_requests SET created_by = ? WHERE workspace_id = ? AND created_by = ?`, &t.ReadRequests},
	} {
		res, err := tx.ExecContext(ctx, q.sql, to, wsID, from)
		if err != nil {
			return t, err
		}
		n, _ := res.RowsAffected()
		*q.n = int(n)
	}
	return t, nil
}

// RemoveMember saca a la persona del área y le pasa a receiver lo suyo de esa área (§8.4: quitarlo
// de un área transfiere solo lo de esa área). Sin receiver no transfiere nada.
func (r *Repo) RemoveMember(ctx context.Context, s Scope, userID, receiver string) (Transfer, error) {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return Transfer{}, err
	}
	defer tx.Rollback()
	t := Transfer{WsID: s.WsID}
	if receiver != "" {
		if t, err = transfer(ctx, tx, s.WsID, userID, receiver); err != nil {
			return t, err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_members WHERE workspace_id = ? AND user_id = ?`, s.WsID, userID); err != nil {
		return t, err
	}
	return t, tx.Commit()
}

// OffboardingResult es lo que hizo la baja, para la auditoría y el aviso a cada receptor.
type OffboardingResult struct {
	Transfers     []Transfer
	PersonalWsID  string // "" si nunca entró a su espacio personal
	PersonalPages int
	PersonalMedia []MediaFile // los archivos se borran del disco después de confirmar
	Drafts        int64
	Proposals     int64
}

// CompleteOffboarding hace la baja de una vez: transfiere lo de cada área a su receptor, borra lo
// no publicado (espacio personal, autoguardados, propuestas devueltas o retiradas) y lo que era
// solo de la persona (seguimientos, avisos, passkeys, sesiones), la saca de todas las áreas y le
// quita el superadmin. Todo o nada: si algo falla, la baja sigue pendiente y el job la retoma.
func (r *Repo) CompleteOffboarding(ctx context.Context, userID string, receivers map[string]string, at time.Time) (OffboardingResult, error) {
	var out OffboardingResult
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	for wsID, to := range receivers {
		t, err := transfer(ctx, tx, wsID, userID, to)
		if err != nil {
			return out, err
		}
		if !t.Empty() {
			out.Transfers = append(out.Transfers, t)
		}
	}
	err = tx.QueryRowContext(ctx, `SELECT id FROM workspaces WHERE kind = 'personal' AND owner_user_id = ?`, userID).Scan(&out.PersonalWsID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if out.PersonalWsID != "" {
		if out.PersonalMedia, err = personalPurge(ctx, tx, out.PersonalWsID, &out.PersonalPages); err != nil {
			return out, err
		}
	}
	for _, q := range []struct {
		sql string
		n   *int64
	}{
		{`DELETE FROM page_drafts WHERE user_id = ?`, &out.Drafts},
		{`DELETE FROM proposals WHERE author_id = ? AND status IN ('rejected', 'withdrawn')`, &out.Proposals},
		{`DELETE FROM page_watchers WHERE user_id = ?`, nil},
		{`DELETE FROM notifications WHERE user_id = ?`, nil},
		{`DELETE FROM passkeys WHERE user_id = ?`, nil},
		{`DELETE FROM sessions WHERE user_id = ?`, nil},
		{`DELETE FROM auth_tokens WHERE user_id = ?`, nil},
		{`DELETE FROM workspace_members WHERE user_id = ?`, nil},
	} {
		res, err := tx.ExecContext(ctx, q.sql, userID)
		if err != nil {
			return out, err
		}
		if q.n != nil {
			*q.n, _ = res.RowsAffected()
		}
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE users SET is_active = 0, is_global_admin = 0, offboarded_at = ?, offboarding_at = NULL,
		   offboarding_receiver_json = NULL, offboarding_by = NULL, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND offboarded_at IS NULL`, sqlTime(at), userID)
	if err != nil {
		return out, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return out, ErrNotFound
	}
	return out, tx.Commit()
}

// personalPurge borra el espacio personal entero: sus filas de búsqueda (que no tienen clave
// foránea) y después el workspace, que se lleva por cascada páginas, revisiones y media.
// Devuelve los archivos de media para borrarlos del disco.
func personalPurge(ctx context.Context, tx *sql.Tx, wsID string, pages *int) ([]MediaFile, error) {
	rows, err := tx.QueryContext(ctx, `SELECT workspace_id, id FROM media_assets WHERE workspace_id = ?`, wsID)
	if err != nil {
		return nil, err
	}
	files, err := collect(rows, func(row scanner) (MediaFile, error) {
		var f MediaFile
		return f, row.Scan(&f.WsID, &f.ID)
	})
	if err != nil {
		return nil, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pages WHERE workspace_id = ?`, wsID).Scan(pages); err != nil {
		return nil, err
	}
	for _, q := range []string{
		`DELETE FROM pages_fts WHERE rowid IN (SELECT search_rowid FROM pages WHERE workspace_id = ?)`,
		`DELETE FROM pages_title_trgm WHERE rowid IN (SELECT search_rowid FROM pages WHERE workspace_id = ?)`,
		`DELETE FROM workspaces WHERE id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, wsID); err != nil {
			return nil, err
		}
	}
	return files, nil
}

// AddNotifications deja avisos sueltos de un área, fuera de un reparto del worker.
func (r *Repo) AddNotifications(ctx context.Context, wsID string, ns []NewNotification) error {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertNotifications(ctx, tx, wsID, ns); err != nil {
		return err
	}
	return tx.Commit()
}

// DuePseudonyms son quienes completaron la baja antes de cutoff y todavía tienen eventos de
// auditoría con su copia sin seudonimizar (§13.10, B14).
func (r *Repo) DuePseudonyms(ctx context.Context, cutoff time.Time) ([]string, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT u.id FROM users u WHERE u.offboarded_at <= ?
		   AND EXISTS (SELECT 1 FROM audit_log a WHERE a.actor_id = u.id AND json_extract(a.actor_snapshot_json, '$.pseudonym') IS NULL)`,
		sqlTime(cutoff))
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (string, error) {
		var id string
		return id, row.Scan(&id)
	})
}

// Pseudonymize reemplaza la copia del actor (email, nombre, IP) de sus eventos por el seudónimo.
// La copia está fuera del hash: la cadena no se rompe (§11.1, §13.10).
func (r *Repo) Pseudonymize(ctx context.Context, userID, pseudonym string) (int64, error) {
	snapshot, _ := json.Marshal(map[string]string{"pseudonym": pseudonym})
	res, err := r.db.Write.ExecContext(ctx,
		`UPDATE audit_log SET actor_snapshot_json = ? WHERE actor_id = ? AND json_extract(actor_snapshot_json, '$.pseudonym') IS NULL`,
		string(snapshot), userID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
