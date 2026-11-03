package wikijs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
	"testing/fstest"
	"time"

	"go.yaml.in/yaml/v3"

	// Los drivers de la BD de Wiki.js: Go puro, sin CGO, como el resto del binario (§16).
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Page es una página de Wiki.js tal como se leyó, antes de convertirla.
type Page struct {
	Path        string // su ruta en Wiki.js, sin "/" inicial ni extensión: "ti/redes/vpn"
	Locale      string
	Title       string
	Description string
	Tags        []string
	Published   bool
	HTML        bool // del editor visual: el contenido es HTML
	Content     string
	History     []Version // de la más vieja a la más nueva; solo desde la BD
}

// Version es una versión anterior de una página (pageHistory de Wiki.js).
type Version struct {
	Title, Content string
	HTML           bool
	Author, At     string
}

// maxFileBytes es la página más grande que se lee de la carpeta.
const maxFileBytes = 8 << 20

// ReadFolder lee la carpeta del almacenamiento "Local File System" o "Git" de Wiki.js: un .md
// (o un .html, del editor visual) por página, con su cabecera, y los archivos al lado.
func ReadFolder(fsys fs.FS) ([]Page, []string, error) {
	var pages []Page
	var unreadable []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != "." && strings.HasPrefix(d.Name(), ".") { // .git y compañía
				return fs.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(path.Ext(p))
		if ext != ".md" && ext != ".html" {
			return nil
		}
		raw, err := readFile(fsys, p)
		if err != nil {
			unreadable = append(unreadable, p)
			return nil
		}
		pg, err := parseFile(strings.TrimSuffix(p, path.Ext(p)), raw, ext == ".html")
		if err != nil {
			unreadable = append(unreadable, p)
			return nil
		}
		pages = append(pages, pg)
		return nil
	})
	return pages, unreadable, err
}

func readFile(fsys fs.FS, p string) (string, error) {
	f, err := fsys.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err == nil && len(b) > maxFileBytes {
		err = errors.New("too large")
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n"), err
}

// parseFile separa la cabecera de Wiki.js: YAML entre "---" en un .md, y el mismo YAML dentro
// de un comentario "<!-- … -->" en un .html.
func parseFile(p, raw string, isHTML bool) (Page, error) {
	pg := Page{Path: p, HTML: isHTML, Published: true, Content: raw}
	open, close := "---\n", "\n---"
	if isHTML {
		open, close = "<!--\n", "\n-->"
	}
	if !strings.HasPrefix(raw, open) {
		pg.Title = path.Base(p)
		return pg, nil
	}
	end := strings.Index(raw[len(open):], close)
	if end < 0 {
		return pg, errors.New("unterminated header")
	}
	head := raw[len(open) : len(open)+end]
	pg.Content = strings.TrimLeft(raw[len(open)+end+len(close):], "\n")
	var fm struct {
		Title       string `yaml:"title"`
		Description string `yaml:"description"`
		Published   *bool  `yaml:"published"`
		Tags        any    `yaml:"tags"`
	}
	if err := yaml.Unmarshal([]byte(head), &fm); err != nil {
		return pg, err
	}
	pg.Title, pg.Description = strings.TrimSpace(fm.Title), strings.TrimSpace(fm.Description)
	if pg.Title == "" {
		pg.Title = path.Base(p)
	}
	if fm.Published != nil {
		pg.Published = *fm.Published
	}
	switch t := fm.Tags.(type) {
	case string:
		pg.Tags = splitTags(t)
	case []any:
		for _, x := range t {
			pg.Tags = append(pg.Tags, splitTags(fmt.Sprint(x))...)
		}
	}
	return pg, nil
}

func splitTags(s string) []string {
	var out []string
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

// DB es la base de datos de una instalación de Wiki.js 2.x, solo para leer.
type DB struct {
	db    *sql.DB
	mysql bool
}

// OpenDB abre la BD de Wiki.js. dsn: "postgres://usuario:clave@host:5432/wiki" o
// "mysql://usuario:clave@tcp(host:3306)/wiki" (el formato del driver de MySQL con ese prefijo).
func OpenDB(dsn string) (*DB, error) {
	switch {
	case strings.HasPrefix(dsn, "postgres://"), strings.HasPrefix(dsn, "postgresql://"):
		db, err := sql.Open("pgx", dsn)
		return &DB{db: db}, err
	case strings.HasPrefix(dsn, "mysql://"):
		db, err := sql.Open("mysql", strings.TrimPrefix(dsn, "mysql://"))
		return &DB{db: db, mysql: true}, err
	}
	return nil, errors.New(`the database must start with "postgres://" or "mysql://"`)
}

// NewDB envuelve una conexión ya abierta; con mysql, los nombres van entre comillas invertidas.
func NewDB(db *sql.DB, mysql bool) *DB { return &DB{db: db, mysql: mysql} }

func (d *DB) Close() error { return d.db.Close() }

// q adapta la consulta: Wiki.js crea las columnas en camelCase, y PostgreSQL las quiere entre
// comillas dobles, MySQL entre comillas invertidas; los parámetros son $1 o ?.
func (d *DB) q(s string) string {
	if d.mysql {
		return strings.NewReplacer(`"`, "`", "$1", "?").Replace(s)
	}
	return s
}

// Pages lee las páginas con sus etiquetas y su historial.
func (d *DB) Pages(ctx context.Context) ([]Page, error) {
	rows, err := d.db.QueryContext(ctx, d.q(`SELECT p.id, p.path, COALESCE(p."localeCode", ''), COALESCE(p.title, ''),
		COALESCE(p.description, ''), p."isPublished", COALESCE(p."contentType", 'markdown'), COALESCE(p.content, '')
		FROM pages p ORDER BY p.id`))
	if err != nil {
		return nil, err
	}
	var pages []Page
	index := map[int64]int{}
	for rows.Next() {
		var id int64
		var pg Page
		var ctype string
		if err := rows.Scan(&id, &pg.Path, &pg.Locale, &pg.Title, &pg.Description, &pg.Published, &ctype, &pg.Content); err != nil {
			rows.Close()
			return nil, err
		}
		pg.Path = strings.Trim(pg.Path, "/")
		pg.HTML = ctype == "html"
		index[id] = len(pages)
		pages = append(pages, pg)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	if rows, err = d.db.QueryContext(ctx, d.q(`SELECT pt."pageId", t.tag FROM "pageTags" pt JOIN tags t ON t.id = pt."tagId" ORDER BY pt.id`)); err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var tag string
		if err := rows.Scan(&id, &tag); err != nil {
			rows.Close()
			return nil, err
		}
		if i, ok := index[id]; ok {
			pages[i].Tags = append(pages[i].Tags, tag)
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	// Cada fila de pageHistory es la versión que había antes de un cambio: en orden de id, de la
	// más vieja a la más nueva, y la página misma es la última.
	if rows, err = d.db.QueryContext(ctx, d.q(`SELECT h."pageId", COALESCE(h.title, ''), COALESCE(h.content, ''),
		COALESCE(h."contentType", 'markdown'), COALESCE(u.name, ''), h."createdAt"
		FROM "pageHistory" h LEFT JOIN users u ON u.id = h."authorId" ORDER BY h.id`)); err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var v Version
		var ctype string
		var at any
		if err := rows.Scan(&id, &v.Title, &v.Content, &ctype, &v.Author, &at); err != nil {
			rows.Close()
			return nil, err
		}
		v.HTML, v.At = ctype == "html", dateOf(at)
		if i, ok := index[id]; ok {
			pages[i].History = append(pages[i].History, v)
		}
	}
	return pages, errors.Join(rows.Err(), rows.Close())
}

// dateOf es la fecha de una versión como texto, venga del driver como venga.
func dateOf(v any) string {
	switch t := v.(type) {
	case time.Time:
		return t.UTC().Format("2006-01-02")
	case []byte:
		return dateText(string(t))
	case string:
		return dateText(t)
	}
	return ""
}

func dateText(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

// Assets devuelve los archivos de Wiki.js como un fs.FS: cada uno en la ruta de su carpeta, que
// es la que usan los enlaces de las páginas ("/manuales/red.png"). El contenido se lee de la BD
// solo cuando el importador lo abre.
func (d *DB) Assets(ctx context.Context) (fs.FS, error) {
	rows, err := d.db.QueryContext(ctx, d.q(`SELECT id, COALESCE(slug, name), "parentId" FROM "assetFolders"`))
	if err != nil {
		return nil, err
	}
	type folder struct {
		slug   string
		parent sql.NullInt64
	}
	folders := map[int64]folder{}
	for rows.Next() {
		var id int64
		var f folder
		if err := rows.Scan(&id, &f.slug, &f.parent); err != nil {
			rows.Close()
			return nil, err
		}
		folders[id] = f
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	folderPath := func(id sql.NullInt64) string {
		var parts []string
		for seen := 0; id.Valid && seen < 64; seen++ { // 64: una carpeta que se contiene a sí misma no cuelga el importador
			f, ok := folders[id.Int64]
			if !ok {
				break
			}
			parts = append([]string{f.slug}, parts...)
			id = f.parent
		}
		return strings.Join(parts, "/")
	}
	if rows, err = d.db.QueryContext(ctx, d.q(`SELECT id, filename, "folderId" FROM assets`)); err != nil {
		return nil, err
	}
	a := assetFS{ctx: ctx, db: d, ids: map[string]int64{}}
	for rows.Next() {
		var id int64
		var name string
		var folderID sql.NullInt64
		if err := rows.Scan(&id, &name, &folderID); err != nil {
			rows.Close()
			return nil, err
		}
		a.ids[path.Join(folderPath(folderID), name)] = id
	}
	return a, errors.Join(rows.Err(), rows.Close())
}

type assetFS struct {
	ctx context.Context
	db  *DB
	ids map[string]int64
}

func (a assetFS) Open(name string) (fs.File, error) {
	id, ok := a.ids[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	var data []byte
	if err := a.db.db.QueryRowContext(a.ctx, a.db.q(`SELECT data FROM "assetData" WHERE id = $1`), id).Scan(&data); err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	return fstest.MapFS{name: {Data: data}}.Open(name)
}
