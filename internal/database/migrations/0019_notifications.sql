-- F4: notificaciones y seguir páginas (SPEC §5.3, §12.1).

-- Outbox de eventos: la acción deja una fila en su misma transacción y nunca falla por culpa de
-- las notificaciones. El worker la reparte (verificando el acceso en ese momento) y la borra.
CREATE TABLE notification_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    kind TEXT NOT NULL CHECK(kind IN ('page_saved','page_deleted','proposal_submitted','proposal_resolved')),
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    actor_id TEXT,
    page_id TEXT,
    proposal_id TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- La bandeja de cada persona. min_rank es el rol que hace falta además del nivel de la página
-- (3: los avisos para admins); al listar y al mandar el correo se vuelve a mirar el rol y el
-- nivel actuales, así a quien le bajaron el rol no le queda un título que ya no puede ver (§17).
-- El título y el autor van en payload_json como estaban al avisar: la página puede borrarse.
CREATE TABLE notifications (
    id TEXT PRIMARY KEY,                        -- ULID
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,                         -- proposal_pending, proposal_approved, page_changed…
    category TEXT NOT NULL CHECK(category IN ('proposals','my_proposals','watched')),
    page_id TEXT REFERENCES pages(id) ON DELETE CASCADE,
    proposal_id TEXT REFERENCES proposals(id) ON DELETE CASCADE,
    min_rank INTEGER NOT NULL DEFAULT 1 CHECK(min_rank IN (1,2,3)),
    payload_json TEXT NOT NULL DEFAULT '{}',
    group_key TEXT,                             -- p.ej. "page_changed:<page_id>"
    count INTEGER NOT NULL DEFAULT 1,
    read_at DATETIME,
    emailed_at DATETIME,                        -- el correo ya se resolvió: salió, va al resumen o no corresponde
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
-- Una sola entrada sin leer por persona y grupo: cuarenta guardados son un aviso, no cuarenta.
CREATE UNIQUE INDEX notifications_unread_group ON notifications(user_id, group_key)
    WHERE read_at IS NULL AND group_key IS NOT NULL;
CREATE INDEX notifications_user ON notifications(user_id, updated_at);
CREATE INDEX notifications_email ON notifications(created_at) WHERE emailed_at IS NULL AND read_at IS NULL;

-- Seguir páginas. La papelera no borra estas filas (solo la purga, con la página), así que el
-- reparto de un borrado todavía encuentra a quienes la seguían.
CREATE TABLE page_watchers (
    page_id TEXT NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    include_children INTEGER NOT NULL DEFAULT 0 CHECK(include_children IN (0,1)),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (page_id, user_id)
);
CREATE INDEX page_watchers_user ON page_watchers(user_id);

-- Correo por categoría (§12): sin fila, inmediato. La bandeja de la wiki no se apaga: para dejar
-- de recibir avisos de una página, se deja de seguirla.
CREATE TABLE notification_prefs (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    category TEXT NOT NULL CHECK(category IN ('proposals','my_proposals','watched')),
    email TEXT NOT NULL CHECK(email IN ('immediate','daily','off')),
    PRIMARY KEY (user_id, category)
);

-- Seguir solo lo que uno crea o edita (§12.1, desactivable).
ALTER TABLE users ADD COLUMN auto_watch INTEGER NOT NULL DEFAULT 1 CHECK(auto_watch IN (0,1));

-- El recordatorio sale una vez por envío: reenviar una propuesta lo vuelve a armar (§5.3).
ALTER TABLE proposals ADD COLUMN reminded_at DATETIME;
