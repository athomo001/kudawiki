-- F2.5: búsqueda (SPEC §9).

-- Idioma del contenido de la página: decide el stemmer (§9.1) y el lang del lector (§15).
ALTER TABLE pages ADD COLUMN lang TEXT NOT NULL DEFAULT 'es' CHECK(lang IN ('es','en'));

-- Una fila por página, escrita por repo en la misma transacción del guardado (§9.1).
-- Columnas *_stem: el mismo texto reducido a raíces con Snowball. workspace_id y min_rank
-- van en el índice para que toda consulta filtre por ellos (§9.2).
CREATE VIRTUAL TABLE pages_fts USING fts5(
    page_id UNINDEXED, workspace_id UNINDEXED, min_rank UNINDEXED, lang UNINDEXED,
    title, tags, headings, description, content_text, code_text,
    title_stem, headings_stem, description_stem, content_stem,
    tokenize = 'unicode61 remove_diacritics 2', prefix = '2 3'
);

-- Solo títulos, para "¿quisiste decir?" con errores de tipeo.
CREATE VIRTUAL TABLE pages_title_trgm USING fts5(
    page_id UNINDEXED, workspace_id UNINDEXED, min_rank UNINDEXED,
    title, tokenize = 'trigram'
);

-- Búsquedas sin resultados, sin el usuario (§9.6).
CREATE TABLE search_misses (
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    query_norm TEXT NOT NULL,
    day DATE NOT NULL,
    count INTEGER NOT NULL DEFAULT 1,
    PRIMARY KEY (workspace_id, query_norm, day)
);
