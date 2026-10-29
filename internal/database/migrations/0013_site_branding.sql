-- F3: marca de la empresa (SPEC §15.1, §10.2). Lo que viene de kudawiki.yaml o de KUDAWIKI_*
-- gana sobre estas filas. Los secretos de LDAP y SMTP (secret_enc, key_version) llegan con F4 y
-- F6, que reconstruyen la tabla.
CREATE TABLE site_settings (
    key TEXT PRIMARY KEY,                      -- branding.title, branding.color_primary…
    value_json TEXT NOT NULL,
    updated_by TEXT REFERENCES users(id),
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Logo y favicon de la empresa. En la BD y no en storage/: son pocos KB, entran en los
-- respaldos y no pertenecen a ningún área. Las variantes del correo (F4) y de la marca de agua
-- (F7) se suman con su función.
CREATE TABLE site_assets (
    kind TEXT NOT NULL CHECK(kind IN ('logo','logo_dark','favicon')),
    variant TEXT NOT NULL,                     -- original, ico, png_32, png_180, png_512
    mime_type TEXT NOT NULL,
    data BLOB NOT NULL,
    sha256 TEXT NOT NULL,                      -- también es el ETag
    uploaded_by TEXT NOT NULL REFERENCES users(id),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (kind, variant)
);
