package repo

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// Notificaciones y seguir páginas (SPEC §5.3, §12.1). Hay dos tipos de consulta:
//   - las de una persona (su bandeja, sus preferencias, seguir una página) filtran por ella, y
//     seguir, además, por el Scope;
//   - las del worker son de la instalación, como la cola de correos, y no llevan Scope. Cada
//     consulta que elige destinatarios, o que lista o manda un aviso, compara en SQL el rol
//     actual de cada persona con el nivel actual de la página: es la verificación "en el
//     momento de repartir" de §12.1, y la que impide que un título se filtre a quien ya no
//     puede leerlo (§17).

// Eventos del outbox: los deja cada acción en su transacción.
const (
	EventPageSaved         = "page_saved"
	EventPageDeleted       = "page_deleted"
	EventProposalSubmitted = "proposal_submitted"
	EventProposalResolved  = "proposal_resolved"
)

// rankSQL es RoleRank en SQL, sobre la membresía m.
const rankSQL = `CASE m.role WHEN 'viewer' THEN 1 WHEN 'editor' THEN 2 WHEN 'admin' THEN 3 ELSE 0 END`

func emit(ctx context.Context, tx *sql.Tx, s Scope, kind, actorID, pageID, proposalID string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO notification_events (kind, workspace_id, actor_id, page_id, proposal_id)
		VALUES (?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''))`, kind, s.WsID, actorID, pageID, proposalID)
	return err
}

type NotificationEvent struct {
	ID                                      int64
	Kind, WsID, ActorID, PageID, ProposalID string
}

// NotificationEvents son los eventos por repartir, en orden.
func (r *Repo) NotificationEvents(ctx context.Context, limit int) ([]NotificationEvent, error) {
	rows, err := r.db.Read.QueryContext(ctx, `SELECT id, kind, workspace_id, COALESCE(actor_id, ''), COALESCE(page_id, ''), COALESCE(proposal_id, '')
		FROM notification_events ORDER BY id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (NotificationEvent, error) {
		var e NotificationEvent
		return e, row.Scan(&e.ID, &e.Kind, &e.WsID, &e.ActorID, &e.PageID, &e.ProposalID)
	})
}

// EventPage es la página de un evento, aunque ya esté en la papelera.
type EventPage struct {
	ID, Slug, Title string
	MinRank         int
	Deleted         bool
}

func (r *Repo) EventPage(ctx context.Context, wsID, pageID string) (EventPage, error) {
	var p EventPage
	err := r.db.Read.QueryRowContext(ctx, `SELECT id, slug, title, min_rank, deleted_at IS NOT NULL FROM pages WHERE id = ? AND workspace_id = ?`,
		pageID, wsID).Scan(&p.ID, &p.Slug, &p.Title, &p.MinRank, &p.Deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return EventPage{}, ErrNotFound
	}
	return p, err
}

// PageWatchers son quienes siguen la página, o un padre "con sus subpáginas", y todavía pueden
// leerla. Con unit, también quienes seguían una subpágina borrada junto con ella (§4.5). Seguir
// es desde ese momento: quien empezó a seguirla después del evento no recibe su aviso, aunque
// el reparto llegue más tarde.
func (r *Repo) PageWatchers(ctx context.Context, e NotificationEvent, p EventPage, unit bool) ([]string, error) {
	q := `SELECT DISTINCT w.user_id FROM page_watchers w
		JOIN pages a ON a.id = w.page_id
		JOIN workspace_members m ON m.workspace_id = a.workspace_id AND m.user_id = w.user_id
		JOIN users u ON u.id = w.user_id AND u.is_active = 1
		WHERE a.workspace_id = ? AND ` + rankSQL + ` >= MAX(?, a.min_rank)
		  AND w.created_at <= (SELECT created_at FROM notification_events WHERE id = ?) AND (a.id = ?`
	args := []any{e.WsID, p.MinRank, e.ID, p.ID}
	if unit {
		q += ` OR a.trash_root_id = ?`
		args = append(args, p.ID)
	}
	if anc := ancestors(p.Slug); len(anc) > 0 {
		q += ` OR (w.include_children = 1 AND a.deleted_at IS NULL AND a.slug IN (?` + strings.Repeat(",?", len(anc)-1) + `))`
		args = append(args, anc...)
	}
	rows, err := r.db.Read.QueryContext(ctx, q+`)`, args...)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (string, error) {
		var id string
		return id, row.Scan(&id)
	})
}

// AreaAdmins son los admins activos del área: a ellos les llegan las propuestas (§5.3).
func (r *Repo) AreaAdmins(ctx context.Context, wsID string) ([]string, error) {
	rows, err := r.db.Read.QueryContext(ctx, `SELECT m.user_id FROM workspace_members m JOIN users u ON u.id = m.user_id
		WHERE m.workspace_id = ? AND m.role = 'admin' AND u.is_active = 1`, wsID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (string, error) {
		var id string
		return id, row.Scan(&id)
	})
}

// AutoWatch hace que quien crea o edita una página la siga, si no lo desactivó (§12.1). No
// toca un seguimiento que ya existe: quien sigue el subárbol lo sigue igual.
func (r *Repo) AutoWatch(ctx context.Context, pageID, userID string) error {
	_, err := r.db.Write.ExecContext(ctx, `INSERT OR IGNORE INTO page_watchers (page_id, user_id)
		SELECT ?, id FROM users WHERE id = ? AND auto_watch = 1`, pageID, userID)
	return err
}

// NewNotification es un aviso para una persona. Con GroupKey, si ya tiene uno sin leer del
// mismo grupo, se suma a ese (§12.1).
type NewNotification struct {
	UserID, Kind, Category, PageID, ProposalID, GroupKey, Payload string
	MinRank                                                       int
}

// ApplyEvent guarda los avisos de un evento y lo borra, en una transacción: un evento se
// reparte una sola vez.
func (r *Repo) ApplyEvent(ctx context.Context, e NotificationEvent, ns []NewNotification) error {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertNotifications(ctx, tx, e.WsID, ns); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM notification_events WHERE id = ?`, e.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// insertNotifications agrupa: si cambia el tipo dentro del grupo (la propuesta que necesitaba
// rehacerse ahora está aprobada), el aviso vuelve a empezar y vuelve a tener correo.
func insertNotifications(ctx context.Context, tx *sql.Tx, wsID string, ns []NewNotification) error {
	for _, n := range ns {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO notifications (id, user_id, workspace_id, kind, category, page_id, proposal_id, min_rank, payload_json, group_key)
			 VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, NULLIF(?, ''))
			 ON CONFLICT (user_id, group_key) WHERE read_at IS NULL AND group_key IS NOT NULL DO UPDATE SET
			   count = CASE WHEN kind = excluded.kind THEN count + 1 ELSE 1 END,
			   emailed_at = CASE WHEN kind = excluded.kind THEN emailed_at END,
			   created_at = CASE WHEN kind = excluded.kind THEN created_at ELSE CURRENT_TIMESTAMP END,
			   kind = excluded.kind, category = excluded.category, page_id = excluded.page_id, proposal_id = excluded.proposal_id,
			   min_rank = excluded.min_rank, payload_json = excluded.payload_json, updated_at = CURRENT_TIMESTAMP`,
			ulid.Make().String(), n.UserID, wsID, n.Kind, n.Category, n.PageID, n.ProposalID, max(1, n.MinRank), n.Payload, n.GroupKey); err != nil {
			return err
		}
	}
	return nil
}

// DueReminder es una propuesta pendiente hace más de lo que espera el recordatorio (§5.3).
type DueReminder struct {
	ID, WsID, Title, AuthorID string
	CreatedAt                 time.Time
}

func (r *Repo) DueReminders(ctx context.Context, before time.Time) ([]DueReminder, error) {
	rows, err := r.db.Read.QueryContext(ctx, `SELECT id, workspace_id, title, author_id, created_at FROM proposals
		WHERE status = 'pending' AND reminded_at IS NULL AND created_at <= ? ORDER BY created_at`, sqlTime(before))
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (DueReminder, error) {
		var d DueReminder
		return d, row.Scan(&d.ID, &d.WsID, &d.Title, &d.AuthorID, &d.CreatedAt)
	})
}

// ApplyReminder guarda los avisos y marca la propuesta como recordada, en una transacción.
func (r *Repo) ApplyReminder(ctx context.Context, d DueReminder, ns []NewNotification) error {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertNotifications(ctx, tx, d.WsID, ns); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE proposals SET reminded_at = CURRENT_TIMESTAMP WHERE id = ?`, d.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// notificationVisible deja solo los avisos que su destinatario todavía puede ver: sigue en el
// área, con el rol que pide el aviso y el nivel actual de la página.
const (
	notificationFrom = ` FROM notifications n
	JOIN workspaces w ON w.id = n.workspace_id
	JOIN workspace_members m ON m.workspace_id = n.workspace_id AND m.user_id = n.user_id
	LEFT JOIN pages p ON p.id = n.page_id`
	notificationVisible = ` AND ` + rankSQL + ` >= MAX(n.min_rank, COALESCE(p.min_rank, 1))`
)

// Notification es un aviso de la bandeja. PageSlug es la ruta actual de la página (vacía si está
// en la papelera); el título y quién lo hizo van en Payload, como estaban al avisar.
type Notification struct {
	ID, Kind, AreaSlug, AreaName, PageSlug, ProposalID, Payload string
	Count                                                       int
	Read                                                        bool
	UpdatedAt                                                   time.Time
}

const notificationColumns = `SELECT n.id, n.kind, w.slug, w.name, CASE WHEN p.deleted_at IS NULL THEN COALESCE(p.slug, '') ELSE '' END,
	COALESCE(n.proposal_id, ''), n.payload_json, n.count, n.read_at IS NOT NULL, n.updated_at`

// Notifications es la bandeja de una persona, lo más reciente primero, y cuántos no leyó.
func (r *Repo) Notifications(ctx context.Context, userID string, limit int) ([]Notification, int, error) {
	rows, err := r.db.Read.QueryContext(ctx, notificationColumns+notificationFrom+` WHERE n.user_id = ?`+notificationVisible+`
		ORDER BY n.updated_at DESC, n.id DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, 0, err
	}
	list, err := collect(rows, func(row scanner) (Notification, error) {
		var n Notification
		return n, row.Scan(&n.ID, &n.Kind, &n.AreaSlug, &n.AreaName, &n.PageSlug, &n.ProposalID, &n.Payload, &n.Count, &n.Read, &n.UpdatedAt)
	})
	if err != nil {
		return nil, 0, err
	}
	var unread int
	err = r.db.Read.QueryRowContext(ctx, `SELECT COUNT(*)`+notificationFrom+` WHERE n.user_id = ? AND n.read_at IS NULL`+notificationVisible, userID).Scan(&unread)
	return list, unread, err
}

// ReadNotifications marca como leídos esos avisos de la persona, o todos si ids está vacío.
func (r *Repo) ReadNotifications(ctx context.Context, userID string, ids []string) error {
	q, args := `UPDATE notifications SET read_at = CURRENT_TIMESTAMP WHERE user_id = ? AND read_at IS NULL`, []any{userID}
	if len(ids) > 0 {
		q += ` AND id IN (?` + strings.Repeat(",?", len(ids)-1) + `)`
		for _, id := range ids {
			args = append(args, id)
		}
	}
	_, err := r.db.Write.ExecContext(ctx, q, args...)
	return err
}

// ReadPageNotifications: abrir una página marca como leídos sus avisos de cambios (§12.1); si
// todavía no salió el correo, ya no sale.
func (r *Repo) ReadPageNotifications(ctx context.Context, userID, pageID string) error {
	_, err := r.db.Write.ExecContext(ctx, `UPDATE notifications SET read_at = CURRENT_TIMESTAMP
		WHERE user_id = ? AND page_id = ? AND category = 'watched' AND read_at IS NULL`, userID, pageID)
	return err
}

// ReadProposalNotifications: lo mismo al abrir una propuesta.
func (r *Repo) ReadProposalNotifications(ctx context.Context, userID, proposalID string) error {
	_, err := r.db.Write.ExecContext(ctx, `UPDATE notifications SET read_at = CURRENT_TIMESTAMP
		WHERE user_id = ? AND proposal_id = ? AND read_at IS NULL`, userID, proposalID)
	return err
}

// ReadPendingNotifications: abrir las pendientes del área marca como leídos sus avisos de
// propuestas por revisar.
func (r *Repo) ReadPendingNotifications(ctx context.Context, s Scope, userID string) error {
	_, err := r.db.Write.ExecContext(ctx, `UPDATE notifications SET read_at = CURRENT_TIMESTAMP
		WHERE user_id = ? AND workspace_id = ? AND category = 'proposals' AND read_at IS NULL`, userID, s.WsID)
	return err
}

// NotificationEmail es un aviso cuyo correo toca resolver, con lo que hace falta para escribirlo.
type NotificationEmail struct {
	Notification
	UserID, Email, FullName, Locale, Category string
	Pref                                      string // immediate | daily | off
}

// DueNotificationEmails son los avisos sin leer y sin correo. Con daily, los de quienes piden
// el resumen diario; si no, los demás que llevan sin leer desde antes de before (§12.1).
func (r *Repo) DueNotificationEmails(ctx context.Context, before time.Time, daily bool, limit int) ([]NotificationEmail, error) {
	q := `SELECT n.id, n.kind, w.slug, w.name, CASE WHEN p.deleted_at IS NULL THEN COALESCE(p.slug, '') ELSE '' END,
		COALESCE(n.proposal_id, ''), n.payload_json, n.count, 0, n.updated_at,
		u.id, u.email, u.full_name, u.locale, n.category, COALESCE(np.email, 'immediate')` + notificationFrom + `
		JOIN users u ON u.id = n.user_id AND u.is_active = 1
		LEFT JOIN notification_prefs np ON np.user_id = n.user_id AND np.category = n.category
		WHERE n.emailed_at IS NULL AND n.read_at IS NULL` + notificationVisible
	args := []any{}
	if daily {
		q += ` AND np.email = 'daily'`
	} else {
		q += ` AND COALESCE(np.email, 'immediate') != 'daily' AND n.created_at <= ?`
		args = append(args, sqlTime(before))
	}
	rows, err := r.db.Read.QueryContext(ctx, q+` ORDER BY n.user_id, n.updated_at LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (NotificationEmail, error) {
		var e NotificationEmail
		return e, row.Scan(&e.ID, &e.Kind, &e.AreaSlug, &e.AreaName, &e.PageSlug, &e.ProposalID, &e.Payload, &e.Count, &e.Read, &e.UpdatedAt,
			&e.UserID, &e.Email, &e.FullName, &e.Locale, &e.Category, &e.Pref)
	})
}

// MarkEmailed da por resuelto el correo de esos avisos: salió, o no correspondía.
func (r *Repo) MarkEmailed(ctx context.Context, ids []string, at time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	args := []any{sqlTime(at)}
	for _, id := range ids {
		args = append(args, id)
	}
	_, err := r.db.Write.ExecContext(ctx, `UPDATE notifications SET emailed_at = ? WHERE id IN (?`+strings.Repeat(",?", len(ids)-1)+`)`, args...)
	return err
}

// NotificationCategories son las categorías de esta fase; las demás de §12.1 llegan con su función.
var NotificationCategories = []string{"proposals", "my_proposals", "watched"}

// NotificationPrefs: si sigue lo que crea o edita, y el correo de cada categoría.
type NotificationPrefs struct {
	AutoWatch bool
	Email     map[string]string
}

func (r *Repo) NotificationPrefs(ctx context.Context, userID string) (NotificationPrefs, error) {
	p := NotificationPrefs{Email: map[string]string{}}
	for _, c := range NotificationCategories {
		p.Email[c] = "immediate"
	}
	if err := r.db.Read.QueryRowContext(ctx, `SELECT auto_watch FROM users WHERE id = ?`, userID).Scan(&p.AutoWatch); err != nil {
		return p, err
	}
	rows, err := r.db.Read.QueryContext(ctx, `SELECT category, email FROM notification_prefs WHERE user_id = ?`, userID)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var c, e string
		if err := rows.Scan(&c, &e); err != nil {
			return p, err
		}
		p.Email[c] = e
	}
	return p, rows.Err()
}

// SetNotificationPrefs guarda lo que trae p; una categoría que no viene queda como estaba.
func (r *Repo) SetNotificationPrefs(ctx context.Context, userID string, p NotificationPrefs) error {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE users SET auto_watch = ? WHERE id = ?`, p.AutoWatch, userID); err != nil {
		return err
	}
	for c, e := range p.Email {
		if err := setEmailPref(ctx, tx, userID, c, e); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetEmailPref cambia el correo de una categoría: lo usa la baja en un clic.
func (r *Repo) SetEmailPref(ctx context.Context, userID, category, email string) error {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := setEmailPref(ctx, tx, userID, category, email); err != nil {
		return err
	}
	return tx.Commit()
}

func setEmailPref(ctx context.Context, tx *sql.Tx, userID, category, email string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO notification_prefs (user_id, category, email) SELECT id, ?, ? FROM users WHERE id = ?
		ON CONFLICT (user_id, category) DO UPDATE SET email = excluded.email`, category, email, userID)
	return err
}

// PageWatch dice cómo sigue una persona una página: "page", "subtree" o "" , y Via, la ruta del
// padre más cercano que sigue con sus subpáginas, si lo hay.
type PageWatch struct {
	Mode, Via string
}

func (r *Repo) PageWatch(ctx context.Context, s Scope, userID string, p Page) (PageWatch, error) {
	var w PageWatch
	var subtree bool
	err := r.db.Read.QueryRowContext(ctx, `SELECT include_children FROM page_watchers WHERE page_id = ? AND user_id = ?`, p.ID, userID).Scan(&subtree)
	switch {
	case err == nil && subtree:
		w.Mode = "subtree"
	case err == nil:
		w.Mode = "page"
	case !errors.Is(err, sql.ErrNoRows):
		return w, err
	}
	anc := ancestors(p.Slug)
	if len(anc) == 0 {
		return w, nil
	}
	err = r.db.Read.QueryRowContext(ctx, `SELECT a.slug FROM page_watchers pw JOIN pages a ON a.id = pw.page_id
		WHERE pw.user_id = ? AND pw.include_children = 1 AND a.workspace_id = ? AND a.deleted_at IS NULL AND a.min_rank <= ?
		  AND a.slug IN (?`+strings.Repeat(",?", len(anc)-1)+`) ORDER BY length(a.slug) DESC LIMIT 1`,
		append([]any{userID, s.WsID, s.MaxRank}, anc...)...).Scan(&w.Via)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return w, err
	}
	return w, nil
}

// Watch sigue una página del Scope, sola o con sus subpáginas.
func (r *Repo) Watch(ctx context.Context, s Scope, userID, pageID string, subtree bool) error {
	return affected(r.db.Write.ExecContext(ctx, `INSERT INTO page_watchers (page_id, user_id, include_children)
		SELECT id, ?, ? FROM pages WHERE id = ? AND workspace_id = ? AND min_rank <= ? AND deleted_at IS NULL
		ON CONFLICT (page_id, user_id) DO UPDATE SET include_children = excluded.include_children`,
		userID, subtree, pageID, s.WsID, s.MaxRank))
}

// Unwatch deja de seguirla. No seguirla ya no es un error.
func (r *Repo) Unwatch(ctx context.Context, s Scope, userID, pageID string) error {
	_, err := r.db.Write.ExecContext(ctx, `DELETE FROM page_watchers WHERE user_id = ? AND page_id IN
		(SELECT id FROM pages WHERE id = ? AND workspace_id = ? AND min_rank <= ?)`, userID, pageID, s.WsID, s.MaxRank)
	return err
}
