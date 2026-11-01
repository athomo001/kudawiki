-- F6: baja de usuarios (SPEC §8.4) y seudonimización de la auditoría (§13.10, B14).

-- La baja empieza desactivando la cuenta y termina, al vencer el período de gracia, con la
-- transferencia y el borrado. Mientras offboarding_at no es NULL hay una baja en curso:
-- reactivar la cuenta la anula sin perder nada.
ALTER TABLE users ADD COLUMN offboarding_at DATETIME;          -- fin del período de gracia
ALTER TABLE users ADD COLUMN offboarding_receiver_json TEXT;   -- {workspace_id: admin_user_id}
-- Quien la inició: recibe lo de un área que se quedó sin admin durante la gracia.
ALTER TABLE users ADD COLUMN offboarding_by TEXT REFERENCES users(id);
-- Cuándo se completó: desde ahí corre audit.pseudonymize_after_days (§19.17).
ALTER TABLE users ADD COLUMN offboarded_at DATETIME;
