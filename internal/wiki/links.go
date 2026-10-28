package wiki

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/repo"
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

// SetTags reemplaza las etiquetas de la página (SPEC §4.6).
func (s *Service) SetTags(ctx context.Context, sc repo.Scope, slug string, tags []string) error {
	clean := make([]string, 0, len(tags))
	seen := map[string]bool{}
	for _, t := range tags {
		t = strings.TrimSpace(t)
		key := strings.ToLower(t)
		if t == "" || seen[key] {
			continue
		}
		if !tagRe.MatchString(t) || utf8.RuneCountInString(t) > 30 {
			return ErrInvalidTags
		}
		seen[key] = true
		clean = append(clean, t)
	}
	if len(clean) > maxTags {
		return ErrInvalidTags
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
	id, n, err := s.store.MovePages(ctx, sc, actorID, from, to, func(title, markdown, lang string, moves map[string]string) (string, []string, repo.IndexDoc) {
		out := rewriteMoves(markdown, moves)
		return out, Links(out), IndexDoc(title, out, lang)
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
	id, from, err := s.store.SetLevel(ctx, sc, actorID, slug, level)
	if errors.Is(err, repo.ErrBelowParent) {
		return ErrLevelBelowParent
	}
	if err == nil && from != level {
		s.record(ctx, sc, audit.PageLevelChange, id, map[string]any{"from": from, "to": level})
	}
	return err
}
