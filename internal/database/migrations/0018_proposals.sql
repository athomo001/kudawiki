-- F4: propuestas con aprobación (SPEC §5).

-- Modo del área y si los lectores pueden proponer (§5.1, §19.5). Columnas con CHECK, como la
-- descripción: la base rechaza un valor mal escrito sin tener que validar JSON de una tabla
-- clave/valor (la workspace_settings de §10.1 llega si algún día hacen falta muchas más).
ALTER TABLE workspaces ADD COLUMN approval_mode TEXT NOT NULL DEFAULT 'direct' CHECK(approval_mode IN ('direct','required'));
ALTER TABLE workspaces ADD COLUMN viewers_can_propose INTEGER NOT NULL DEFAULT 0 CHECK(viewers_can_propose IN (0,1));

-- Excepciones por subárbol (§5.2.1): la ruta va sin barra final y vale para ella y lo que
-- cuelga de ella; gana la más larga.
CREATE TABLE approval_overrides (
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    slug_prefix TEXT NOT NULL,
    approval_mode TEXT NOT NULL CHECK(approval_mode IN ('direct','required')),
    PRIMARY KEY (workspace_id, slug_prefix)
);

-- Una propuesta es un cambio a una página (page_id) o una página nueva (page_id NULL hasta que
-- se aprueba). Nunca se indexa para la búsqueda: lo que no está publicado no se encuentra.
-- needs_rebase de §10 no se guarda: se calcula al leerla intentando el merge contra la versión
-- actual, así nunca queda desactualizado cuando la página cambia por otro camino.
CREATE TABLE proposals (
    id TEXT PRIMARY KEY,                       -- ULID
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    page_id TEXT REFERENCES pages(id) ON DELETE CASCADE,
    target_slug TEXT NOT NULL,
    base_revision INTEGER,                     -- NULL en una página nueva
    -- Nivel de una página nueva: el de su padre más alto al proponerla. En un cambio manda el de
    -- la página, que puede subir después (§3.2).
    min_rank INTEGER NOT NULL DEFAULT 1 CHECK(min_rank IN (1,2,3)),
    title TEXT NOT NULL,
    content_markdown TEXT NOT NULL,
    lang TEXT NOT NULL DEFAULT 'es' CHECK(lang IN ('es','en')),
    change_summary TEXT,
    status TEXT NOT NULL CHECK(status IN ('pending','merged','rejected','withdrawn')),
    author_id TEXT NOT NULL REFERENCES users(id),
    reviewer_id TEXT REFERENCES users(id),
    review_comment TEXT,
    merged_revision INTEGER,                   -- la revisión que publicó al aprobarse
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    reviewed_at DATETIME,
    CHECK ((page_id IS NULL) = (base_revision IS NULL) OR status = 'merged')
);
CREATE INDEX proposals_ws_status ON proposals(workspace_id, status);
CREATE INDEX proposals_author ON proposals(workspace_id, author_id);

-- La revisión que sale de una propuesta guarda a su autor (created_by) y a quien la aprobó (§5.2).
ALTER TABLE page_revisions ADD COLUMN approved_by TEXT REFERENCES users(id);
ALTER TABLE page_revisions ADD COLUMN proposal_id TEXT REFERENCES proposals(id);
