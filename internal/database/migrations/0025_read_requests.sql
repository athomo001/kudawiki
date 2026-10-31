-- F5: acuse de lectura (SPEC §4.14, §10.3).

-- Una solicitud de lectura: qué página (y desde qué revisión), a quién y hasta cuándo. Los
-- destinatarios no se copian: se calculan con audience_json contra los miembros de hoy, así quien
-- entra al área después también la recibe y quien pierde el nivel deja de estar (§17).
-- audience_json: {"all":true} | {"roles":["viewer","editor"]} | {"users":["<id>",...]}.
-- notified_at y reminded_at son del worker: el aviso de la solicitud y el recordatorio antes del
-- plazo ya se repartieron.
CREATE TABLE read_requests (
    id TEXT PRIMARY KEY,                       -- ULID
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    page_id TEXT NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    revision_number INTEGER NOT NULL,          -- la versión que hay que leer (o una posterior)
    statement TEXT,                            -- "Confirmo que leí…"; NULL = el texto de siempre
    audience_json TEXT NOT NULL,
    due_at DATETIME,
    created_by TEXT NOT NULL REFERENCES users(id),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    closed_at DATETIME,
    notified_at DATETIME,
    reminded_at DATETIME
);
CREATE INDEX read_requests_page ON read_requests(workspace_id, page_id);
CREATE INDEX read_requests_open ON read_requests(closed_at, notified_at);

-- La confirmación, con la revisión exacta que la persona tenía en pantalla.
CREATE TABLE read_acknowledgements (
    request_id TEXT NOT NULL REFERENCES read_requests(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id),
    revision_number INTEGER NOT NULL,
    acknowledged_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (request_id, user_id)
);

-- Aperturas de la página por los destinatarios, solo mientras la solicitud está abierta: no es
-- un registro de todas las lecturas (§11.1 no cambia).
CREATE TABLE read_request_views (
    request_id TEXT NOT NULL REFERENCES read_requests(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id),
    first_opened_at DATETIME NOT NULL,
    last_opened_at DATETIME NOT NULL,
    open_count INTEGER NOT NULL DEFAULT 1,
    last_revision_number INTEGER NOT NULL,
    PRIMARY KEY (request_id, user_id)
);
