-- F8: estado de los jobs del sistema, para retomar donde quedaron y para mostrarlo en /readyz y en
-- el panel (SPEC §11.2, §13.5, §21.2.4): hasta qué evento llegó la copia de la auditoría, el
-- último respaldo subido al destino externo y la última verificación de un respaldo. Un JSON
-- por job; no son ajustes de nadie, por eso no van en site_settings.
CREATE TABLE job_state (
    name TEXT PRIMARY KEY,
    value_json TEXT NOT NULL,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
