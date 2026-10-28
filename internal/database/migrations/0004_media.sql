-- F2.3: media. El cifrado de media de sellados (F7) y el borrado suave (F3) de SPEC §10
-- llegan con su función.
CREATE TABLE media_assets (
    id TEXT PRIMARY KEY,                       -- ULID; también es el nombre en disco
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    page_id TEXT REFERENCES pages(id) ON DELETE SET NULL,  -- NULL hasta que una página la usa
    original_filename TEXT NOT NULL,
    mime_type TEXT NOT NULL,                   -- detectado por el contenido, no por la extensión
    file_size_bytes INTEGER NOT NULL,
    sha256 TEXT NOT NULL,
    has_thumbnail INTEGER NOT NULL DEFAULT 0,
    uploaded_by TEXT NOT NULL REFERENCES users(id),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX media_ws_page ON media_assets(workspace_id, page_id);
