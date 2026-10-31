-- F4: autoguardado del editor (SPEC §4.11).

-- Un borrador por persona y página. El de una página que ya existe va por page_id, así sigue a
-- la página si alguien la mueve; el de una página nueva, por la ruta donde se va a crear. Son
-- contenido sin publicar de una sola persona: no se indexan ni se ven en ninguna otra parte, y
-- se borran al publicar, al descartar, al irse la persona o a los 30 días (la purga diaria).
-- Los de sellados (cifrados con la DEK) llegan con los sellados en F7.
CREATE TABLE page_drafts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    page_id TEXT REFERENCES pages(id) ON DELETE CASCADE,  -- NULL = página nueva
    target_slug TEXT NOT NULL,                            -- de una página nueva; en otra, la ruta que tenía al guardar
    base_revision INTEGER NOT NULL DEFAULT 0,             -- la revisión sobre la que se escribía; 0 en una nueva
    title TEXT NOT NULL DEFAULT '',
    content_markdown TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX page_drafts_page ON page_drafts(user_id, page_id) WHERE page_id IS NOT NULL;
CREATE UNIQUE INDEX page_drafts_new ON page_drafts(user_id, workspace_id, target_slug) WHERE page_id IS NULL;
CREATE INDEX page_drafts_age ON page_drafts(updated_at);
