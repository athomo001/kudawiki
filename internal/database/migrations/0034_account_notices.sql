-- foreign_keys: off
-- F6: la categoría de avisos de la cuenta (§12.1), la de los que no se pueden apagar: por ahora,
-- el cambio de rol en un área. notification_prefs no cambia: esta categoría no tiene preferencia.
-- SQLite no cambia un CHECK: se reconstruye la tabla, como en 0023.
CREATE TABLE notifications_new (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    category TEXT NOT NULL CHECK(category IN ('proposals','my_proposals','watched','review','reading','quiz_extra','quiz_failed','account')),
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
