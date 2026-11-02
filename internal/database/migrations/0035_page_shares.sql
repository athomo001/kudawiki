-- F6: artículos compartidos entre áreas (SPEC §4.19, ADR-0010).

-- Un vínculo de una página de un área (workspace_id) con otra área (target_workspace_id), nunca
-- una copia: el destino lee la versión publicada actual de la página. Es la vía con nombre propio
-- de ADR-0010: se consulta siempre por esta tabla, nunca ampliando el Scope del destino.
-- pending: pedido por un editor, espera al admin del origen; active: se ve en Compartido del
-- destino; rejected: el admin del origen dijo que no; ended: ya no se comparte (end_reason dice
-- por qué). Las filas se conservan: son la historia de qué se compartió con quién.
CREATE TABLE page_shares (
    id TEXT PRIMARY KEY,                       -- ULID
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    page_id TEXT NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    target_workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK(status IN ('pending','active','rejected','ended')),
    requested_by TEXT NOT NULL REFERENCES users(id),
    reviewed_by TEXT REFERENCES users(id),
    review_comment TEXT,
    -- unshared: el admin del origen la retiró; removed: el admin del destino la quitó; level: la
    -- página subió de nivel; deleted: la página se borró. ended_by es NULL si lo hizo el sistema.
    end_reason TEXT CHECK(end_reason IN ('unshared','removed','level','deleted')),
    ended_by TEXT REFERENCES users(id),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    reviewed_at DATETIME,
    ended_at DATETIME,
    CHECK (workspace_id <> target_workspace_id),
    CHECK ((status = 'ended') = (end_reason IS NOT NULL))
);
-- Un solo pedido abierto o vínculo activo por página y destino; después de terminado se puede
-- volver a pedir (§4.19: "no vuelve salvo que el origen la comparta de nuevo").
CREATE UNIQUE INDEX page_shares_open ON page_shares(page_id, target_workspace_id) WHERE status IN ('pending','active');
CREATE INDEX page_shares_target ON page_shares(target_workspace_id, status);
CREATE INDEX page_shares_source ON page_shares(workspace_id, status);
