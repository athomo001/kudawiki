-- F8.6: un vínculo de compartir también termina porque la página se trasladó a otra área
-- (§4.13, §4.19): end_reason 'transferred'. SQLite no deja cambiar un CHECK, así que la tabla se
-- reconstruye igual que en 0035, con las mismas filas e índices. No tiene tablas hijas.
CREATE TABLE page_shares_new (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    page_id TEXT NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    target_workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK(status IN ('pending','active','rejected','ended')),
    requested_by TEXT NOT NULL REFERENCES users(id),
    reviewed_by TEXT REFERENCES users(id),
    review_comment TEXT,
    end_reason TEXT CHECK(end_reason IN ('unshared','removed','level','deleted','transferred')),
    ended_by TEXT REFERENCES users(id),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    reviewed_at DATETIME,
    ended_at DATETIME,
    CHECK (workspace_id <> target_workspace_id),
    CHECK ((status = 'ended') = (end_reason IS NOT NULL))
);
INSERT INTO page_shares_new SELECT id, workspace_id, page_id, target_workspace_id, status, requested_by, reviewed_by,
    review_comment, end_reason, ended_by, created_at, reviewed_at, ended_at FROM page_shares;
DROP TABLE page_shares;
ALTER TABLE page_shares_new RENAME TO page_shares;
CREATE UNIQUE INDEX page_shares_open ON page_shares(page_id, target_workspace_id) WHERE status IN ('pending','active');
CREATE INDEX page_shares_target ON page_shares(target_workspace_id, status);
CREATE INDEX page_shares_source ON page_shares(workspace_id, status);
