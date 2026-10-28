-- F2.1: áreas, miembros e invitaciones. kind, approval_mode y las columnas de LDAP (SPEC §10)
-- llegan con sus fases.
CREATE TABLE workspaces (
    id TEXT PRIMARY KEY,                       -- ULID
    slug TEXT UNIQUE NOT NULL,
    name TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE workspace_members (
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK(role IN ('admin','editor','viewer')),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (workspace_id, user_id)
);
CREATE INDEX workspace_members_user ON workspace_members(user_id);

-- Solo el SHA-256 del token; el token en claro solo existe en el enlace (SPEC §8.5).
CREATE TABLE auth_tokens (
    token_hash TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK(kind IN ('password_reset','invite')),
    created_by TEXT REFERENCES users(id),
    expires_at DATETIME NOT NULL,
    used_at DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX auth_tokens_user ON auth_tokens(user_id, kind);

ALTER TABLE users ADD COLUMN invited_at DATETIME;  -- NULL = ya aceptó o no aplica
