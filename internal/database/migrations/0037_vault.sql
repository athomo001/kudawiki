-- F7.2: la bóveda de cada persona (SPEC §6.3, §10). La pública envuelve la DEK de los sellados
-- que le comparten; la privada se guarda cifrada con Argon2id(clave de bóveda, vault_salt) y su
-- id como aad. Sin bóveda (NULL) no se le puede dar acceso a un sellado.
ALTER TABLE users ADD COLUMN vault_pubkey BLOB;
ALTER TABLE users ADD COLUMN vault_privkey_enc BLOB;
ALTER TABLE users ADD COLUMN vault_salt BLOB;
