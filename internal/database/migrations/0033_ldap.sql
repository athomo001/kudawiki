-- F6.2b: cuentas del directorio y reglas de mapeo de grupos a áreas (SPEC §8.3, §8.6).

-- Una cuenta del directorio se identifica por su identidad estable (objectGUID en AD), no por el
-- correo: si cambia el correo en AD, la cuenta sigue siendo la misma (§8.3.5). No tiene
-- contraseña local.
ALTER TABLE users ADD COLUMN auth_source TEXT NOT NULL DEFAULT 'local' CHECK(auth_source IN ('local','ldap'));
ALTER TABLE users ADD COLUMN ldap_dn TEXT;
ALTER TABLE users ADD COLUMN ldap_guid TEXT;
CREATE UNIQUE INDEX users_ldap_guid ON users(ldap_guid) WHERE ldap_guid IS NOT NULL;

-- Grupo del directorio → (área, rol). El grupo se reconoce por su identidad (objectGUID en AD;
-- el atributo de identidad o el DN en los demás), no por el DN, que cambia al moverlo (§8.3.1).
-- group_sid solo existe en AD: con él se cruzan los tokenGroups de quien entra.
CREATE TABLE ldap_group_mappings (
    id TEXT PRIMARY KEY,                        -- ULID
    group_guid TEXT NOT NULL,
    group_sid TEXT,
    group_dn TEXT NOT NULL,                     -- solo para mostrar
    group_name TEXT NOT NULL,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK(role IN ('admin','editor','viewer')),
    created_by TEXT NOT NULL REFERENCES users(id),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (group_guid, workspace_id)
);
CREATE INDEX ldap_group_mappings_sid ON ldap_group_mappings(group_sid) WHERE group_sid IS NOT NULL;

-- De dónde viene el rol de cada miembro (§8.3.2). role sigue siendo el rol efectivo, el único que
-- leen las consultas de acceso: max(manual_role, ldap_role). manual_role es el que dio una
-- persona (NULL si solo viene de un grupo) y ldap_role el que da el directorio (NULL si ninguno).
-- Así la contención no depende de calcular un máximo en cada consulta.
ALTER TABLE workspace_members ADD COLUMN manual_role TEXT CHECK(manual_role IN ('admin','editor','viewer'));
ALTER TABLE workspace_members ADD COLUMN ldap_role TEXT CHECK(ldap_role IN ('admin','editor','viewer'));
ALTER TABLE workspace_members ADD COLUMN ldap_mapping_id TEXT REFERENCES ldap_group_mappings(id) ON DELETE SET NULL;
UPDATE workspace_members SET manual_role = role;
