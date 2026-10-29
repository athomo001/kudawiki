-- F3.4d: descripciones (SPEC §10). La del área la edita su admin y la ven sus miembros (§3.1).
-- La de la página es corta y va con su ícono (emoji) en listados, búsqueda e índice de hijas
-- (§4.1); son metadatos, como las etiquetas: cambiarlos no crea revisión.
ALTER TABLE workspaces ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE pages ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE pages ADD COLUMN icon TEXT NOT NULL DEFAULT '';
