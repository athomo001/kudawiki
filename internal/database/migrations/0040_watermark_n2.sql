-- F7.5: marca de agua también en las páginas N2 del área (SPEC §4.15). En N3 y en los sellados
-- va siempre; en N2 la enciende el admin del área.
ALTER TABLE workspaces ADD COLUMN watermark_n2 INTEGER NOT NULL DEFAULT 0;
