-- F8.11: orden manual por carpeta del árbol (SPEC §15, B7). NULL es el orden de siempre, por
-- ruta; las páginas con posición van primero, de menor a mayor, entre sus hermanas.
ALTER TABLE pages ADD COLUMN position INTEGER;
