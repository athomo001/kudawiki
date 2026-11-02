package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/oklog/ulid/v2"
)

// LDAPMapping es una regla grupo del directorio → (área, rol) (SPEC §8.3.1). Solo la ve y la
// cambia el superadmin: las áreas no la consultan, así que no lleva Scope.
type LDAPMapping struct {
	ID        string
	GroupGUID string
	GroupSID  string // solo en AD
	GroupDN   string
	GroupName string
	WsID      string
	WsSlug    string
	WsName    string
	Role      string
	CreatedBy string
}

const mappingColumns = `g.id, g.group_guid, COALESCE(g.group_sid, ''), g.group_dn, g.group_name, g.workspace_id, w.slug, w.name, g.role, g.created_by`

func scanMapping(row scanner) (LDAPMapping, error) {
	var m LDAPMapping
	err := row.Scan(&m.ID, &m.GroupGUID, &m.GroupSID, &m.GroupDN, &m.GroupName, &m.WsID, &m.WsSlug, &m.WsName, &m.Role, &m.CreatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return LDAPMapping{}, ErrNotFound
	}
	return m, err
}

// LDAPMappings lista las reglas por grupo y área.
func (r *Repo) LDAPMappings(ctx context.Context) ([]LDAPMapping, error) {
	rows, err := r.db.Read.QueryContext(ctx, `SELECT `+mappingColumns+` FROM ldap_group_mappings g
		JOIN workspaces w ON w.id = g.workspace_id ORDER BY g.group_name, w.name`)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanMapping)
}

func (r *Repo) LDAPMappingByID(ctx context.Context, id string) (LDAPMapping, error) {
	return scanMapping(r.db.Read.QueryRowContext(ctx, `SELECT `+mappingColumns+` FROM ldap_group_mappings g
		JOIN workspaces w ON w.id = g.workspace_id WHERE g.id = ?`, id))
}

// CreateLDAPMapping guarda la regla y devuelve su ID. El mismo grupo dos veces en un área es
// ErrConflict: se cambia el rol de la que ya existe.
func (r *Repo) CreateLDAPMapping(ctx context.Context, m LDAPMapping) (string, error) {
	id := ulid.Make().String()
	_, err := r.db.Write.ExecContext(ctx, `INSERT INTO ldap_group_mappings
		(id, group_guid, group_sid, group_dn, group_name, workspace_id, role, created_by) VALUES (?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?)`,
		id, m.GroupGUID, m.GroupSID, m.GroupDN, m.GroupName, m.WsID, m.Role, m.CreatedBy)
	return id, conflict(err)
}

func (r *Repo) SetLDAPMappingRole(ctx context.Context, id, role string) error {
	return affected(r.db.Write.ExecContext(ctx, `UPDATE ldap_group_mappings SET role = ? WHERE id = ?`, role, id))
}

func (r *Repo) DeleteLDAPMapping(ctx context.Context, id string) error {
	return affected(r.db.Write.ExecContext(ctx, `DELETE FROM ldap_group_mappings WHERE id = ?`, id))
}

// LDAPAccount es una cuenta del directorio que ya existe en la wiki, con su rol efectivo en un
// área ("" si no es miembro).
type LDAPAccount struct {
	UserID   string
	GUID     string
	FullName string
	Role     string
}

// LDAPAccounts busca, entre las identidades dadas, las que ya tienen cuenta, y su rol en el área:
// es lo que la vista previa de una regla necesita para decir a quién agrega y a quién sube (§8.3.4).
func (r *Repo) LDAPAccounts(ctx context.Context, wsID string, guids []string) ([]LDAPAccount, error) {
	list, _ := json.Marshal(guids)
	rows, err := r.db.Read.QueryContext(ctx, `SELECT u.id, u.ldap_guid, u.full_name, COALESCE(m.role, '') FROM users u
		LEFT JOIN workspace_members m ON m.user_id = u.id AND m.workspace_id = ?
		WHERE u.ldap_guid IN (SELECT value FROM json_each(?)) ORDER BY u.full_name`, wsID, string(list))
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (LDAPAccount, error) {
		var a LDAPAccount
		return a, row.Scan(&a.UserID, &a.GUID, &a.FullName, &a.Role)
	})
}

// UserByLDAPGUID busca la cuenta de una persona del directorio por su identidad, no por el
// correo: si el correo cambia en el directorio, la cuenta sigue siendo la misma (§8.3.5).
func (r *Repo) UserByLDAPGUID(ctx context.Context, guid string) (User, error) {
	return scanUser(r.db.Read.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE ldap_guid = ?`, guid))
}

// CreateLDAPUser crea la cuenta de quien entra por primera vez con el directorio (JIT, §8.3.3):
// sin contraseña local. Un correo o una identidad que ya existen son ErrConflict.
func (r *Repo) CreateLDAPUser(ctx context.Context, u User, dn string) error {
	_, err := r.db.Write.ExecContext(ctx,
		`INSERT INTO users (id, email, full_name, auth_source, ldap_dn, ldap_guid, is_active, locale) VALUES (?, ?, ?, 'ldap', ?, ?, 1, ?)`,
		u.ID, u.Email, u.FullName, dn, u.LDAPGUID, u.Locale)
	return conflict(err)
}

// UpdateLDAPUser trae del directorio el correo, el nombre y el DN (§8.6.3): en la wiki no se
// editan. Un correo que ya usa otra cuenta es ErrConflict.
func (r *Repo) UpdateLDAPUser(ctx context.Context, id, email, fullName, dn string) error {
	_, err := r.db.Write.ExecContext(ctx,
		`UPDATE users SET email = ?, full_name = ?, ldap_dn = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND auth_source = 'ldap'`,
		email, fullName, dn, id)
	return conflict(err)
}

// LocalSuperadmins cuenta los superadmins activos que entran con contraseña local: tiene que
// quedar al menos uno, para entrar aunque el directorio no responda (§8.3.4).
func (r *Repo) LocalSuperadmins(ctx context.Context) (int, error) {
	var n int
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE is_global_admin = 1 AND is_active = 1 AND auth_source = 'local' AND password_hash IS NOT NULL`).Scan(&n)
	return n, err
}

// LinkLDAPUser deja una cuenta local como cuenta del directorio (§8.6.3): sin contraseña local,
// con el correo y el nombre del directorio, y sin sesiones ni enlaces abiertos, que se abrieron
// con la contraseña que ya no existe. Devuelve cuántas sesiones cerró. Un correo o una identidad
// de otra cuenta son ErrConflict.
func (r *Repo) LinkLDAPUser(ctx context.Context, id, guid, dn, email, fullName string) (int64, error) {
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`UPDATE users SET auth_source = 'ldap', ldap_guid = ?, ldap_dn = ?, email = ?, full_name = ?, password_hash = NULL,
		 updated_at = CURRENT_TIMESTAMP WHERE id = ? AND auth_source = 'local'`, guid, dn, email, fullName, id)
	if err != nil {
		return 0, conflict(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM auth_tokens WHERE user_id = ?`, id); err != nil {
		return 0, err
	}
	res, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id)
	if err != nil {
		return 0, err
	}
	closed, _ := res.RowsAffected()
	return closed, tx.Commit()
}

// LDAPUsers son todas las cuentas del directorio, también las desactivadas: la sincronización
// compara con ellas lo que encuentra en el directorio (§8.3.3).
func (r *Repo) LDAPUsers(ctx context.Context) ([]User, error) {
	rows, err := r.db.Read.QueryContext(ctx, `SELECT `+userColumns+` FROM users WHERE auth_source = 'ldap' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanUser)
}

// LDAPMembership es una membresía de área con rol del directorio.
type LDAPMembership struct {
	UserID, WsID, Role, MappingID string
}

// LDAPMemberships son todas las membresías de área con rol del directorio: lo que la
// sincronización puede quitar, y su total es la base del tope de bajas (§8.3.4).
func (r *Repo) LDAPMemberships(ctx context.Context) ([]LDAPMembership, error) {
	rows, err := r.db.Read.QueryContext(ctx, `SELECT m.user_id, m.workspace_id, m.ldap_role, COALESCE(m.ldap_mapping_id, '')
		FROM workspace_members m JOIN workspaces w ON w.id = m.workspace_id
		WHERE m.ldap_role IS NOT NULL AND w.kind = 'area'`)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (LDAPMembership, error) {
		var m LDAPMembership
		return m, row.Scan(&m.UserID, &m.WsID, &m.Role, &m.MappingID)
	})
}

// LastLDAPSync es cuándo terminó la última sincronización y, si se abortó, por qué; cero si
// nunca corrió. Sale de la auditoría, así también cuenta la de la consola.
func (r *Repo) LastLDAPSync(ctx context.Context) (time.Time, string, error) {
	var at, aborted string
	err := r.db.Read.QueryRowContext(ctx, `SELECT at, CASE action WHEN 'ldap.sync_aborted'
		THEN COALESCE(json_extract(meta_json, '$.reason'), 'aborted') ELSE '' END
		FROM audit_log WHERE action IN ('ldap.sync_done', 'ldap.sync_aborted') ORDER BY id DESC LIMIT 1`).Scan(&at, &aborted)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, "", nil
	}
	if err != nil {
		return time.Time{}, "", err
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	return t, aborted, err
}

// DirectoryGrant es lo que las reglas de grupos le dan a una persona en un área: el rol, la regla
// que lo da y el nombre de su grupo, para la auditoría.
type DirectoryGrant struct {
	WsID, Role, MappingID, Group string
}

// MemberSource es de dónde viene el rol de un miembro (§8.3.2): Role es el efectivo, el más alto
// de ManualRole y LDAPRole. Todo vacío si no es miembro.
type MemberSource struct {
	WsID       string
	Role       string
	ManualRole string
	LDAPRole   string
	MappingID  string
}

const memberSourceColumns = `workspace_id, role, COALESCE(manual_role, ''), COALESCE(ldap_role, ''), COALESCE(ldap_mapping_id, '')`

func scanMemberSource(row scanner) (MemberSource, error) {
	var m MemberSource
	err := row.Scan(&m.WsID, &m.Role, &m.ManualRole, &m.LDAPRole, &m.MappingID)
	if errors.Is(err, sql.ErrNoRows) {
		return MemberSource{}, nil
	}
	return m, err
}

func (r *Repo) MemberSourceOf(ctx context.Context, s Scope, userID string) (MemberSource, error) {
	return scanMemberSource(r.db.Read.QueryRowContext(ctx, `SELECT `+memberSourceColumns+` FROM workspace_members
		WHERE workspace_id = ? AND user_id = ?`, s.WsID, userID))
}

// MemberSources son las membresías de una persona en todas sus áreas: lo que la sincronización
// con el directorio compara con lo que dan las reglas. No son contenido de un área, así que no
// llevan Scope, como el resto de lo de usuarios.
func (r *Repo) MemberSources(ctx context.Context, userID string) ([]MemberSource, error) {
	rows, err := r.db.Read.QueryContext(ctx, `SELECT `+memberSourceColumns+` FROM workspace_members m
		JOIN workspaces w ON w.id = m.workspace_id WHERE m.user_id = ? AND w.kind = 'area'`, userID)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanMemberSource)
}

// SetLDAPRole deja el rol que da el directorio y recalcula el efectivo; si no era miembro, lo
// agrega.
func (r *Repo) SetLDAPRole(ctx context.Context, s Scope, userID, role, mappingID string) error {
	_, err := r.db.Write.ExecContext(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role, ldap_role, ldap_mapping_id) VALUES (?, ?, ?, ?, NULLIF(?, ''))
		 ON CONFLICT (workspace_id, user_id) DO UPDATE SET ldap_role = excluded.ldap_role, ldap_mapping_id = excluded.ldap_mapping_id,
		   role = `+higherRole("manual_role", "excluded.ldap_role"), s.WsID, userID, role, role, mappingID)
	return err
}

// DropLDAPRole quita lo que daba el directorio a quien también es miembro a mano: queda con su
// rol manual.
func (r *Repo) DropLDAPRole(ctx context.Context, s Scope, userID string) error {
	return affected(r.db.Write.ExecContext(ctx, `UPDATE workspace_members SET ldap_role = NULL, ldap_mapping_id = NULL, role = manual_role
		WHERE workspace_id = ? AND user_id = ? AND manual_role IS NOT NULL`, s.WsID, userID))
}

// DetachLDAPRole convierte en manual lo que daba el directorio (desanclar, §8.3.2): queda con el
// rol que tenía, y salir del grupo ya no se lo quita.
func (r *Repo) DetachLDAPRole(ctx context.Context, s Scope, userID string) error {
	return affected(r.db.Write.ExecContext(ctx, `UPDATE workspace_members SET manual_role = role, ldap_role = NULL, ldap_mapping_id = NULL
		WHERE workspace_id = ? AND user_id = ? AND ldap_role IS NOT NULL`, s.WsID, userID))
}

// DropManualRole quita el rol que dio una persona a quien además está por un grupo: queda con
// el del directorio.
func (r *Repo) DropManualRole(ctx context.Context, s Scope, userID string) error {
	return affected(r.db.Write.ExecContext(ctx, `UPDATE workspace_members SET manual_role = NULL, role = ldap_role
		WHERE workspace_id = ? AND user_id = ? AND ldap_role IS NOT NULL`, s.WsID, userID))
}
