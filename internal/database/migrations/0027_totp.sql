-- F6.1a: segundo factor TOTP (SPEC §8.1, §10). El secreto va cifrado con la MASTER_KEY y
-- totp_key_version dice con qué versión, para la rotación (§6.5), igual que site_settings.
-- Mientras totp_enabled = 0 el secreto es el de un alta sin confirmar: no se pide al entrar.
-- totp_last_step es el último paso de 30 s aceptado: RFC 6238 §5.2 prohíbe aceptar dos veces
-- el mismo código, y así un código visto por encima del hombro no sirve una segunda vez.
ALTER TABLE users ADD COLUMN totp_secret_enc BLOB;
ALTER TABLE users ADD COLUMN totp_key_version INTEGER;
ALTER TABLE users ADD COLUMN totp_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN totp_last_step INTEGER NOT NULL DEFAULT 0;

-- Diez códigos de un solo uso; solo su SHA-256 (§8.1). Tienen 80 bits: una copia de la base no
-- sirve para adivinarlos.
CREATE TABLE totp_recovery_codes (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash TEXT NOT NULL,
    used_at DATETIME,
    PRIMARY KEY (user_id, code_hash)
);
