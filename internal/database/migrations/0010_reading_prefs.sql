-- F3.4: preferencias de lectura por persona (SPEC §15). Se aplican en todas sus áreas.
-- JSON chico y cerrado ({"font":"dyslexic","wide":true}); los valores los valida auth.
ALTER TABLE users ADD COLUMN reading_prefs_json TEXT NOT NULL DEFAULT '{}';
