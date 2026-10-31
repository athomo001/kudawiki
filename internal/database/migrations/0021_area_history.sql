-- F4: historial por área (SPEC §4.3). Como approval_mode, son columnas de workspaces y no filas
-- de workspace_settings, que todavía no existe (§10).

-- El área exige un resumen al cambiar una página que existe.
ALTER TABLE workspaces ADD COLUMN require_change_summary INTEGER NOT NULL DEFAULT 0 CHECK(require_change_summary IN (0,1));
-- Retención del historial: NULL = todas. Una de las dos, nunca las dos; la purga diaria borra
-- lo demás salvo la revisión actual y la base de una propuesta abierta.
ALTER TABLE workspaces ADD COLUMN revisions_keep_last INTEGER CHECK(revisions_keep_last > 0);
ALTER TABLE workspaces ADD COLUMN revisions_keep_days INTEGER CHECK(revisions_keep_days > 0);
