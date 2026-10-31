-- F5: cuestionarios (SPEC §4.16, §10.3).

-- Un cuestionario es de una página y vive en su área y nivel: no es una página, no está en el
-- árbol ni en la búsqueda. draft_json es lo que se está editando ({"settings":…,"questions":[…]});
-- lo que responde la gente es siempre una versión publicada (quiz_versions).
CREATE TABLE quizzes (
    id TEXT PRIMARY KEY,                       -- ULID
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    page_id TEXT NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'draft' CHECK(status IN ('draft','published','archived')),
    published_version INTEGER,                 -- NULL mientras nunca se publicó
    draft_json TEXT NOT NULL,
    open_to_readers INTEGER NOT NULL DEFAULT 0, -- 1 = lo puede hacer cualquiera que lea la página
    created_by TEXT NOT NULL REFERENCES users(id),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX quizzes_page ON quizzes(workspace_id, page_id);

-- La solicitud de lectura que lleva cuestionario se cumple al aprobarlo (§4.14).
ALTER TABLE read_requests ADD COLUMN quiz_id TEXT REFERENCES quizzes(id);

-- Cada publicación congela preguntas y configuración; los intentos apuntan a una versión. La
-- versión 0 es la foto del borrador para probarlo (§4.16.6): nunca la responde nadie más.
-- questions_json lleva las respuestas correctas y no sale nunca hacia el navegador (§4.16.7).
CREATE TABLE quiz_versions (
    quiz_id TEXT NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    page_revision_number INTEGER NOT NULL,     -- la revisión de la página al publicar
    settings_json TEXT NOT NULL,
    questions_json TEXT NOT NULL,
    published_by TEXT NOT NULL REFERENCES users(id),
    published_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (quiz_id, version)
);

-- Un intento. request_id es la solicitud por la que se hace (NULL = cuestionario abierto): los
-- intentos, la espera y los intentos extra se cuentan por solicitud, así una solicitud nueva
-- (la página cambió, §4.16.6) vuelve a dar todos los intentos.
-- layout_json: [{"q":"<id>","o":["<id>",…]}], las preguntas y alternativas en el orden que vio.
-- answers_json: {"<pregunta>":["<alternativa>",…]}, se va guardando mientras responde.
CREATE TABLE quiz_attempts (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    quiz_id TEXT NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    user_id TEXT NOT NULL REFERENCES users(id),
    request_id TEXT REFERENCES read_requests(id) ON DELETE SET NULL,
    attempt_number INTEGER NOT NULL,
    is_preview INTEGER NOT NULL DEFAULT 0,     -- prueba de quien lo administra; no cuenta
    layout_json TEXT NOT NULL,
    answers_json TEXT NOT NULL DEFAULT '{}',
    status TEXT NOT NULL DEFAULT 'in_progress' CHECK(status IN ('in_progress','submitted','timed_out')),
    started_at DATETIME NOT NULL,
    deadline_at DATETIME,                      -- started_at + tiempo límite; NULL = sin límite
    submitted_at DATETIME,
    score_points REAL,
    max_points REAL,
    score_percent REAL,
    passed INTEGER,
    FOREIGN KEY (quiz_id, version) REFERENCES quiz_versions(quiz_id, version)
);
CREATE INDEX quiz_attempts_user ON quiz_attempts(quiz_id, user_id, started_at);
-- Un solo intento abierto por persona y cuestionario: también frena dos "empezar" simultáneos.
CREATE UNIQUE INDEX quiz_attempts_open ON quiz_attempts(quiz_id, user_id) WHERE status = 'in_progress';

CREATE TABLE quiz_extra_attempts (
    quiz_id TEXT NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id),
    request_id TEXT REFERENCES read_requests(id) ON DELETE CASCADE,
    extra INTEGER NOT NULL CHECK(extra > 0),
    reason TEXT NOT NULL,
    granted_by TEXT NOT NULL REFERENCES users(id),
    granted_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX quiz_extra_attempts_user ON quiz_extra_attempts(quiz_id, user_id);

-- El área puede apagar los cuestionarios (§4.16.7).
ALTER TABLE workspaces ADD COLUMN quizzes_enabled INTEGER NOT NULL DEFAULT 1;
