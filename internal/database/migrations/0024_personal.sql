-- F5: espacio personal (SPEC §7). Es un workspace con kind = 'personal', un solo miembro (su dueño,
-- admin) y slug "~<id del usuario en minúsculas>". Se crea la primera vez que la persona entra a
-- la wiki. No aparece en el selector de áreas ni en el panel de áreas del superadmin.
ALTER TABLE workspaces ADD COLUMN kind TEXT NOT NULL DEFAULT 'area' CHECK(kind IN ('area','personal'));
ALTER TABLE workspaces ADD COLUMN owner_user_id TEXT REFERENCES users(id);
CREATE UNIQUE INDEX workspaces_personal_owner ON workspaces(owner_user_id) WHERE kind = 'personal';

-- Una propuesta de página nueva que salió de un borrador personal: el borrador se queda en el
-- espacio personal y muestra en qué quedó (enviado, publicado, devuelto).
ALTER TABLE proposals ADD COLUMN source_personal_page_id TEXT REFERENCES pages(id) ON DELETE SET NULL;
CREATE INDEX proposals_source ON proposals(source_personal_page_id) WHERE source_personal_page_id IS NOT NULL;
