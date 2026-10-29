package repo

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// SearchQuery es la búsqueda ya traducida por el paquete search: Match es una expresión FTS5
// armada por el sistema, nunca el texto crudo de la persona (SPEC §9.3).
type SearchQuery struct {
	Match   string
	Words   []string // las palabras buscadas, para "¿quisiste decir?" y las búsquedas sin resultados
	Tags    []string
	TagsAny bool
	Path    string
	Author  string
	Since   time.Time
	ByDate  bool // ordenar por última modificación en vez de por relevancia (§9.4)
}

// IndexDoc es lo que se indexa de una página (SPEC §9.1).
type IndexDoc struct {
	Lang                                                  string // decide el stemmer; no se guarda en el índice
	Title, Description, Headings, Content, Code           string
	TitleStem, DescriptionStem, HeadingsStem, ContentStem string
}

type SearchHit struct {
	ID, Slug, Title   string
	Description, Icon string // para mostrar en el resultado (§4.1)
	UpdatedAt         time.Time
	// Los llena Snippets, solo para los resultados que se muestran: el fragmento con las
	// coincidencias entre HitStart y HitEnd, y el Markdown para ubicar su sección (§9.4).
	Snippet, Markdown string
	rowid             int64
}

// Marcas del fragmento: caracteres de uso privado y no HTML, así el texto de una página nunca
// llega al navegador como marcado; la interfaz lo pinta como texto.
const (
	HitStart = ""
	HitEnd   = ""
)

// writeIndex reescribe la fila de la página en los dos índices, dentro de la transacción del
// guardado (SPEC §9.1). Cada página ocupa siempre la misma fila (pages.search_rowid), así
// reindexar no recorre el índice. El área y el nivel no van al índice: se filtran en pages.
func writeIndex(ctx context.Context, tx *sql.Tx, pageID string, d IndexDoc) error {
	var rowid int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(search_rowid, 0) FROM pages WHERE id = ?`, pageID).Scan(&rowid); err != nil {
		return err
	}
	if rowid == 0 {
		if err := tx.QueryRowContext(ctx,
			`UPDATE pages SET search_rowid = (SELECT COALESCE(MAX(search_rowid), 0) + 1 FROM pages) WHERE id = ? RETURNING search_rowid`,
			pageID).Scan(&rowid); err != nil {
			return err
		}
	} else {
		for _, q := range []string{`DELETE FROM pages_fts WHERE rowid = ?`, `DELETE FROM pages_title_trgm WHERE rowid = ?`} {
			if _, err := tx.ExecContext(ctx, q, rowid); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO pages_fts (rowid, title, tags, headings, description, content_text, code_text,
		   title_stem, headings_stem, description_stem, content_stem)
		 VALUES (?, ?, COALESCE((SELECT group_concat(tag, ' ') FROM page_tags WHERE page_id = ?), ''), ?, ?, ?, ?, ?, ?, ?, ?)`,
		rowid, d.Title, pageID, d.Headings, d.Description, d.Content, d.Code, d.TitleStem, d.HeadingsStem, d.DescriptionStem, d.ContentStem); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO pages_title_trgm (rowid, title) VALUES (?, ?)`, rowid, d.Title)
	return err
}

// Search devuelve hasta limit candidatos dentro del Scope, ya ordenados: toda consulta une con
// pages y filtra por su área, su nivel y la papelera (SPEC §9.2, §4.5); lo borrado sigue en el
// índice hasta la purga. Sin términos (solo filtros) lista por fecha de modificación. Los
// candidatos sirven para la página que se muestra y para los conteos de los filtros, así la
// búsqueda FTS corre una sola vez. El fragmento se pide aparte (Snippets): medido con 50 000
// páginas (§9.5), calcularlo para todas las coincidencias antes de ordenar costaba ~20 ms.
func (r *Repo) Search(ctx context.Context, s Scope, q SearchQuery, limit int) ([]SearchHit, error) {
	query, args := searchSQL(s, q, limit)
	rows, err := r.db.Read.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (SearchHit, error) {
		var h SearchHit
		return h, row.Scan(&h.rowid, &h.ID, &h.Slug, &h.Title, &h.Description, &h.Icon, &h.UpdatedAt)
	})
}

// Snippets llena el fragmento y el Markdown de los resultados dados, que salieron de Search
// con el mismo Scope; la consulta vuelve a filtrar por él.
func (r *Repo) Snippets(ctx context.Context, s Scope, q SearchQuery, hits []SearchHit) error {
	if q.Match == "" || len(hits) == 0 {
		return nil
	}
	args := []any{q.Match, s.WsID, s.MaxRank}
	for _, h := range hits {
		args = append(args, h.rowid)
	}
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT f.rowid, snippet(pages_fts, 4, '`+HitStart+`', '`+HitEnd+`', '…', 14), p.content_markdown
		 FROM pages_fts f CROSS JOIN pages p ON p.search_rowid = f.rowid
		 WHERE pages_fts MATCH ? AND p.workspace_id = ? AND p.deleted_at IS NULL AND p.min_rank <= ?
		   AND f.rowid IN (?`+strings.Repeat(",?", len(hits)-1)+`)`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	type found struct{ snippet, markdown string }
	byRow := map[int64]found{}
	for rows.Next() {
		var id int64
		var f found
		if err := rows.Scan(&id, &f.snippet, &f.markdown); err != nil {
			return err
		}
		byRow[id] = f
	}
	for i := range hits {
		hits[i].Snippet, hits[i].Markdown = byRow[hits[i].rowid].snippet, byRow[hits[i].rowid].markdown
	}
	return rows.Err()
}

// SearchMeta trae las etiquetas y los autores (quien escribió alguna revisión, como el filtro
// autor:) de las páginas dadas, para contar los filtros (SPEC §9.4). Vuelve a filtrar por el
// Scope: un conteo nunca incluye una página que quien busca no ve (§17).
func (r *Repo) SearchMeta(ctx context.Context, s Scope, pageIDs []string) (tags, authors map[string][]string, err error) {
	tags, authors = map[string][]string{}, map[string][]string{}
	if len(pageIDs) == 0 {
		return tags, authors, nil
	}
	tagsSQL, authorsSQL, args := searchMetaSQL(s, pageIDs)
	for _, x := range []struct {
		query string
		into  map[string][]string
	}{{tagsSQL, tags}, {authorsSQL, authors}} {
		rows, err := r.db.Read.QueryContext(ctx, x.query, args...)
		if err != nil {
			return nil, nil, err
		}
		pairs, err := collect(rows, func(row scanner) ([2]string, error) {
			var p [2]string
			return p, row.Scan(&p[0], &p[1])
		})
		if err != nil {
			return nil, nil, err
		}
		for _, p := range pairs {
			x.into[p[0]] = append(x.into[p[0]], p[1])
		}
	}
	return tags, authors, nil
}

// searchMetaSQL arma las dos consultas de SearchMeta. Empiezan por la lista de candidatos con
// CROSS JOIN, que en SQLite fija el orden: si no, el planificador prefiere recorrer todas las
// páginas del área por pages_ws_rank y, con 50 000, cada consulta tardaba ~150 ms en caliente y
// más de un segundo en frío (medido; con la lista primero, 0,3 y 5 ms).
func searchMetaSQL(s Scope, pageIDs []string) (tags, authors string, args []any) {
	cands := `WITH c(id) AS (VALUES (?)` + strings.Repeat(",(?)", len(pageIDs)-1) + `) `
	scope := ` WHERE p.workspace_id = ? AND p.deleted_at IS NULL AND p.min_rank <= ?`
	for _, id := range pageIDs {
		args = append(args, id)
	}
	args = append(args, s.WsID, s.MaxRank)
	tags = cands + `SELECT t.page_id, t.tag FROM c CROSS JOIN pages p ON p.id = c.id CROSS JOIN page_tags t ON t.page_id = p.id` + scope
	authors = cands + `SELECT DISTINCT r.page_id, u.full_name FROM c CROSS JOIN pages p ON p.id = c.id
		CROSS JOIN page_revisions r ON r.page_id = p.id JOIN users u ON u.id = r.created_by` + scope
	return tags, authors, args
}

// searchSQL arma la consulta. CROSS JOIN obliga a SQLite a recorrer primero el índice FTS5 y
// después buscar cada coincidencia en pages: con un JOIN normal el planificador puede elegir
// recorrer pages y ejecutar la búsqueda FTS5 una vez por página (medido: minutos con 50 000).
func searchSQL(s Scope, q SearchQuery, limit int) (string, []any) {
	var b strings.Builder
	var args []any
	if q.Match != "" {
		b.WriteString(`SELECT f.rowid, p.id, p.slug, p.title, p.description, p.icon, p.updated_at FROM pages_fts f CROSS JOIN pages p ON p.search_rowid = f.rowid
			WHERE pages_fts MATCH ? AND p.workspace_id = ? AND p.deleted_at IS NULL AND p.min_rank <= ?`)
		args = append(args, q.Match, s.WsID, s.MaxRank)
	} else {
		b.WriteString(`SELECT 0, p.id, p.slug, p.title, p.description, p.icon, p.updated_at FROM pages p WHERE p.workspace_id = ? AND p.deleted_at IS NULL AND p.min_rank <= ?`)
		args = append(args, s.WsID, s.MaxRank)
	}
	if q.Path != "" {
		b.WriteString(` AND (p.slug = ? OR p.slug LIKE ?)`)
		args = append(args, q.Path, q.Path+"/%")
	}
	if len(q.Tags) > 0 && q.TagsAny {
		b.WriteString(` AND EXISTS (SELECT 1 FROM page_tags t WHERE t.page_id = p.id AND t.tag IN (?` + strings.Repeat(",?", len(q.Tags)-1) + `))`)
		for _, t := range q.Tags {
			args = append(args, t)
		}
	} else {
		for _, t := range q.Tags {
			b.WriteString(` AND EXISTS (SELECT 1 FROM page_tags t WHERE t.page_id = p.id AND t.tag = ?)`)
			args = append(args, t)
		}
	}
	if q.Author != "" {
		b.WriteString(` AND EXISTS (SELECT 1 FROM page_revisions r JOIN users u ON u.id = r.created_by
			WHERE r.page_id = p.id AND (u.full_name LIKE ? OR u.email LIKE ?))`)
		args = append(args, "%"+q.Author+"%", "%"+q.Author+"%")
	}
	if !q.Since.IsZero() {
		b.WriteString(` AND p.updated_at >= ?`)
		args = append(args, q.Since.Format("2006-01-02"))
	}
	if q.Match != "" && !q.ByDate {
		// Pesos bm25 de §9.1, en el orden de las columnas de pages_fts.
		b.WriteString(` ORDER BY bm25(pages_fts, 10, 6, 4, 3, 1, 0.5, 10, 4, 3, 1)`)
	} else {
		b.WriteString(` ORDER BY p.updated_at DESC, p.slug`)
	}
	b.WriteString(` LIMIT ?`)
	return b.String(), append(args, limit)
}

// SuggestTitle propone el título visible que más se parece a lo buscado, por trigramas: así
// "procedimento" sugiere "Procedimiento de conexión VPN". Nunca sugiere lo que el lector no ve (§9.2).
func (r *Repo) SuggestTitle(ctx context.Context, s Scope, words []string) (string, error) {
	text := strings.ToLower(strings.Join(words, " "))
	var grams []string
	runes := []rune(text)
	for i := 0; i+3 <= len(runes); i++ {
		if g := string(runes[i : i+3]); !strings.Contains(g, " ") {
			grams = append(grams, `"`+strings.ReplaceAll(g, `"`, `""`)+`"`)
		}
	}
	if len(grams) == 0 {
		return "", nil
	}
	var title string
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT p.title FROM pages_title_trgm t CROSS JOIN pages p ON p.search_rowid = t.rowid
		 WHERE pages_title_trgm MATCH ? AND p.workspace_id = ? AND p.deleted_at IS NULL AND p.min_rank <= ?
		 ORDER BY bm25(pages_title_trgm) LIMIT 1`, strings.Join(grams, " OR "), s.WsID, s.MaxRank).Scan(&title)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return title, err
}

// RecordMiss cuenta una búsqueda sin resultados, sin guardar quién la hizo (SPEC §9.6).
func (r *Repo) RecordMiss(ctx context.Context, s Scope, query string) error {
	_, err := r.db.Write.ExecContext(ctx,
		`INSERT INTO search_misses (workspace_id, query_norm, day) VALUES (?, ?, date('now'))
		 ON CONFLICT (workspace_id, query_norm, day) DO UPDATE SET count = count + 1`, s.WsID, query)
	return err
}

type Miss struct {
	Query string
	Count int
}

// Misses son las búsquedas sin resultados más repetidas de los últimos 90 días.
func (r *Repo) Misses(ctx context.Context, s Scope) ([]Miss, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT query_norm, SUM(count) FROM search_misses WHERE workspace_id = ? AND day >= date('now', '-90 day')
		 GROUP BY query_norm ORDER BY SUM(count) DESC, query_norm LIMIT 20`, s.WsID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (Miss, error) {
		var m Miss
		return m, row.Scan(&m.Query, &m.Count)
	})
}

// IndexStale dice si hay páginas sin indexar (p. ej. las creadas antes de la búsqueda).
func (r *Repo) IndexStale(ctx context.Context) (bool, error) {
	var stale bool
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT (SELECT COUNT(*) FROM pages) <> (SELECT COUNT(*) FROM pages_fts)`).Scan(&stale)
	return stale, err
}

// RebuildIndex regenera el índice de todas las páginas de todas las áreas: el índice es
// derivado y nunca es la única copia de nada (SPEC §9.7). Es una operación del sistema.
func (r *Repo) RebuildIndex(ctx context.Context, derive func(title, description, markdown, lang string) IndexDoc) error {
	rows, err := r.db.Read.QueryContext(ctx, `SELECT id, title, description, content_markdown, lang FROM pages`)
	if err != nil {
		return err
	}
	type row struct{ id, title, description, markdown, lang string }
	pages, err := collect(rows, func(sc scanner) (row, error) {
		var p row
		return p, sc.Scan(&p.id, &p.title, &p.description, &p.markdown, &p.lang)
	})
	if err != nil {
		return err
	}
	tx, err := r.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{`DELETE FROM pages_fts`, `DELETE FROM pages_title_trgm`} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	for _, p := range pages {
		if err := writeIndex(ctx, tx, p.id, derive(p.title, p.description, p.markdown, p.lang)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
