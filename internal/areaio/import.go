package areaio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/athomo001/kudawiki/internal/wiki"
	"go.yaml.in/yaml/v3"
)

// Límites del importador: un .md más grande que esto no es una página (CheckPage lo rechazaría
// igual) y un ZIP con más archivos que esto no es un área, es un abuso.
const (
	maxPageBytes = 4 << 20
	maxFiles     = 50000
	// maxReadBytes es todo lo que se lee de los .md. Sin este tope, un ZIP de 1 GiB con 50 000
	// páginas muy comprimibles hacía descomprimir y guardar en memoria 200 GiB (F8.15).
	maxReadBytes = 1 << 30
)

var (
	// ErrTooManyFiles: el ZIP o la carpeta trae más archivos de los que un área puede tener.
	ErrTooManyFiles = errors.New("import_too_many_files")
	// ErrTooLarge: las páginas, ya descomprimidas, pasan de maxReadBytes.
	ErrTooLarge = errors.New("import_too_large")
)

// Page es una página que el importador va a crear.
type Page struct {
	Path, Slug string
	FrontMatter
	Markdown string
	// Summary es el resumen de la revisión con el contenido actual; History, las versiones
	// anteriores de más vieja a más nueva, que se publican antes como revisiones propias (el
	// importador de Wiki.js las trae de su BD, §13.8).
	Summary string
	History []Version
}

// Version es una versión anterior de una página.
type Version struct {
	Title, Markdown, Summary string
}

// Skip es un archivo que no se importa, con el motivo (un código estable que la interfaz traduce).
type Skip struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Warning es algo que se importó, pero no del todo: un archivo que no se pudo subir, un dueño que
// no es del área, un nivel que no se pudo poner.
type Warning struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
}

// Report es lo que el importador hizo, o haría con dry-run.
type Report struct {
	Pages    []string  `json:"pages"`
	Skipped  []Skip    `json:"skipped"`
	Media    int       `json:"media"`
	Warnings []Warning `json:"warnings"`
}

// Writer es lo que el importador usa para crear: las mismas operaciones que la interfaz, con sus
// validaciones, su auditoría y su índice.
type Writer interface {
	Save(ctx context.Context, sc repo.Scope, actorID string, base int, w repo.PageWrite) error
	SetTags(ctx context.Context, sc repo.Scope, slug string, tags []string) error
	SetInfo(ctx context.Context, sc repo.Scope, slug string, description, icon *string) error
	SetLevel(ctx context.Context, sc repo.Scope, actorID, slug string, level int) error
	SetReview(ctx context.Context, sc repo.Scope, actorID, slug string, ownerID *string, days *int) error
}

type uploader interface {
	Upload(ctx context.Context, sc repo.Scope, actorID, filename string, body io.Reader) (repo.Media, error)
}

type importStore interface {
	PageBySlug(ctx context.Context, s repo.Scope, slug string) (repo.Page, error)
	UserByEmail(ctx context.Context, email string) (repo.User, error)
}

// Importer crea páginas en un área a partir de archivos Markdown.
type Importer struct {
	Store importStore
	Pages Writer
	Media uploader
}

// Target es dónde se importa: el área (su Scope y su slug, para las URL de la media) y quién.
type Target struct {
	Scope   repo.Scope
	Area    string
	ActorID string
}

// Read arma las páginas de una carpeta o un ZIP (como fs.FS). Si es un ZIP de Kudawiki, las
// páginas están en pages/; si no, cada .md es una página y su ruta sale de la del archivo.
// Lo que no sirve queda en skipped.
func Read(fsys fs.FS) ([]Page, []Skip, error) {
	root := "."
	if _, err := fs.Stat(fsys, "manifest.json"); err == nil {
		if _, err := fs.Stat(fsys, "pages"); err == nil {
			root = "pages"
		}
	}
	var pages []Page
	var skipped []Skip
	seen := map[string]string{}
	n, read := 0, 0
	err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if n++; n > maxFiles {
			return ErrTooManyFiles
		}
		if d.IsDir() || !strings.EqualFold(path.Ext(p), ".md") {
			return nil
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, root), "/")
		slug := SlugFromPath(strings.TrimSuffix(rel, path.Ext(rel)))
		if !wiki.ValidSlug(slug) {
			skipped = append(skipped, Skip{Path: p, Reason: "invalid_path"})
			return nil
		}
		if other, dup := seen[slug]; dup {
			skipped = append(skipped, Skip{Path: p, Reason: "duplicate_path:" + other})
			return nil
		}
		raw, err := readLimited(fsys, p)
		if read += len(raw); read > maxReadBytes {
			return ErrTooLarge
		}
		if err != nil {
			skipped = append(skipped, Skip{Path: p, Reason: "too_large"})
			return nil
		}
		fm, body, err := ParseFrontMatter(raw)
		if err != nil {
			skipped = append(skipped, Skip{Path: p, Reason: "bad_front_matter"})
			return nil
		}
		if fm.Title == "" {
			fm.Title, body = titleFrom(body, path.Base(strings.TrimSuffix(rel, path.Ext(rel))))
		}
		seen[slug] = p
		pages = append(pages, Page{Path: p, Slug: slug, FrontMatter: fm, Markdown: body})
		return nil
	})
	// De padres a hijas: cada página nace con el nivel de su padre ya puesto (§3.2).
	slices.SortFunc(pages, func(a, b Page) int { return strings.Compare(a.Slug, b.Slug) })
	return pages, skipped, err
}

func readLimited(fsys fs.FS, p string) (string, error) {
	f, err := fsys.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxPageBytes+1))
	if err != nil {
		return "", err
	}
	if len(b) > maxPageBytes {
		// Lo leído vuelve igual, para que cuente en maxReadBytes.
		return string(b), errors.New("too large")
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n"), nil
}

// ParseFrontMatter separa la cabecera YAML del contenido. Acepta las etiquetas como lista o como
// texto separado por comas (así las guarda Wiki.js) y deja pasar los campos que no conoce.
func ParseFrontMatter(s string) (FrontMatter, string, error) {
	var fm FrontMatter
	if !strings.HasPrefix(s, "---\n") {
		return fm, s, nil
	}
	end := strings.Index(s[4:], "\n---")
	if end < 0 {
		return fm, s, nil
	}
	head, body := s[4:4+end], s[4+end+4:]
	body = strings.TrimPrefix(strings.TrimPrefix(body, "\n"), "\n")
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(head), &raw); err != nil {
		return fm, s, err
	}
	str := func(k string) string {
		if v, ok := raw[k]; ok && v != nil {
			return strings.TrimSpace(fmt.Sprint(v))
		}
		return ""
	}
	fm.Title, fm.Description, fm.Owner, fm.Lang = str("title"), str("description"), str("owner"), str("lang")
	if l, ok := raw["level"].(int); ok {
		fm.Level = l
	}
	switch t := raw["tags"].(type) {
	case string:
		for _, x := range strings.Split(t, ",") {
			if x = strings.TrimSpace(x); x != "" {
				fm.Tags = append(fm.Tags, x)
			}
		}
	case []any:
		for _, x := range t {
			if s := strings.TrimSpace(fmt.Sprint(x)); s != "" {
				fm.Tags = append(fm.Tags, s)
			}
		}
	}
	return fm, body, nil
}

// titleFrom toma el título del primer "# " del contenido, y lo saca de ahí para que no salga
// dos veces; sin él, el nombre del archivo.
func titleFrom(body, file string) (string, string) {
	for i, line := range strings.SplitN(body, "\n", 50) {
		if t, ok := strings.CutPrefix(line, "# "); ok && strings.TrimSpace(t) != "" {
			lines := strings.Split(body, "\n")
			return strings.TrimSpace(t), strings.TrimLeft(strings.Join(append(lines[:i], lines[i+1:]...), "\n"), "\n")
		}
		if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "#") {
			break
		}
	}
	return strings.ReplaceAll(strings.ReplaceAll(file, "-", " "), "_", " "), body
}

var fold = strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u", "ñ", "n", "à", "a", "è", "e", "ì", "i", "ò", "o", "ù", "u", "ç", "c")

// SlugFromPath convierte una ruta de archivo en una ruta de página: minúsculas sin tildes,
// espacios y "_" como "-", y fuera lo demás (§4.1).
func SlugFromPath(p string) string {
	parts := strings.Split(strings.ReplaceAll(p, `\`, "/"), "/")
	out := parts[:0]
	for _, part := range parts {
		part = fold.Replace(strings.ToLower(part))
		var b strings.Builder
		dash := false
		for _, r := range part {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
				b.WriteRune(r)
				dash = false
			case !dash && b.Len() > 0:
				b.WriteByte('-')
				dash = true
			}
		}
		if s := strings.Trim(b.String(), "-"); s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, "/")
}

// linkRe encuentra los enlaces e imágenes de Markdown: [texto](destino) y ![alt](destino).
var linkRe = regexp.MustCompile(`(!?)\[([^\]\n]*)\]\(\s*<?([^)\s>]+)>?(\s+"[^"\n]*")?\s*\)`)

// Run crea las páginas en el área. Con dryRun no escribe nada: dice qué haría. Una página cuya ruta
// ya existe se salta, nunca se pisa.
func (im Importer) Run(ctx context.Context, t Target, fsys fs.FS, pages []Page, dryRun bool) (Report, error) {
	rep := Report{Pages: []string{}, Skipped: []Skip{}, Warnings: []Warning{}}
	byPath := map[string]string{} // ruta del archivo sin .md → slug
	for _, p := range pages {
		byPath[strings.TrimSuffix(p.Path, path.Ext(p.Path))] = p.Slug
	}
	uploaded := map[string]string{} // archivo → URL de su media
	for _, p := range pages {
		if _, err := im.Store.PageBySlug(ctx, t.Scope, p.Slug); err == nil {
			rep.Skipped = append(rep.Skipped, Skip{Path: p.Path, Reason: "exists"})
			continue
		} else if !errors.Is(err, repo.ErrNotFound) {
			return rep, err
		}
		body := im.rewrite(ctx, t, fsys, p.Path, p.Markdown, byPath, uploaded, &rep, dryRun, true)
		rep.Pages = append(rep.Pages, p.Slug)
		if dryRun {
			continue
		}
		versions := make([]Version, 0, len(p.History)+1)
		for _, v := range p.History {
			// Los avisos salen del contenido actual: un enlace roto de una versión vieja no importa.
			versions = append(versions, Version{Title: v.Title, Summary: v.Summary,
				Markdown: im.rewrite(ctx, t, fsys, p.Path, v.Markdown, byPath, uploaded, &rep, false, false)})
		}
		versions = append(versions, Version{Title: p.Title, Markdown: body, Summary: p.Summary})
		if err := im.save(ctx, t, p, versions); err != nil {
			rep.Pages = rep.Pages[:len(rep.Pages)-1]
			rep.Skipped = append(rep.Skipped, Skip{Path: p.Path, Reason: errCode(err)})
			continue
		}
		im.meta(ctx, t, p, &rep)
	}
	return rep, nil
}

// save publica las versiones en orden, cada una como una revisión. Una versión igual a la anterior
// no crea revisión (§4.3), por eso la base se lee después de cada una. Si la primera no se pudo
// crear, la página no existe; si falla una posterior, queda con lo que alcanzó y se informa.
func (im Importer) save(ctx context.Context, t Target, p Page, versions []Version) error {
	lang := p.Lang
	if lang != "es" && lang != "en" {
		lang = ""
	}
	base := 0
	for _, v := range versions {
		title := v.Title
		if title == "" {
			title = p.Title
		}
		if err := im.Pages.Save(ctx, t.Scope, t.ActorID, base, repo.PageWrite{Slug: p.Slug, Title: title, Markdown: v.Markdown,
			Lang: lang, Summary: v.Summary}); err != nil {
			return err
		}
		cur, err := im.Store.PageBySlug(ctx, t.Scope, p.Slug)
		if err != nil {
			return err
		}
		base = cur.Revision
	}
	return nil
}

// meta pone lo que el front matter trae además del contenido. Cada cosa que no se puede poner es
// un aviso: la página ya está creada.
func (im Importer) meta(ctx context.Context, t Target, p Page, rep *Report) {
	warn := func(reason string, err error) {
		rep.Warnings = append(rep.Warnings, Warning{Path: p.Path, Reason: reason, Detail: errCode(err)})
	}
	if len(p.Tags) > 0 {
		if err := im.Pages.SetTags(ctx, t.Scope, p.Slug, p.Tags); err != nil {
			warn("tags", err)
		}
	}
	if p.Description != "" {
		d := p.Description
		if err := im.Pages.SetInfo(ctx, t.Scope, p.Slug, &d, nil); err != nil {
			warn("description", err)
		}
	}
	if p.Level > 1 {
		if err := im.Pages.SetLevel(ctx, t.Scope, t.ActorID, p.Slug, p.Level); err != nil {
			warn("level", err)
		}
	}
	if p.Owner != "" {
		u, err := im.Store.UserByEmail(ctx, p.Owner)
		if err == nil {
			err = im.Pages.SetReview(ctx, t.Scope, t.ActorID, p.Slug, &u.ID, nil)
		}
		if err != nil {
			warn("owner", err)
		}
	}
}

// rewrite cambia los enlaces del contenido: a otro .md importado, por un [[enlace]]; a un archivo
// que viene al lado, por su media en el área (subiéndola una sola vez). Lo demás queda igual, y
// con warn, lo que no se encontró es un aviso: el enlace va a quedar roto.
func (im Importer) rewrite(ctx context.Context, t Target, fsys fs.FS, file, markdown string, byPath, uploaded map[string]string, rep *Report, dryRun, warn bool) string {
	pagePath := file
	dir := path.Dir(file)
	broken := func(m, target string) string {
		if warn {
			rep.Warnings = append(rep.Warnings, Warning{Path: pagePath, Reason: "broken_link", Detail: target})
		}
		return m
	}
	return linkRe.ReplaceAllStringFunc(markdown, func(m string) string {
		sub := linkRe.FindStringSubmatch(m)
		bang, text, target := sub[1], sub[2], sub[3]
		if strings.Contains(target, "://") || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "mailto:") ||
			strings.HasPrefix(target, "/api/") {
			return m
		}
		clean, _, _ := strings.Cut(target, "#")
		clean, _, _ = strings.Cut(clean, "?")
		if dec, err := url.PathUnescape(clean); err == nil {
			clean = dec
		}
		var file string
		if strings.HasPrefix(clean, "/") {
			file = path.Clean(strings.TrimPrefix(clean, "/"))
		} else {
			file = path.Join(dir, clean)
		}
		if bang == "" {
			if slug, ok := byPath[strings.TrimSuffix(file, path.Ext(file))]; ok && (path.Ext(file) == "" || strings.EqualFold(path.Ext(file), ".md")) {
				if text == "" || text == slug {
					return "[[" + slug + "]]"
				}
				return "[[" + slug + "|" + strings.ReplaceAll(text, "]", "") + "]]"
			}
		}
		if strings.EqualFold(path.Ext(file), ".md") || strings.HasPrefix(file, "../") {
			return broken(m, target)
		}
		if u, ok := uploaded[file]; ok {
			return strings.Replace(m, target, u, 1)
		}
		f, err := fsys.Open(file)
		if err != nil {
			return broken(m, target)
		}
		defer f.Close()
		if st, err := f.Stat(); err != nil || st.IsDir() {
			return broken(m, target)
		}
		// Un archivo que solo usa una versión vieja se sube igual, para que esa versión se vea, pero
		// no se cuenta: el informe habla del contenido actual.
		count := 0
		if warn {
			count = 1
		}
		rep.Media += count
		if dryRun {
			uploaded[file] = target
			return m
		}
		md, err := im.Media.Upload(ctx, t.Scope, t.ActorID, path.Base(file), f)
		if err != nil {
			rep.Media -= count
			rep.Warnings = append(rep.Warnings, Warning{Path: pagePath, Reason: "media", Detail: file + ": " + errCode(err)})
			return m
		}
		u := "/api/w/" + t.Area + "/media/" + md.ID
		uploaded[file] = u
		return strings.Replace(m, target, u, 1)
	})
}

// errCode es el código estable de un error de dominio ("path_unavailable", "invalid_tags"…), o
// "error" si no es uno de ellos.
func errCode(err error) string {
	if s := err.Error(); s != "" && !strings.ContainsAny(s, " :") {
		return s
	}
	return "error"
}
