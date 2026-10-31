-- foreign_keys: off
-- F5: vigencia del contenido (SPEC §4.8) y las categorías de avisos de F5 (§12.1).

-- Cada página tiene dueño (por defecto quien la creó) y puede tener su propio intervalo de
-- revisión; sin él, manda el del área. verified_at es la última verificación: marcarla como
-- vigente o editarla. review_notified_at es el último aviso de vencida al dueño: si es anterior a
-- verified_at, el ciclo es nuevo y vuelve a avisar.
ALTER TABLE pages ADD COLUMN owner_id TEXT REFERENCES users(id);
ALTER TABLE pages ADD COLUMN review_interval_days INTEGER CHECK(review_interval_days > 0);
ALTER TABLE pages ADD COLUMN verified_at DATETIME;
ALTER TABLE pages ADD COLUMN verified_by TEXT REFERENCES users(id);
ALTER TABLE pages ADD COLUMN review_notified_at DATETIME;
-- Lo que ya existe queda verificado por su última edición: una edición cuenta como verificación.
UPDATE pages SET owner_id = created_by, verified_at = updated_at, verified_by = updated_by;
CREATE INDEX idx_pages_review ON pages(workspace_id, owner_id, verified_at);

-- Apagada por defecto (§19.11): NULL = el área no vence. Como las demás opciones del área, es una
-- columna de workspaces (workspace_settings no existe, §10).
ALTER TABLE workspaces ADD COLUMN default_review_days INTEGER CHECK(default_review_days > 0);

-- La plantilla puede fijar el intervalo de la página que nace de ella (§4.9).
ALTER TABLE page_templates ADD COLUMN review_interval_days INTEGER CHECK(review_interval_days > 0);

-- Las categorías de F5: vigencia, solicitudes de lectura y cuestionarios con sus recordatorios,
-- intentos extra recibidos y quien agotó los intentos sin aprobar (a quien asignó). SQLite no
-- cambia un CHECK: se reconstruyen las dos tablas, una vez para toda la fase.
CREATE TABLE notifications_new (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    category TEXT NOT NULL CHECK(category IN ('proposals','my_proposals','watched','review','reading','quiz_extra','quiz_failed')),
    page_id TEXT REFERENCES pages(id) ON DELETE CASCADE,
    proposal_id TEXT REFERENCES proposals(id) ON DELETE CASCADE,
    min_rank INTEGER NOT NULL DEFAULT 1 CHECK(min_rank IN (1,2,3)),
    payload_json TEXT NOT NULL DEFAULT '{}',
    group_key TEXT,
    count INTEGER NOT NULL DEFAULT 1,
    read_at DATETIME,
    emailed_at DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO notifications_new SELECT * FROM notifications;
DROP TABLE notifications;
ALTER TABLE notifications_new RENAME TO notifications;
CREATE UNIQUE INDEX notifications_unread_group ON notifications(user_id, group_key)
    WHERE read_at IS NULL AND group_key IS NOT NULL;
CREATE INDEX notifications_user ON notifications(user_id, updated_at);
CREATE INDEX notifications_email ON notifications(created_at) WHERE emailed_at IS NULL AND read_at IS NULL;

CREATE TABLE notification_prefs_new (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    category TEXT NOT NULL CHECK(category IN ('proposals','my_proposals','watched','review','reading','quiz_extra','quiz_failed')),
    email TEXT NOT NULL CHECK(email IN ('immediate','daily','off')),
    PRIMARY KEY (user_id, category)
);
INSERT INTO notification_prefs_new SELECT * FROM notification_prefs;
DROP TABLE notification_prefs;
ALTER TABLE notification_prefs_new RENAME TO notification_prefs;
