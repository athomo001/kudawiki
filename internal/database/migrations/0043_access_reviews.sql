-- F8.4: revisión periódica de accesos (SPEC §21.2.7). Una fila por confirmación: quién, cuándo
-- y la lista que tenía a la vista al confirmar. workspace_id NULL es la revisión del superadmin
-- (admins de área y superadmins). La evidencia para el auditor es access_review.confirm en la
-- auditoría encadenada; esta tabla da el plazo y lo que se confirmó.
CREATE TABLE access_reviews (
    id TEXT PRIMARY KEY,                        -- ULID
    workspace_id TEXT REFERENCES workspaces(id) ON DELETE CASCADE,
    reviewer_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    reviewer_name TEXT NOT NULL,                -- copia: el nombre sigue aunque la cuenta se borre
    snapshot_json TEXT NOT NULL,
    confirmed_at DATETIME NOT NULL
);
CREATE INDEX access_reviews_ws ON access_reviews(workspace_id, confirmed_at);
