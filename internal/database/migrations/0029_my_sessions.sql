-- F6.1c: mis sesiones y aviso de inicio de sesión nuevo (SPEC §8.2).
-- id es el nombre público de la sesión, para cerrarla desde "Mis sesiones": el hash del token no
-- sale del servidor. Las sesiones que ya existían reciben uno al azar.
ALTER TABLE sessions ADD COLUMN id TEXT;
UPDATE sessions SET id = lower(hex(randomblob(16)));
CREATE UNIQUE INDEX sessions_id ON sessions(id);
-- La IP y el navegador con que se inició; las de antes de esta migración quedan vacías.
ALTER TABLE sessions ADD COLUMN ip TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN user_agent TEXT NOT NULL DEFAULT '';

-- Desde qué IP y con qué navegador (familia y sistema, no la versión) entró cada persona. Una IP o
-- un navegador que no está aquí hace que salga el aviso "inicio de sesión nuevo". Las sesiones se
-- borran al vencer, así que no sirven para recordarlo.
CREATE TABLE login_history (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    ip TEXT NOT NULL,
    browser TEXT NOT NULL,
    last_at DATETIME NOT NULL,
    PRIMARY KEY (user_id, ip, browser)
);
