-- F3: recuperación por consola (SPEC §13.6). El bloqueo por intentos fallidos vive en la memoria
-- del servidor; `kudawiki admin unlock` corre en otro proceso y deja aquí la hora: el servidor
-- olvida los intentos de esa persona anteriores a ella.
ALTER TABLE users ADD COLUMN login_unlocked_at DATETIME;
