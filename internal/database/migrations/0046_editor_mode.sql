-- F8.10: modo del editor de cada persona (SPEC §4.2): Markdown o visual (Milkdown). Es una
-- preferencia, como el idioma: el contenido siempre se guarda en Markdown.
ALTER TABLE users ADD COLUMN editor_mode TEXT NOT NULL DEFAULT 'markdown' CHECK(editor_mode IN ('markdown','visual'));
