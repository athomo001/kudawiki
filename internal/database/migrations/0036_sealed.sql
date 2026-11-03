-- F7: documentos sellados (SPEC §6, ADR-0004, ADR-0011).

-- Un sellado vive en sus propias tablas, no en pages: así ninguna consulta de páginas (árbol,
-- búsqueda, backlinks, conteos, vigencia, lecturas) lo puede alcanzar por olvidar un filtro
-- (ADR-0011). En claro solo quedan el área, la ruta, el autor y las fechas: el título y el
-- contenido van cifrados con la DEK del documento en cada revisión.
CREATE TABLE sealed_pages (
    id TEXT PRIMARY KEY,                       -- ULID
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    slug TEXT NOT NULL,
    author_id TEXT NOT NULL REFERENCES users(id),
    current_revision INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_by TEXT NOT NULL REFERENCES users(id),
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX sealed_pages_slug ON sealed_pages(workspace_id, slug);
CREATE INDEX sealed_pages_author ON sealed_pages(author_id);

-- payload: XChaCha20-Poly1305 con la DEK del JSON {title, markdown}, con aad = id:revisión, así
-- una revisión no se puede hacer pasar por otra ni por la de otro documento (§6.3).
CREATE TABLE sealed_revisions (
    page_id TEXT NOT NULL REFERENCES sealed_pages(id) ON DELETE CASCADE,
    revision_number INTEGER NOT NULL,
    payload BLOB NOT NULL,
    created_by TEXT NOT NULL REFERENCES users(id),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (page_id, revision_number)
);

-- La DEK solo se guarda envuelta: con la contraseña del documento (password, Argon2id con
-- kdf_salt), con la bóveda de cada persona con acceso (user) y con la MASTER_KEY (master, con su
-- key_version para la rotación de §6.5). Borrarlas todas deja ilegible cualquier resto (§6.1).
CREATE TABLE sealed_keys (
    page_id TEXT NOT NULL REFERENCES sealed_pages(id) ON DELETE CASCADE,
    holder_type TEXT NOT NULL CHECK(holder_type IN ('password','user','master')),
    holder_user_id TEXT REFERENCES users(id) ON DELETE CASCADE,
    permission TEXT NOT NULL DEFAULT 'read' CHECK(permission IN ('read','edit')),
    kdf_salt BLOB,
    wrapped_dek BLOB NOT NULL,
    key_version INTEGER NOT NULL DEFAULT 1,
    granted_by TEXT REFERENCES users(id),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK ((holder_type = 'user') = (holder_user_id IS NOT NULL)),
    CHECK ((holder_type = 'password') = (kdf_salt IS NOT NULL))
);
CREATE UNIQUE INDEX sealed_keys_holder ON sealed_keys(page_id, holder_type, IFNULL(holder_user_id, ''));

-- La ruta de un sellado queda reservada (§6.1): ninguna página viva la puede tomar, ni al crearse,
-- ni al moverse, ni al restaurarse; y un sellado no toma la de una página viva. Va en la base y no
-- en cada camino del código, que son varios (guardar, mover, publicar, restaurar, aprobar). El
-- mensaje es el mismo para todos y repo lo trata como el choque de siempre: no dice qué ocupa la ruta.
CREATE TRIGGER pages_slug_reserved_insert BEFORE INSERT ON pages
WHEN NEW.deleted_at IS NULL AND EXISTS (SELECT 1 FROM sealed_pages s WHERE s.workspace_id = NEW.workspace_id AND s.slug = NEW.slug)
BEGIN SELECT RAISE(ABORT, 'slug_reserved'); END;

CREATE TRIGGER pages_slug_reserved_update BEFORE UPDATE OF slug, workspace_id, deleted_at ON pages
WHEN NEW.deleted_at IS NULL AND EXISTS (SELECT 1 FROM sealed_pages s WHERE s.workspace_id = NEW.workspace_id AND s.slug = NEW.slug)
BEGIN SELECT RAISE(ABORT, 'slug_reserved'); END;

CREATE TRIGGER sealed_slug_reserved_insert BEFORE INSERT ON sealed_pages
WHEN EXISTS (SELECT 1 FROM pages p WHERE p.workspace_id = NEW.workspace_id AND p.slug = NEW.slug AND p.deleted_at IS NULL)
BEGIN SELECT RAISE(ABORT, 'slug_reserved'); END;
