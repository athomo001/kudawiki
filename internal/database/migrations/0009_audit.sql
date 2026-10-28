-- F3.3: auditoría (SPEC §11).
-- Solo se agrega al final. Cada fila sella la anterior: hash = SHA-256(prev_hash || fila), así
-- cambiar, borrar o intercalar una fila rompe la cadena y `kudawiki audit verify` lo detecta.
CREATE TABLE audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    -- Texto con ancho fijo (2026-10-28T12:00:00.000000Z): ordena bien como texto y el hash se
    -- calcula sobre los mismos bytes que se guardan.
    at TEXT NOT NULL,
    actor_id TEXT,                              -- NULL = lo hizo el sistema (meta.system, §11.2)
    workspace_id TEXT,                          -- NULL = evento global
    action TEXT NOT NULL,                       -- lista cerrada, internal/audit
    target_type TEXT,
    target_id TEXT,
    meta_json TEXT NOT NULL DEFAULT '{}',       -- identidad, nunca contenido (§11.2)
    -- Email, nombre e IP tal como estaban. Fuera del hash para poder seudonimizarla (§13.10).
    actor_snapshot_json TEXT NOT NULL DEFAULT '{}',
    prev_hash TEXT NOT NULL,
    hash TEXT NOT NULL
);
-- Sin claves foráneas a propósito: un evento sobrevive a la purga de lo que registra.
CREATE INDEX audit_ws ON audit_log(workspace_id, id);
CREATE INDEX audit_actor ON audit_log(actor_id, id);

-- La copia del actor es lo único que se puede reescribir (seudonimizar), y no está en el hash.
CREATE TRIGGER audit_no_update BEFORE UPDATE OF id, at, actor_id, workspace_id, action, target_type, target_id, meta_json, prev_hash, hash ON audit_log
BEGIN SELECT RAISE(ABORT, 'audit_log is append-only'); END;
-- Piso de retención (§11.2): nada de los últimos 180 días se puede borrar, ni por SQL directo.
CREATE TRIGGER audit_retention_floor BEFORE DELETE ON audit_log
WHEN old.at >= strftime('%Y-%m-%dT%H:%M:%S', 'now', '-180 days')
BEGIN SELECT RAISE(ABORT, 'audit_log keeps at least 180 days'); END;
