package wiki

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/athomo001/kudawiki/internal/search"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

var (
	ErrInvalidTags      = errors.New("invalid_tags")
	ErrInvalidMove      = errors.New("invalid_move")
	ErrInvalidLevel     = errors.New("invalid_level")
	ErrLevelBelowParent = errors.New("level_below_parent")
)

const maxTags = 20

// Una etiqueta: letras, números, espacio, "-" y "_", de 1 a 30 caracteres.
var tagRe = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N} _-]*$`)

// Links devuelve las rutas a las que enlaza el contenido con [[…]] (SPEC §4.7). Sale del
// mismo parser que el render: un [[x]] dentro de un bloque de código no es un enlace.
func Links(markdown string) []string {
	src := []byte(markdown)
	pc := parser.NewContext()
	pc.Set(optionsKey, &Options{})
	doc := md.Parser().Parse(text.NewReader(src), parser.WithContext(pc))
	seen := map[string]bool{}
	var out []string
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if t, ok := n.(*wikiToken); ok && entering {
			if target := t.target(); target != "" && !seen[target] {
				seen[target] = true
				out = append(out, target)
			}
		}
		return ast.WalkContinue, nil
	})
	return out
}

// linkRe encuentra cada [[ruta]] o [[ruta|texto]].
var linkRe = regexp.MustCompile(`\[\[\s*([a-z0-9]+(?:[/-][a-z0-9]+)*)\s*(\||\]\])`)

// rewriteMoves cambia los [[enlaces]] a rutas movidas (moves: vieja → nueva) y conserva el
// texto del enlace. Solo toca rutas que se movieron: un enlace a una subpágina que se quedó
// en su lugar (por oculta) no cambia. Opera sobre el texto: también dentro de un bloque de código.
func rewriteMoves(markdown string, moves map[string]string) string {
	return linkRe.ReplaceAllStringFunc(markdown, func(m string) string {
		sub := linkRe.FindStringSubmatch(m)
		if to, ok := moves[sub[1]]; ok {
			return "[[" + to + sub[2]
		}
		return m
	})
}

// CleanTags quita vacías y repetidas (sin contar mayúsculas) y valida las que quedan (SPEC §4.6).
// La usan las páginas y las plantillas, que traen etiquetas por defecto (§4.9).
func CleanTags(tags []string) ([]string, error) {
	clean := make([]string, 0, len(tags))
	seen := map[string]bool{}
	for _, t := range tags {
		t = strings.TrimSpace(t)
		key := strings.ToLower(t)
		if t == "" || seen[key] {
			continue
		}
		if !tagRe.MatchString(t) || utf8.RuneCountInString(t) > 30 {
			return nil, ErrInvalidTags
		}
		seen[key] = true
		clean = append(clean, t)
	}
	if len(clean) > maxTags {
		return nil, ErrInvalidTags
	}
	return clean, nil
}

// SetTags reemplaza las etiquetas de la página (SPEC §4.6).
func (s *Service) SetTags(ctx context.Context, sc repo.Scope, slug string, tags []string) error {
	clean, err := CleanTags(tags)
	if err != nil {
		return err
	}
	if err := s.direct(ctx, sc, slug); err != nil {
		return err
	}
	p, err := s.store.PageBySlug(ctx, sc, slug)
	if err != nil {
		return err
	}
	if err := s.store.SetTags(ctx, sc, p.ID, clean); err != nil {
		return err
	}
	s.record(ctx, sc, audit.PageUpdate, p.ID, map[string]any{"fields": []string{"tags"}})
	return nil
}

// Move mueve la página y sus subpáginas a otra ruta: deja alias en las rutas viejas y
// reescribe los enlaces que apuntaban a ellas, en una sola transacción (SPEC §4.1, §4.7).
func (s *Service) Move(ctx context.Context, sc repo.Scope, actorID, from, to string) error {
	if !ValidSlug(to) || to == from || strings.HasPrefix(to, from+"/") {
		return ErrInvalidMove
	}
	if err := s.direct(ctx, sc, from, to); err != nil {
		return err
	}
	id, n, err := s.store.MovePages(ctx, sc, actorID, from, to, func(title, description, markdown, lang string, moves map[string]string) (string, []string, repo.IndexDoc) {
		out := rewriteMoves(markdown, moves)
		return out, Links(out), IndexDoc(title, description, out, lang)
	})
	if errors.Is(err, repo.ErrConflict) || errors.Is(err, repo.ErrHidden) {
		return s.unavailable(ctx, sc, "move", "")
	}
	if err == nil {
		s.record(ctx, sc, audit.PageMove, id, map[string]any{"pages": n})
	}
	return err
}

// SetLevel cambia el nivel de lectura (§3.2): nadie sube una página sobre su propio nivel ni
// la deja bajo el de su padre; las subpáginas que quedarían por debajo suben con ella.
func (s *Service) SetLevel(ctx context.Context, sc repo.Scope, actorID, slug string, level int) error {
	if level < 1 || level > 3 || level > sc.MaxRank {
		return ErrInvalidLevel
	}
	if err := s.direct(ctx, sc, slug); err != nil {
		return err
	}
	id, from, err := s.store.SetLevel(ctx, sc, actorID, slug, level)
	if errors.Is(err, repo.ErrBelowParent) {
		return ErrLevelBelowParent
	}
	if err == nil && from != level {
		s.record(ctx, sc, audit.PageLevelChange, id, map[string]any{"from": from, "to": level})
	}
	return err
}

// maxDescription: la descripción es una línea para listados y búsquedas, no un resumen largo.
const maxDescription = 200

// SetInfo cambia la descripción corta y el ícono de la página (§4.1); lo que viene en nil queda
// como estaba. Son metadatos, como las etiquetas: no crean revisión. La descripción va también
// al índice de búsqueda, con su raíz.
func (s *Service) SetInfo(ctx context.Context, sc repo.Scope, slug string, description, icon *string) error {
	if err := s.direct(ctx, sc, slug); err != nil {
		return err
	}
	p, err := s.store.PageBySlug(ctx, sc, slug)
	if err != nil {
		return err
	}
	if description != nil {
		p.Description = strings.TrimSpace(*description)
	}
	if icon != nil {
		p.Icon = strings.TrimSpace(*icon)
		if *icon != p.Icon { // espacios alrededor: no es un emoji
			return ErrInvalidInfo
		}
	}
	if utf8.RuneCountInString(p.Description) > maxDescription || !validIcon(p.Icon) {
		return ErrInvalidInfo
	}
	return s.store.SetPageInfo(ctx, sc, p.ID, p.Description, search.Stem(p.Description, p.Lang), p.Icon)
}

// validIcon acepta vacío o un emoji, también compuesto (👩‍💻, banderas): solo símbolos y sus
// modificadores, nunca letras, números ni espacios, y como mucho 8 caracteres.
func validIcon(icon string) bool {
	if utf8.RuneCountInString(icon) > 8 {
		return false
	}
	for _, r := range icon {
		if !unicode.In(r, unicode.So, unicode.Sk, unicode.Mn, unicode.Me, unicode.Cf) {
			return false
		}
	}
	return true
}
