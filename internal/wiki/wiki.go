// Package wiki tiene las páginas, sus revisiones, el diff y el render (SPEC §4.1, §4.3).
package wiki

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/sergi/go-diff/diffmatchpatch"
)

var (
	ErrInvalidPage = errors.New("invalid_page")
	ErrConflict    = errors.New("conflict")
	// ErrInvalidInfo: descripción demasiado larga o un ícono que no es un emoji (§4.1).
	ErrInvalidInfo = errors.New("invalid_page_info")
	// ErrPathUnavailable vale para una ruta ocupada y para una bajo un padre que quien escribe no
	// ve: un solo mensaje, para que la diferencia no delate páginas ocultas (§19.35).
	ErrPathUnavailable = errors.New("path_unavailable")
)

const (
	maxTitle    = 200
	maxMarkdown = 512 << 10 // 512 KiB por página
)

// El slug es una ruta jerárquica (redes/vpn/procedimiento): segmentos en minúsculas, números
// y guiones, hasta 10 niveles. Nada de "..", espacios ni mayúsculas que dupliquen rutas.
var slugRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*(?:/[a-z0-9]+(?:-[a-z0-9]+)*){0,9}$`)

// mediaRefs encuentra la media que usa el contenido (![x](/api/w/ti/media/ID)). La de otra
// área se descarta al asociar: repo solo toca media del Scope.
var mediaRe = regexp.MustCompile(`/api/w/[a-z0-9-]+/media/([0-9A-HJKMNP-TV-Z]{26})`)

func mediaRefs(markdown string) []string {
	var ids []string
	for _, m := range mediaRe.FindAllStringSubmatch(markdown, -1) {
		ids = append(ids, m[1])
	}
	return ids
}

func ValidSlug(slug string) bool { return len(slug) <= 255 && slugRe.MatchString(slug) }

type store interface {
	PageBySlug(ctx context.Context, s repo.Scope, slug string) (repo.Page, error)
	CreatePage(ctx context.Context, s repo.Scope, w repo.PageWrite) (string, error)
	UpdatePage(ctx context.Context, s repo.Scope, pageID string, base int, w repo.PageWrite) error
	RevisionByNumber(ctx context.Context, s repo.Scope, pageID string, n int) (repo.Revision, error)
	SetTags(ctx context.Context, s repo.Scope, pageID string, tags []string) error
	SetPageInfo(ctx context.Context, s repo.Scope, pageID, description, descriptionStem, icon string) error
	MovePages(ctx context.Context, s repo.Scope, actorID, from, to string, rewrite repo.Rewrite) (string, int, error)
	SetLevel(ctx context.Context, s repo.Scope, actorID, slug string, level int) (string, int, error)
	DeletePages(ctx context.Context, s repo.Scope, actorID, slug string) (string, int, error)
	RestorePages(ctx context.Context, s repo.Scope, actorID, id string) (int, error)
}

type Service struct {
	store store
	audit *audit.Recorder
}

func NewService(s store, rec *audit.Recorder) *Service { return &Service{store: s, audit: rec} }

// record registra un evento sobre una página del área (§11): meta lleva ids y números de
// revisión, nunca el título ni el contenido.
func (s *Service) record(ctx context.Context, sc repo.Scope, a audit.Action, pageID string, meta map[string]any) {
	s.audit.Record(ctx, audit.Event{Action: a, WsID: sc.WsID, TargetType: "page", TargetID: pageID, Meta: meta})
}

// unavailable registra el intento de usar una ruta no disponible y devuelve el error de
// siempre (§19.35). Muchos intentos de una persona es la forma de buscar páginas ocultas, y eso
// se ve con quién y cuántas veces. La ruta pedida no se guarda: le diría al superadmin, que no
// es miembro del área, que algo existe ahí.
func (s *Service) unavailable(ctx context.Context, sc repo.Scope, op, pageID string) error {
	s.record(ctx, sc, audit.PathUnavailable, pageID, map[string]any{"op": op})
	return ErrPathUnavailable
}

// Get es la lectura de una página. La de las páginas N3 queda en la auditoría (§11.1).
func (s *Service) Get(ctx context.Context, sc repo.Scope, slug string) (repo.Page, error) {
	p, err := s.store.PageBySlug(ctx, sc, slug)
	if err == nil && p.MinRank == 3 {
		s.record(ctx, sc, audit.PageRead, p.ID, map[string]any{"revision": p.Revision})
	}
	return p, err
}

// Save crea la página (base 0) o publica una revisión nueva sobre base. Si el título y el
// contenido no cambiaron, no se crea revisión (SPEC §4.3).
func (s *Service) Save(ctx context.Context, sc repo.Scope, actorID string, base int, w repo.PageWrite) error {
	id, rev, err := s.save(ctx, sc, actorID, base, w)
	switch {
	case err != nil || rev == 0:
		return err
	case rev == 1:
		s.record(ctx, sc, audit.PageCreate, id, map[string]any{"revision": rev})
	default:
		s.record(ctx, sc, audit.PageUpdate, id, map[string]any{"revision": rev})
	}
	return nil
}

// save devuelve la página y su revisión nueva; 0 si no hubo cambios.
func (s *Service) save(ctx context.Context, sc repo.Scope, actorID string, base int, w repo.PageWrite) (string, int, error) {
	w.Title, w.Summary, w.AuthorID = strings.TrimSpace(w.Title), strings.TrimSpace(w.Summary), actorID
	w.MediaIDs, w.Links = mediaRefs(w.Markdown), Links(w.Markdown)
	if !ValidSlug(w.Slug) || w.Title == "" || utf8.RuneCountInString(w.Title) > maxTitle || len(w.Markdown) > maxMarkdown ||
		(w.Lang != "" && w.Lang != "es" && w.Lang != "en") {
		return "", 0, ErrInvalidPage
	}
	if base == 0 {
		if w.Lang == "" {
			w.Lang = "es"
		}
		w.Index = IndexDoc(w.Title, "", w.Markdown, w.Lang) // la descripción se pone después, como metadato
		w.Kind = "created"
		id, err := s.store.CreatePage(ctx, sc, w)
		if errors.Is(err, repo.ErrConflict) || errors.Is(err, repo.ErrHidden) {
			return "", 0, s.unavailable(ctx, sc, "create", "")
		}
		return id, 1, err
	}
	p, err := s.store.PageBySlug(ctx, sc, w.Slug)
	if err != nil {
		return "", 0, err
	}
	if p.Revision != base {
		return "", 0, ErrConflict
	}
	if w.Lang == "" { // revertir o un cliente que no lo manda: se conserva el idioma
		w.Lang = p.Lang
	}
	if p.Title == w.Title && p.Markdown == w.Markdown && p.Lang == w.Lang {
		return p.ID, 0, nil
	}
	w.Index = IndexDoc(w.Title, p.Description, w.Markdown, w.Lang)
	if w.Kind == "" {
		w.Kind = "updated"
	}
	err = s.store.UpdatePage(ctx, sc, p.ID, base, w)
	if errors.Is(err, repo.ErrConflict) {
		return "", 0, ErrConflict // alguien guardó entre la lectura y la escritura
	}
	return p.ID, base + 1, err
}

// Revert publica una revisión nueva con el contenido de la n; la historia no se reescribe.
func (s *Service) Revert(ctx context.Context, sc repo.Scope, actorID, slug string, n, base int) error {
	p, err := s.store.PageBySlug(ctx, sc, slug)
	if err != nil {
		return err
	}
	old, err := s.store.RevisionByNumber(ctx, sc, p.ID, n)
	if err != nil {
		return err
	}
	id, rev, err := s.save(ctx, sc, actorID, base, repo.PageWrite{Slug: slug, Title: old.Title, Markdown: old.Markdown, Kind: "reverted"})
	if err == nil && rev > 0 {
		s.record(ctx, sc, audit.PageRevert, id, map[string]any{"revision": rev, "from_revision": n})
	}
	return err
}

// Delete manda a la papelera la página y las subpáginas que quien borra ve (SPEC §4.5, §19.37).
func (s *Service) Delete(ctx context.Context, sc repo.Scope, actorID, slug string) error {
	id, n, err := s.store.DeletePages(ctx, sc, actorID, slug)
	if err == nil {
		s.record(ctx, sc, audit.PageDelete, id, map[string]any{"pages": n})
	}
	return err
}

// Restore devuelve una unidad de la papelera a sus rutas. Si una está ocupada, o quedó bajo un
// padre que quien restaura no ve, la ruta no está disponible: un solo mensaje (§19.35).
func (s *Service) Restore(ctx context.Context, sc repo.Scope, actorID, id string) error {
	n, err := s.store.RestorePages(ctx, sc, actorID, id)
	if errors.Is(err, repo.ErrConflict) || errors.Is(err, repo.ErrHidden) {
		return s.unavailable(ctx, sc, "restore", id)
	}
	if err == nil {
		s.record(ctx, sc, audit.PageRestore, id, map[string]any{"pages": n})
	}
	return err
}

// DiffLine es una línea del diff: Op es "=", "+" o "-". Parts va solo en una línea cambiada que
// tiene pareja (la borrada y la que la reemplaza) y marca qué parte de ella cambió.
type DiffLine struct {
	Op    string     `json:"op"`
	Text  string     `json:"text"`
	Parts []DiffPart `json:"parts,omitempty"`
}

// DiffPart es un tramo de una línea cambiada; Changed es lo que se borró o se agregó.
type DiffPart struct {
	Text    string `json:"text"`
	Changed bool   `json:"changed,omitempty"`
}

var diffOps = map[diffmatchpatch.Operation]string{
	diffmatchpatch.DiffEqual: "=", diffmatchpatch.DiffInsert: "+", diffmatchpatch.DiffDelete: "-",
}

// Diff compara por líneas y resalta lo que cambió dentro de cada línea (SPEC §4.3).
func Diff(from, to string) []DiffLine {
	dmp := diffmatchpatch.New()
	a, b, lines := dmp.DiffLinesToChars(from, to)
	diffs := dmp.DiffCharsToLines(dmp.DiffMain(a, b, false), lines)

	var out []DiffLine
	for _, d := range diffs {
		op := diffOps[d.Type]
		for _, line := range strings.SplitAfter(d.Text, "\n") {
			if line != "" {
				out = append(out, DiffLine{Op: op, Text: strings.TrimSuffix(line, "\n")})
			}
		}
	}
	withinLines(dmp, out)
	return out
}

// wordRe corta una línea en palabras y en tramos de lo que no es palabra (espacios, signos).
var wordRe = regexp.MustCompile(`[\p{L}\p{N}_]+|[^\p{L}\p{N}_]+`)

// withinLines empareja cada bloque de líneas borradas con las agregadas que lo siguen (la
// primera con la primera, y así) y marca en cada par solo los caracteres que cambiaron.
func withinLines(dmp *diffmatchpatch.DiffMatchPatch, lines []DiffLine) {
	for i := 0; i < len(lines); {
		del := i
		for i < len(lines) && lines[i].Op == "-" {
			i++
		}
		ins := i
		for i < len(lines) && lines[i].Op == "+" {
			i++
		}
		for k := 0; del+k < ins && ins+k < i; k++ {
			lines[del+k].Parts, lines[ins+k].Parts = lineParts(dmp, lines[del+k].Text, lines[ins+k].Text)
		}
		if i == del {
			i++
		}
	}
}

// lineParts devuelve los tramos de la línea vieja y de la nueva, por palabras completas: por
// caracteres, "falla" → "conecta" marcaría "fall" y dejaría la "a" igual. Si no tienen nada en
// común, marcar toda la línea no aporta: no devuelve tramos.
func lineParts(dmp *diffmatchpatch.DiffMatchPatch, from, to string) (old, cur []DiffPart) {
	// Cada palabra (o tramo de separadores) pasa a ser un solo carácter, como hace
	// DiffLinesToChars con las líneas; así el diff no corta palabras.
	var tokens []string
	ids := map[string]rune{}
	encode := func(s string) string {
		var b strings.Builder
		for _, tok := range wordRe.FindAllString(s, -1) {
			id, ok := ids[tok]
			if !ok {
				id = rune(0xE000 + len(tokens)) // área de uso privado: no choca con el texto
				ids[tok] = id
				tokens = append(tokens, tok)
			}
			b.WriteRune(id)
		}
		return b.String()
	}
	a, b := encode(from), encode(to)
	decode := func(s string) string {
		var out strings.Builder
		for _, r := range s {
			out.WriteString(tokens[r-0xE000])
		}
		return out.String()
	}
	common := false
	for _, d := range dmp.DiffMain(a, b, false) {
		d.Text = decode(d.Text)
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			common = common || strings.TrimSpace(d.Text) != ""
			old = append(old, DiffPart{Text: d.Text})
			cur = append(cur, DiffPart{Text: d.Text})
		case diffmatchpatch.DiffDelete:
			old = append(old, DiffPart{Text: d.Text, Changed: true})
		case diffmatchpatch.DiffInsert:
			cur = append(cur, DiffPart{Text: d.Text, Changed: true})
		}
	}
	if !common {
		return nil, nil
	}
	return old, cur
}
