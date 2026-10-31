-- F4: cola persistente de correos (SPEC §12). Un correo se encola en la acción que lo origina y
-- lo entrega el worker, con reintentos: un servidor SMTP caído no hace fallar la acción.
-- El cuerpo (destinatario, plantilla, datos y enlace con token) va cifrado con la MASTER_KEY: un
-- respaldo de wiki.db, que no lleva la llave, no trae enlaces de recuperación usables (§6). Al
-- entregarse, o al rendirse, se borra; la fila queda para la purga.
CREATE TABLE email_outbox (
    id TEXT PRIMARY KEY,                       -- ULID; también es el dato asociado del cifrado
    template TEXT NOT NULL,                    -- invite, password_reset… (sin datos personales)
    payload_enc BLOB,                          -- NULL una vez entregado o rendido
    key_version INTEGER,
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at DATETIME NOT NULL,
    last_error TEXT,                           -- lo que contestó el servidor, para el log y la purga
    sent_at DATETIME,
    failed_at DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK ((payload_enc IS NULL) = (sent_at IS NOT NULL OR failed_at IS NOT NULL))
);
CREATE INDEX email_outbox_due ON email_outbox(next_attempt_at) WHERE payload_enc IS NOT NULL;
