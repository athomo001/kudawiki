-- F5: plantillas (SPEC §4.9). No son páginas: en una tabla aparte no entran al árbol, la búsqueda,
-- las etiquetas, los backlinks, las estadísticas ni la salud, sin filtrarlas en cada consulta (§17).
-- workspace_id NULL es una plantilla global del superadmin: se copia a cada área nueva y desde ahí
-- es del área. min_rank es a la vez quién la ve y el nivel con que nace la página.
CREATE TABLE page_templates (
    id TEXT PRIMARY KEY,                       -- ULID
    workspace_id TEXT REFERENCES workspaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    content_markdown TEXT NOT NULL DEFAULT '',
    min_rank INTEGER NOT NULL DEFAULT 1 CHECK(min_rank BETWEEN 1 AND 3),
    tags_json TEXT NOT NULL DEFAULT '[]',
    created_by TEXT NOT NULL REFERENCES users(id),
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX page_templates_ws ON page_templates(workspace_id, name);
