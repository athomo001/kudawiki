-- F1: solo identidad local y sesiones. Las columnas de TOTP, LDAP y bóveda (SPEC §10)
-- llegan con su fase, en migraciones nuevas.
CREATE TABLE users (
    id TEXT PRIMARY KEY,                       -- ULID
    email TEXT UNIQUE NOT NULL COLLATE NOCASE,
    full_name TEXT NOT NULL,
    password_hash TEXT,                        -- NULL para usuarios LDAP (F6)
    is_global_admin INTEGER NOT NULL DEFAULT 0,
    is_active INTEGER NOT NULL DEFAULT 1,
    locale TEXT NOT NULL DEFAULT 'es' CHECK(locale IN ('es','en')),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Solo se guarda el SHA-256 del token: una copia de la BD no sirve para entrar (SPEC §8.2).
CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at DATETIME NOT NULL,
    last_seen_at DATETIME NOT NULL
);
CREATE INDEX sessions_user_id ON sessions(user_id);
