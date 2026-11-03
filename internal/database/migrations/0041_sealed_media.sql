-- F7.6: archivos de los sellados (SPEC §6.1: "sus revisiones y media también se cifran", §13.2).
-- Tabla aparte de media_assets por la misma razón que sealed_pages (ADR-0011): el navegador de
-- archivos, la purga y doctor recorren media_assets, y ninguno puede traer uno de estos por
-- olvidar un filtro. El archivo va cifrado con la DEK en storage/ws_<área>/<id>.enc; el nombre
-- original y el tipo, también (meta_enc), porque el nombre ya es contenido. Viven lo que vive el
-- documento: la purga diaria no puede saber cuáles usa sin abrirlo, y la historia los necesita.
CREATE TABLE sealed_media (
    id TEXT PRIMARY KEY,                       -- ULID; también es el nombre en disco, con .enc
    page_id TEXT NOT NULL REFERENCES sealed_pages(id) ON DELETE CASCADE,
    meta_enc BLOB NOT NULL,                    -- {"name","mime"} cifrado con la DEK
    file_size_bytes INTEGER NOT NULL,
    uploaded_by TEXT NOT NULL REFERENCES users(id),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX sealed_media_page ON sealed_media(page_id);
