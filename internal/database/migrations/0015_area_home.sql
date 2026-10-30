-- Inicio del área (SPEC §4.17): anuncios fechados y las visitas para "más vistas".

-- Un anuncio lo ven todos los miembros del área: no tiene nivel. Lo borra quien lo escribió o
-- un admin del área; no se edita ni tiene historial.
CREATE TABLE announcements (
    id TEXT PRIMARY KEY,                       -- ULID
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    body_markdown TEXT NOT NULL,
    created_by TEXT NOT NULL REFERENCES users(id),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX announcements_ws ON announcements(workspace_id, created_at);

-- Visitas por página y por día, nunca quién (SPEC §20.3): como search_misses, un contador.
-- El nivel no se copia: las consultas unen con pages, que es la fuente (§3.3). La purga diaria
-- borra lo de más de 90 días.
CREATE TABLE page_views (
    page_id TEXT NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    day DATE NOT NULL,
    views INTEGER NOT NULL DEFAULT 1,
    PRIMARY KEY (page_id, day)
);
CREATE INDEX page_views_ws_day ON page_views(workspace_id, day);
