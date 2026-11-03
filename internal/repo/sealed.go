package repo

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Sellados (SPEC §6, ADR-0004, ADR-0011). No se leen con un Scope: quien abre uno puede no ser
// miembro de su área (el superadmin, y desde F7.2 las personas con acceso), así que la vía es
// aparte y con nombre propio, como la de los artículos compartidos (ADR-0010). La regla de quién
// lo ve está una sola vez, en sealedVisible.

// SealedDoc es un sellado con lo que se guarda en claro y la relación de quien pregunta con él.
type SealedDoc struct {
	ID, WsID, WsSlug, WsName, Slug string
	Personal                       bool
	AuthorID, Author               string
	UpdatedBy                      string
	UpdatedAt                      time.Time
	Revision                       int
	IsAuthor, IsAreaAdmin          bool
	HasPassword                    bool      // false tras pasar a otro autor en una baja (§8.4)
	Grant                          string    // "", read o edit: el acceso dado a esa persona (F7.2)
	ResetRequested                 bool      // el autor pidió restablecer la contraseña (F7.3)
	ResetExpires                   time.Time // hasta cuándo vale la aprobación del superadmin; cero si no hay
}

// SealedKey es una DEK envuelta. Salt solo en la de contraseña.
type SealedKey struct {
	Type, UserID, Permission string
	Salt, Wrapped            []byte
	KeyVersion               int
}

type SealedRevision struct {
	Number    int
	Payload   []byte
	CreatedBy string
	CreatedAt time.Time
}

// NewSealed es un sellado nuevo con su revisión 1 y sus llaves.
type NewSealed struct {
	ID, Slug, AuthorID string
	Payload            []byte
	Keys               []SealedKey
}

// SealedRef es un sellado en el árbol del área, para quien lo puede ver: el título va cifrado.
type SealedRef struct{ ID, Slug string }

// sealedVisible: quien pregunta (primer ?) es el autor y sigue en el área, un admin del área (rol
// efectivo admin) o tiene un acceso dado, que es por persona y no por área (§8.3.3); el superadmin
// (segundo ?) los ve todos (§6.1). Los demás, también el autor que salió del área: no existe (§3.3).
const sealedVisible = ` ((sp.author_id = ?1 AND m.user_id IS NOT NULL) OR ?2 OR m.role = 'admin'
	OR EXISTS (SELECT 1 FROM sealed_keys g WHERE g.page_id = sp.id AND g.holder_type = 'user' AND g.holder_user_id = ?1))`

const sealedFrom = ` FROM sealed_pages sp
	JOIN workspaces w ON w.id = sp.workspace_id
	JOIN users a ON a.id = sp.author_id
	JOIN users ub ON ub.id = sp.updated_by
	LEFT JOIN workspace_members m ON m.workspace_id = sp.workspace_id AND m.user_id = ?1`

// SealedByID es el sellado si quien pregunta lo puede ver; si no, ErrNotFound, igual que si no existiera.
func (r *Repo) SealedByID(ctx context.Context, userID string, super bool, id string) (SealedDoc, error) {
	var d SealedDoc
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT sp.id, w.id, w.slug, w.name, sp.slug, w.kind = 'personal', sp.author_id, a.full_name, ub.full_name, sp.updated_at,
		   sp.current_revision, sp.author_id = ?1, COALESCE(m.role = 'admin', 0),
		   EXISTS (SELECT 1 FROM sealed_keys k WHERE k.page_id = sp.id AND k.holder_type = 'password'),
		   COALESCE((SELECT g.permission FROM sealed_keys g WHERE g.page_id = sp.id AND g.holder_type = 'user' AND g.holder_user_id = ?1), ''),
		   pr.requested_at IS NOT NULL, pr.expires_at`+
			sealedFrom+` LEFT JOIN sealed_password_resets pr ON pr.page_id = sp.id WHERE sp.id = ?3 AND`+sealedVisible, userID, super, id).
		Scan(&d.ID, &d.WsID, &d.WsSlug, &d.WsName, &d.Slug, &d.Personal, &d.AuthorID, &d.Author, &d.UpdatedBy, &d.UpdatedAt,
			&d.Revision, &d.IsAuthor, &d.IsAreaAdmin, &d.HasPassword, &d.Grant, &d.ResetRequested, (*zeroTime)(&d.ResetExpires))
	if errors.Is(err, sql.ErrNoRows) {
		return SealedDoc{}, ErrNotFound
	}
	return d, err
}

// SealedInArea son los sellados del área de s que quien pregunta puede ver, para su árbol.
func (r *Repo) SealedInArea(ctx context.Context, s Scope, userID string, super bool) ([]SealedRef, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT sp.id, sp.slug`+sealedFrom+` WHERE sp.workspace_id = ?3 AND`+sealedVisible+` ORDER BY sp.slug`,
		userID, super, s.WsID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (SealedRef, error) {
		var ref SealedRef
		return ref, row.Scan(&ref.ID, &ref.Slug)
	})
}

// CreateSealed guarda el sellado en el área de s, con su revisión 1 y sus llaves. Una ruta ocupada,
// por una página viva o por otro sellado, da ErrConflict.
func (r *Repo) CreateSealed(ctx context.Context, s Scope, d NewSealed) error {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO sealed_pages (id, workspace_id, slug, author_id, updated_by) VALUES (?, ?, ?, ?, ?)`,
		d.ID, s.WsID, d.Slug, d.AuthorID, d.AuthorID); err != nil {
		return conflict(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sealed_revisions (page_id, revision_number, payload, created_by) VALUES (?, 1, ?, ?)`,
		d.ID, d.Payload, d.AuthorID); err != nil {
		return err
	}
	for _, k := range d.Keys {
		if err := insertSealedKey(ctx, tx, d.ID, d.AuthorID, k); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AddSealedKey agrega una DEK envuelta; si ya hay una de ese tipo (y persona), ErrConflict.
func (r *Repo) AddSealedKey(ctx context.Context, pageID, grantedBy string, k SealedKey) error {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertSealedKey(ctx, tx, pageID, grantedBy, k); err != nil {
		return conflict(err)
	}
	return tx.Commit()
}

func insertSealedKey(ctx context.Context, tx *sql.Tx, pageID, grantedBy string, k SealedKey) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO sealed_keys (page_id, holder_type, holder_user_id, permission, kdf_salt, wrapped_dek, key_version, granted_by)
		 VALUES (?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?)`,
		pageID, k.Type, k.UserID, cmpOr(k.Permission, "read"), k.Salt, k.Wrapped, max(k.KeyVersion, 1), grantedBy)
	return err
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// SealedKeyOf es la DEK envuelta de un tipo (y de una persona, para "user").
func (r *Repo) SealedKeyOf(ctx context.Context, pageID, holderType, userID string) (SealedKey, error) {
	var k SealedKey
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT holder_type, COALESCE(holder_user_id, ''), permission, kdf_salt, wrapped_dek, key_version FROM sealed_keys
		 WHERE page_id = ? AND holder_type = ? AND IFNULL(holder_user_id, '') = ?`, pageID, holderType, userID).
		Scan(&k.Type, &k.UserID, &k.Permission, &k.Salt, &k.Wrapped, &k.KeyVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return SealedKey{}, ErrNotFound
	}
	return k, err
}

// SealedRevisionOf es una revisión cifrada; n = 0, la vigente.
func (r *Repo) SealedRevisionOf(ctx context.Context, pageID string, n int) (SealedRevision, error) {
	var rev SealedRevision
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT r.revision_number, r.payload, u.full_name, r.created_at FROM sealed_revisions r JOIN users u ON u.id = r.created_by
		 WHERE r.page_id = ? AND r.revision_number = CASE WHEN ? > 0 THEN ? ELSE (SELECT current_revision FROM sealed_pages WHERE id = r.page_id) END`,
		pageID, n, n).Scan(&rev.Number, &rev.Payload, &rev.CreatedBy, &rev.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SealedRevision{}, ErrNotFound
	}
	return rev, err
}

// SealedHistory son las revisiones, la más nueva primero, sin el contenido cifrado.
func (r *Repo) SealedHistory(ctx context.Context, pageID string) ([]SealedRevision, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT r.revision_number, u.full_name, r.created_at FROM sealed_revisions r JOIN users u ON u.id = r.created_by
		 WHERE r.page_id = ? ORDER BY r.revision_number DESC`, pageID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (SealedRevision, error) {
		var rev SealedRevision
		return rev, row.Scan(&rev.Number, &rev.CreatedBy, &rev.CreatedAt)
	})
}

// SaveSealed agrega la revisión base+1. Si otra persona guardó antes, ErrConflict sin tocar nada:
// el payload ya va atado a su número de revisión (aad), así que no se puede correr a la siguiente.
func (r *Repo) SaveSealed(ctx context.Context, pageID, actorID string, base int, payload []byte) error {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`UPDATE sealed_pages SET current_revision = current_revision + 1, updated_by = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND current_revision = ?`, actorID, pageID, base)
	if errors.Is(affected(res, err), ErrNotFound) {
		return ErrConflict
	} else if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sealed_revisions (page_id, revision_number, payload, created_by) VALUES (?, ?, ?, ?)`,
		pageID, base+1, payload, actorID); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteSealed borra el sellado para siempre (§6.1): página, revisiones y todas sus DEK envueltas
// en una transacción, con secure_delete para que SQLite pise lo borrado, y después un checkpoint
// del WAL para que no quede ahí. Sin las DEK, cualquier resto es ilegible (crypto-shredding).
func (r *Repo) DeleteSealed(ctx context.Context, pageID string) error {
	return r.shredSealed(ctx, pageID, func(*sql.Tx) error { return nil })
}

// shredSealed borra el sellado en una transacción con secure_delete, corre then en esa misma
// transacción y, confirmada, hace el checkpoint del WAL.
func (r *Repo) shredSealed(ctx context.Context, pageID string, then func(*sql.Tx) error) error {
	// La conexión de escritura es una sola: el PRAGMA vale para la transacción que sigue. Se
	// apaga después del Rollback (los defer corren al revés): con la transacción abierta, otro
	// ExecContext esperaría la conexión para siempre.
	if _, err := r.db.Write.ExecContext(ctx, `PRAGMA secure_delete = ON`); err != nil {
		return err
	}
	defer r.db.Write.ExecContext(context.WithoutCancel(ctx), `PRAGMA secure_delete = OFF`)
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM sealed_keys WHERE page_id = ?`,
		`DELETE FROM sealed_revisions WHERE page_id = ?`,
		`DELETE FROM sealed_password_resets WHERE page_id = ?`,
		`DELETE FROM sealed_media WHERE page_id = ?`, // los .enc los borra quien llama, confirmado esto
	} {
		if _, err := tx.ExecContext(ctx, q, pageID); err != nil {
			return err
		}
	}
	if err := affected(tx.ExecContext(ctx, `DELETE FROM sealed_pages WHERE id = ?`, pageID)); err != nil {
		return err
	}
	if err := then(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	_, err = r.db.Write.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}

// ----- Desclasificar y restablecer la contraseña (§6.1, §6.3, F7.3) -----

// DeclassifySealed convierte el sellado en una página del área de s, en una transacción: lo
// borra como un borrado definitivo (sin él, la ruta deja de estar reservada) y crea la página en
// su ruta con al menos el nivel level. Las subpáginas que ya había bajo esa ruta suben a ese
// nivel, como al subir el de un padre (§3.2), con actorID como autor de ese cambio. Devuelve el
// id de la página.
func (r *Repo) DeclassifySealed(ctx context.Context, s Scope, pageID, actorID string, level int, w PageWrite) (string, error) {
	var id string
	err := r.shredSealed(ctx, pageID, func(tx *sql.Tx) error {
		var err error
		if id, err = createPage(ctx, tx, s, w, level); err != nil {
			return err
		}
		_, _, err = setLevel(ctx, tx, s, actorID, w.Slug, level)
		return err
	})
	return id, err
}

// RequestSealedReset anota que el autor pidió restablecer la contraseña. Una aprobación vigente
// se conserva.
func (r *Repo) RequestSealedReset(ctx context.Context, pageID, userID string, now time.Time) error {
	_, err := r.db.Write.ExecContext(ctx,
		`INSERT INTO sealed_password_resets (page_id, requested_by, requested_at) VALUES (?, ?, ?)
		 ON CONFLICT(page_id) DO UPDATE SET requested_by = excluded.requested_by, requested_at = excluded.requested_at`,
		pageID, userID, now.UTC())
	return err
}

// ApproveSealedReset aprueba el restablecimiento hasta expires, con o sin pedido del autor.
func (r *Repo) ApproveSealedReset(ctx context.Context, pageID, by string, now, expires time.Time) error {
	_, err := r.db.Write.ExecContext(ctx,
		`INSERT INTO sealed_password_resets (page_id, approved_by, approved_at, expires_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(page_id) DO UPDATE SET approved_by = excluded.approved_by, approved_at = excluded.approved_at,
		   expires_at = excluded.expires_at`,
		pageID, by, now.UTC(), expires.UTC())
	return err
}

// ResetSealedPassword reemplaza la DEK envuelta con la contraseña y gasta la aprobación, en una
// transacción. Sin una aprobación vigente, ErrNotFound y nada cambia.
func (r *Repo) ResetSealedPassword(ctx context.Context, pageID, actorID string, k SealedKey, now time.Time) error {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := affected(tx.ExecContext(ctx, `DELETE FROM sealed_password_resets WHERE page_id = ? AND expires_at > ?`, pageID, now.UTC())); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sealed_keys WHERE page_id = ? AND holder_type = 'password'`, pageID); err != nil {
		return err
	}
	if err := insertSealedKey(ctx, tx, pageID, actorID, k); err != nil {
		return err
	}
	return tx.Commit()
}

// AdminSealed es un sellado en la lista del superadmin, que no recorre las áreas (§6.1).
type AdminSealed struct {
	ID, Slug, WsSlug, WsName string
	Personal                 bool
	Author                   string
	UpdatedAt                time.Time
	ResetRequested           bool
	ResetExpires             time.Time
}

// AllSealed son todos los sellados, primero los que tienen un pedido de restablecimiento. Solo
// para el superadmin, que ya los ve todos (§6.1): sale solo lo que se guarda en claro.
func (r *Repo) AllSealed(ctx context.Context) ([]AdminSealed, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT sp.id, sp.slug, w.slug, w.name, w.kind = 'personal', a.full_name, sp.updated_at,
		   pr.requested_at IS NOT NULL, pr.expires_at
		 FROM sealed_pages sp JOIN workspaces w ON w.id = sp.workspace_id JOIN users a ON a.id = sp.author_id
		 LEFT JOIN sealed_password_resets pr ON pr.page_id = sp.id
		 ORDER BY pr.requested_at IS NULL, w.name, sp.slug`)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (AdminSealed, error) {
		var s AdminSealed
		return s, row.Scan(&s.ID, &s.Slug, &s.WsSlug, &s.WsName, &s.Personal, &s.Author, &s.UpdatedAt, &s.ResetRequested, (*zeroTime)(&s.ResetExpires))
	})
}

// zeroTime lee un DATETIME que puede ser NULL como el tiempo cero.
type zeroTime time.Time

func (t *zeroTime) Scan(v any) error {
	var n sql.NullTime
	if err := n.Scan(v); err != nil {
		return err
	}
	*t = zeroTime(n.Time)
	return nil
}

// ----- Bóveda y accesos (§6.3, F7.2) -----

// Vault es la bóveda de una persona: su pública y la privada cifrada con su clave de bóveda.
type Vault struct{ Pub, PrivEnc, Salt []byte }

// VaultOf es la bóveda de la persona; ErrNotFound si no la configuró.
func (r *Repo) VaultOf(ctx context.Context, userID string) (Vault, error) {
	var v Vault
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT vault_pubkey, vault_privkey_enc, vault_salt FROM users WHERE id = ? AND vault_pubkey IS NOT NULL`, userID).
		Scan(&v.Pub, &v.PrivEnc, &v.Salt)
	if errors.Is(err, sql.ErrNoRows) {
		return Vault{}, ErrNotFound
	}
	return v, err
}

// SetVault guarda una bóveda nueva. Las DEK envueltas para la anterior ya no abren con esta: se
// borran, y quien compartió vuelve a hacerlo (§6.3). Devuelve cuántos accesos se perdieron.
func (r *Repo) SetVault(ctx context.Context, userID string, v Vault) (int64, error) {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := affected(tx.ExecContext(ctx, `UPDATE users SET vault_pubkey = ?, vault_privkey_enc = ?, vault_salt = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, v.Pub, v.PrivEnc, v.Salt, userID)); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM sealed_keys WHERE holder_type = 'user' AND holder_user_id = ?`, userID)
	if err != nil {
		return 0, err
	}
	lost, _ := res.RowsAffected()
	return lost, tx.Commit()
}

// SealedGrant es un acceso dado a una persona.
type SealedGrant struct {
	UserID, Name, Email, Permission string
	CreatedAt                       time.Time
}

// SealedGrants son los accesos dados en el sellado, por nombre.
func (r *Repo) SealedGrants(ctx context.Context, pageID string) ([]SealedGrant, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT u.id, u.full_name, u.email, k.permission, k.created_at FROM sealed_keys k JOIN users u ON u.id = k.holder_user_id
		 WHERE k.page_id = ? AND k.holder_type = 'user' ORDER BY u.full_name`, pageID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (SealedGrant, error) {
		var g SealedGrant
		return g, row.Scan(&g.UserID, &g.Name, &g.Email, &g.Permission, &g.CreatedAt)
	})
}

// PutSealedGrant da el acceso o lo reemplaza (otro permiso, o una bóveda nueva).
func (r *Repo) PutSealedGrant(ctx context.Context, pageID, grantedBy string, k SealedKey) error {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM sealed_keys WHERE page_id = ? AND holder_type = 'user' AND holder_user_id = ?`, pageID, k.UserID); err != nil {
		return err
	}
	if err := insertSealedKey(ctx, tx, pageID, grantedBy, k); err != nil {
		return err
	}
	return tx.Commit()
}

// RevokeSealedGrant borra la DEK envuelta de esa persona; ErrNotFound si no tenía acceso.
func (r *Repo) RevokeSealedGrant(ctx context.Context, pageID, userID string) error {
	return affected(r.db.Write.ExecContext(ctx,
		`DELETE FROM sealed_keys WHERE page_id = ? AND holder_type = 'user' AND holder_user_id = ?`, pageID, userID))
}

// SealedHolders son quienes tienen un acceso dado al sellado.
func (r *Repo) SealedHolders(ctx context.Context, pageID string) ([]string, error) {
	rows, err := r.db.Read.QueryContext(ctx, `SELECT holder_user_id FROM sealed_keys WHERE page_id = ? AND holder_type = 'user'`, pageID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (string, error) {
		var id string
		return id, row.Scan(&id)
	})
}

// MySealed es un sellado propio o compartido conmigo, de cualquier área (§14.4 /me/sealed).
type MySealed struct {
	ID, Slug, WsSlug, WsName string
	Personal                 bool
	Author                   string
	Access                   string // author, read o edit
	UpdatedAt                time.Time
}

// MySealedDocs son los sellados que la persona escribió (en áreas donde sigue) y los que le
// compartieron. Los que ve solo por ser admin o superadmin no: esos están en el árbol del área.
func (r *Repo) MySealedDocs(ctx context.Context, userID string) ([]MySealed, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT sp.id, sp.slug, w.slug, w.name, w.kind = 'personal', a.full_name,
		   CASE WHEN sp.author_id = ?1 THEN 'author' ELSE g.permission END, sp.updated_at
		 FROM sealed_pages sp JOIN workspaces w ON w.id = sp.workspace_id JOIN users a ON a.id = sp.author_id
		 LEFT JOIN sealed_keys g ON g.page_id = sp.id AND g.holder_type = 'user' AND g.holder_user_id = ?1
		 WHERE (sp.author_id = ?1 AND EXISTS (SELECT 1 FROM workspace_members m WHERE m.workspace_id = sp.workspace_id AND m.user_id = ?1))
		    OR g.page_id IS NOT NULL
		 ORDER BY sp.updated_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (MySealed, error) {
		var m MySealed
		return m, row.Scan(&m.ID, &m.Slug, &m.WsSlug, &m.WsName, &m.Personal, &m.Author, &m.Access, &m.UpdatedAt)
	})
}

// SealedMedia es un archivo de un sellado (F7.6): en disco, storage/ws_<WsID>/<ID>.enc, cifrado
// con la DEK; su nombre y su tipo van en Meta, también cifrados.
type SealedMedia struct {
	ID, PageID, WsID string
	Meta             []byte
	Size             int64
	CreatedAt        time.Time
}

func (r *Repo) AddSealedMedia(ctx context.Context, m SealedMedia, uploadedBy string) error {
	return affected(r.db.Write.ExecContext(ctx,
		`INSERT INTO sealed_media (id, page_id, meta_enc, file_size_bytes, uploaded_by)
		 SELECT ?, id, ?, ?, ? FROM sealed_pages WHERE id = ?`, m.ID, m.Meta, m.Size, uploadedBy, m.PageID))
}

// SealedMediaOf es un archivo del sellado pageID. Quien llama ya comprobó que lo ve.
func (r *Repo) SealedMediaOf(ctx context.Context, pageID, id string) (SealedMedia, error) {
	m := SealedMedia{ID: id, PageID: pageID}
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT s.workspace_id, m.meta_enc, m.file_size_bytes, m.created_at FROM sealed_media m
		 JOIN sealed_pages s ON s.id = m.page_id WHERE m.page_id = ? AND m.id = ?`, pageID, id).
		Scan(&m.WsID, &m.Meta, &m.Size, &m.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

// SealedMediaList son los archivos de un sellado, para desclasificarlo o borrarlos del disco.
func (r *Repo) SealedMediaList(ctx context.Context, pageID string) ([]SealedMedia, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT m.id, m.page_id, s.workspace_id, m.meta_enc, m.file_size_bytes, m.created_at FROM sealed_media m
		 JOIN sealed_pages s ON s.id = m.page_id WHERE m.page_id = ? ORDER BY m.id`, pageID)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanSealedMedia)
}

// SealedMediaInArea son los archivos de sellados de un área, para que doctor los distinga de un
// archivo sin fila (§13.7).
func (r *Repo) SealedMediaInArea(ctx context.Context, wsID string) ([]SealedMedia, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT m.id, m.page_id, s.workspace_id, m.meta_enc, m.file_size_bytes, m.created_at FROM sealed_media m
		 JOIN sealed_pages s ON s.id = m.page_id WHERE s.workspace_id = ?`, wsID)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanSealedMedia)
}

func scanSealedMedia(row scanner) (SealedMedia, error) {
	var m SealedMedia
	return m, row.Scan(&m.ID, &m.PageID, &m.WsID, &m.Meta, &m.Size, &m.CreatedAt)
}

// SealedWithoutMasterKey son las rutas de los sellados del área sin su DEK envuelta con la
// MASTER_KEY: ningún admin los podría abrir (§6.2) y la rotación no los alcanzaría (§6.5).
func (r *Repo) SealedWithoutMasterKey(ctx context.Context, wsID string) ([]string, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT slug FROM sealed_pages s WHERE workspace_id = ? AND NOT EXISTS
		   (SELECT 1 FROM sealed_keys k WHERE k.page_id = s.id AND k.holder_type = 'master') ORDER BY slug`, wsID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (string, error) {
		var slug string
		return slug, row.Scan(&slug)
	})
}
