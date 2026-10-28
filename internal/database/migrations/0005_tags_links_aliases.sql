-- F2.4: etiquetas, enlaces internos y alias.

-- page_revisions se reconstruye con todos los tipos de SPEC §4.3: SQLite no permite cambiar un
-- CHECK, y dejar fuera valores que el SPEC ya define obligaría a reconstruirla otra vez.
CREATE TABLE page_revisions_new (
    id TEXT PRIMARY KEY,
    page_id TEXT NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    revision_number INTEGER NOT NULL,
    kind TEXT NOT NULL CHECK(kind IN ('created','updated','moved','deleted','restored','reverted','level_changed')),
    title TEXT NOT NULL,
    content_markdown TEXT NOT NULL,
    created_by TEXT NOT NULL REFERENCES users(id),
    change_summary TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (page_id, revision_number)
);
INSERT INTO page_revisions_new SELECT * FROM page_revisions;
DROP TABLE page_revisions;
ALTER TABLE page_revisions_new RENAME TO page_revisions;

-- Una etiqueta existe porque una página la lleva: no hay catálogo aparte (SPEC §4.6).
CREATE TABLE page_tags (
    page_id TEXT NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL,
    tag TEXT NOT NULL COLLATE NOCASE,
    PRIMARY KEY (page_id, tag)
);
CREATE INDEX page_tags_ws ON page_tags(workspace_id, tag);

-- Derivados del contenido en cada guardado; se guarda la ruta para detectar enlaces rotos (SPEC §4.7).
CREATE TABLE page_links (
    workspace_id TEXT NOT NULL,
    from_page_id TEXT NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    to_slug TEXT NOT NULL,
    to_page_id TEXT REFERENCES pages(id) ON DELETE SET NULL,
    PRIMARY KEY (from_page_id, to_slug)
);
CREATE INDEX page_links_to ON page_links(workspace_id, to_slug);

-- Redirecciones tras mover o renombrar (SPEC §4.1).
CREATE TABLE page_aliases (
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    old_slug TEXT NOT NULL,
    page_id TEXT NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    PRIMARY KEY (workspace_id, old_slug)
);
