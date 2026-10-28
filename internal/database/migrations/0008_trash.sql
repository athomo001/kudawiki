-- foreign_keys: off
-- F3.2: papelera (SPEC §4.5).

-- pages se reconstruye: la ruta tiene que ser única solo entre las páginas vivas, para que una
-- borrada no deje su ruta ocupada durante los días que pasa en la papelera. SQLite no permite
-- quitar un UNIQUE de tabla, y reconstruirla obliga a apagar las claves foráneas (las hijas
-- referencian pages y DROP TABLE dispararía sus ON DELETE CASCADE).
CREATE TABLE pages_new (
    id TEXT PRIMARY KEY,                       -- ULID
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    slug TEXT NOT NULL,
    min_rank INTEGER NOT NULL DEFAULT 1 CHECK(min_rank IN (1,2,3)),  -- 1 viewer, 2 editor, 3 admin
    title TEXT NOT NULL,
    content_markdown TEXT NOT NULL,
    lang TEXT NOT NULL DEFAULT 'es' CHECK(lang IN ('es','en')),
    current_revision INTEGER NOT NULL DEFAULT 1,
    search_rowid INTEGER,
    created_by TEXT NOT NULL REFERENCES users(id),
    updated_by TEXT NOT NULL REFERENCES users(id),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME,                       -- NULL = viva
    deleted_by TEXT REFERENCES users(id),
    -- Borrar una página manda su subárbol visible como una unidad (§4.5): todas llevan el id de
    -- la que se borró, y se restauran juntas.
    trash_root_id TEXT
);
INSERT INTO pages_new (id, workspace_id, slug, min_rank, title, content_markdown, lang, current_revision,
    search_rowid, created_by, updated_by, created_at, updated_at)
SELECT id, workspace_id, slug, min_rank, title, content_markdown, lang, current_revision,
    search_rowid, created_by, updated_by, created_at, updated_at FROM pages;
DROP TABLE pages;
ALTER TABLE pages_new RENAME TO pages;

CREATE UNIQUE INDEX pages_ws_slug ON pages(workspace_id, slug) WHERE deleted_at IS NULL;
-- Todas las consultas de contenido filtran por área, papelera y nivel (SPEC §3.3).
CREATE INDEX pages_ws_rank ON pages(workspace_id, deleted_at, min_rank);
CREATE UNIQUE INDEX pages_search_rowid ON pages(search_rowid);
CREATE INDEX pages_trash ON pages(deleted_at) WHERE deleted_at IS NOT NULL;

-- La media sin página va a la papelera a los 7 días (§4.4); la de una página sigue a su página.
ALTER TABLE media_assets ADD COLUMN deleted_at DATETIME;
