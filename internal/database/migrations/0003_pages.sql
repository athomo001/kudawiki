-- F2.2: páginas y su historial. Las columnas de sellados (F7), borrado suave (F3) y texto
-- para la búsqueda (F2.5) de SPEC §10 llegan con su función.
CREATE TABLE pages (
    id TEXT PRIMARY KEY,                       -- ULID
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    slug TEXT NOT NULL,
    min_rank INTEGER NOT NULL DEFAULT 1 CHECK(min_rank IN (1,2,3)),  -- 1 viewer, 2 editor, 3 admin
    title TEXT NOT NULL,
    content_markdown TEXT NOT NULL,
    current_revision INTEGER NOT NULL DEFAULT 1,
    created_by TEXT NOT NULL REFERENCES users(id),
    updated_by TEXT NOT NULL REFERENCES users(id),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (workspace_id, slug)
);
-- Todas las consultas de contenido filtran por área y nivel (SPEC §3.3).
CREATE INDEX pages_ws_rank ON pages(workspace_id, min_rank);

-- Inmutable: revertir crea una fila nueva, nunca reescribe la historia (SPEC §4.3).
CREATE TABLE page_revisions (
    id TEXT PRIMARY KEY,
    page_id TEXT NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    revision_number INTEGER NOT NULL,
    kind TEXT NOT NULL CHECK(kind IN ('created','updated','reverted')),  -- los demás tipos de §4.3 llegan con su función
    title TEXT NOT NULL,
    content_markdown TEXT NOT NULL,
    created_by TEXT NOT NULL REFERENCES users(id),
    change_summary TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (page_id, revision_number)
);
