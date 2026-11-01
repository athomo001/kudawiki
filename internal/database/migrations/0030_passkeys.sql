-- F6.1d: passkeys (SPEC §8.1). Solo la parte pública de la credencial: la clave privada nunca
-- sale del autenticador.
-- backup_eligible no cambia en la vida de una credencial y WebAuthn exige comprobarlo en cada uso;
-- backup_state dice si está sincronizada entre equipos (iCloud, Google) y se actualiza al usarla.
CREATE TABLE passkeys (
    id TEXT PRIMARY KEY,                     -- credential ID (base64url)
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    public_key BLOB NOT NULL,                -- COSE
    sign_count INTEGER NOT NULL DEFAULT 0,
    backup_eligible INTEGER NOT NULL DEFAULT 0,
    backup_state INTEGER NOT NULL DEFAULT 0,
    rp_id TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at DATETIME
);
CREATE INDEX passkeys_user ON passkeys(user_id);
