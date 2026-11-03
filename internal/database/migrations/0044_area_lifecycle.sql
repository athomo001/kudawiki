-- F8.5: ciclo de vida de un área (SPEC §3.5). archived: solo lectura para todos; deactivated:
-- nadie la ve salvo el superadmin desde el panel. delete_at: se pidió borrarla y se purga en esa
-- fecha (workspaces.delete_grace_days); mientras tanto nadie la ve y el superadmin puede
-- cancelarlo. Los espacios personales no pasan por estos estados (§7).
ALTER TABLE workspaces ADD COLUMN status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','archived','deactivated'));
ALTER TABLE workspaces ADD COLUMN delete_at DATETIME;
ALTER TABLE workspaces ADD COLUMN delete_requested_by TEXT REFERENCES users(id) ON DELETE SET NULL;

-- B17: el slug viejo de un área renombrada. Las URL y los enlaces a media que lo usan siguen
-- funcionando para quien es miembro; para el resto, el mismo 404 de siempre (§3.3). Un área que
-- hoy usa ese slug gana sobre el alias.
CREATE TABLE workspace_aliases (
    slug TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX workspace_aliases_ws ON workspace_aliases(workspace_id);
