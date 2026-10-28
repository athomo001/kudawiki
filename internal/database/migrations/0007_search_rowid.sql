-- Rendimiento de la búsqueda (SPEC §9.5), medido con 50 000 páginas:
-- 1. Cada página ocupa siempre la misma fila en los índices (pages.search_rowid). Buscar por
--    una columna UNINDEXED (page_id) recorría el índice entero en cada guardado.
-- 2. El área y el nivel ya no se copian al índice: filtrar por ellos obligaba a FTS5 a cargar
--    la fila entera de cada coincidencia (p95 de 69 ms → 8 ms sin eso). Se filtra uniendo con
--    pages, que es la fuente del dato: además, el nivel no tiene que sincronizarse en dos lugares.
ALTER TABLE pages ADD COLUMN search_rowid INTEGER;
CREATE UNIQUE INDEX pages_search_rowid ON pages(search_rowid);

DROP TABLE pages_fts;
DROP TABLE pages_title_trgm;

-- Columnas *_stem: el mismo texto reducido a raíces con Snowball (§9.1).
CREATE VIRTUAL TABLE pages_fts USING fts5(
    title, tags, headings, description, content_text, code_text,
    title_stem, headings_stem, description_stem, content_stem,
    tokenize = 'unicode61 remove_diacritics 2', prefix = '2 3'
);

-- Solo títulos, para "¿quisiste decir?" con errores de tipeo.
CREATE VIRTUAL TABLE pages_title_trgm USING fts5(title, tokenize = 'trigram');

-- Los índices quedan vacíos: el servidor los reconstruye al arrancar (IndexStale).
