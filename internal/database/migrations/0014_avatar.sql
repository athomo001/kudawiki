-- Foto de perfil (SPEC §8.6.3, §10.2): PNG de 128 px que arma el servidor; nunca se guarda el
-- archivo que subió la persona. En la BD y no en storage/: pesa pocos KB y no es de ningún área.
ALTER TABLE users ADD COLUMN avatar_png BLOB;
