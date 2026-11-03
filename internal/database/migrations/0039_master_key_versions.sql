-- F7.4: versiones de la MASTER_KEY (SPEC §6.5, §10).

-- Una fila por llave que tuvo la instalación. key_check es un HMAC de una constante con la llave:
-- dice si una llave es de esta base y de qué versión, sin poder descifrar nada. El servidor no
-- arranca con una llave que no está aquí, ni con una retirada por una rotación. La primera fila
-- (versión 1) la pone el servidor al arrancar: la migración no conoce la llave.
CREATE TABLE master_key_versions (
    version INTEGER PRIMARY KEY,
    key_check BLOB NOT NULL UNIQUE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    retired_at DATETIME
);
