-- Un seguimiento solo recibe los eventos posteriores a él (SPEC §12.1). Compararlo por fecha no
-- alcanza: created_at tiene segundos, y un evento del mismo segundo en que alguien empezó a
-- seguir la página se repartía como si fuera posterior. El número del evento sí ordena: cada
-- seguimiento guarda el último evento que ya existía cuando empezó.
ALTER TABLE page_watchers ADD COLUMN after_event_id INTEGER NOT NULL DEFAULT 0;

-- Los seguimientos que ya existían conservan la regla de antes con los eventos pendientes: no
-- reciben los de un segundo anterior.
UPDATE page_watchers SET after_event_id = COALESCE(
    (SELECT MAX(e.id) FROM notification_events e WHERE e.created_at < page_watchers.created_at), 0);
