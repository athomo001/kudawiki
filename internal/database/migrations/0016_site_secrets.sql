-- F4: el primer secreto de configuración, la contraseña SMTP (SPEC §10.2, §12). Se reconstruye
-- site_settings como la previó §10.2: un valor es JSON o un secreto cifrado con la MASTER_KEY,
-- nunca los dos. key_version es la versión de la llave que lo cifró, para la rotación (§6.5).
CREATE TABLE site_settings_new (
    key TEXT PRIMARY KEY,
    value_json TEXT,                           -- NULL si es un secreto
    secret_enc BLOB,                           -- smtp.password (y el bind de LDAP en F6)
    key_version INTEGER,
    updated_by TEXT REFERENCES users(id),
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK ((value_json IS NULL) <> (secret_enc IS NULL)),
    CHECK ((secret_enc IS NULL) = (key_version IS NULL))
);
INSERT INTO site_settings_new (key, value_json, updated_by, updated_at)
    SELECT key, value_json, updated_by, updated_at FROM site_settings;
DROP TABLE site_settings;
ALTER TABLE site_settings_new RENAME TO site_settings;
