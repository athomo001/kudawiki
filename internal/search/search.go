// Package search arma las consultas de la búsqueda (SPEC §9): raíces de palabras y la
// traducción de lo que escribe la persona a una expresión FTS5 segura.
package search

import (
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/kljensen/snowball"
)

var ErrEmptyQuery = errors.New("empty_query")

var stemmers = map[string]string{"es": "spanish", "en": "english"}

// Stem reduce cada palabra a su raíz (procedimientos → proced) en el idioma dado (§9.1).
func Stem(text, lang string) string {
	language := stemmers[lang]
	words := words(text)
	for i, w := range words {
		if language != "" {
			if s, err := snowball.Stem(w, language, true); err == nil && s != "" {
				words[i] = s
			}
		}
	}
	return strings.Join(words, " ")
}

func words(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
}

// fts escribe un término como cadena FTS5: entre comillas y con las comillas duplicadas, así
// nada de lo que escribe la persona se interpreta como operador (§9.3).
func fts(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

const (
	plainCols = `{title tags headings description content_text code_text}`
	stemCols  = `{title_stem headings_stem description_stem content_stem}`
)

// Parse traduce la caja de búsqueda (§9.3) a una consulta para repo. Palabras: en cualquier
// campo, con prefijo, y por su raíz en español o inglés. "Frase" exacta, -palabra excluye,
// OR une, y los filtros tag:, path:, autor:/author:, desde:/since: y estado:/status: acotan.
func Parse(input string) (repo.SearchQuery, error) {
	var q repo.SearchQuery
	var pos, neg []string
	or := false
	var plain []string

	for _, tok := range tokenize(input) {
		key, val, isFilter := strings.Cut(tok.text, ":")
		switch {
		case tok.quoted:
			if p := strings.Join(words(tok.text), " "); p != "" {
				pos, or = join(pos, plainCols+` : `+fts(p), or), false
				plain = append(plain, p)
			}
			continue
		case tok.text == "OR":
			or = len(pos) > 0
			continue
		case isFilter && val != "":
			if applyFilter(&q, strings.ToLower(key), val) {
				continue
			}
		}
		negate := strings.HasPrefix(tok.text, "-")
		for _, w := range words(strings.TrimPrefix(tok.text, "-")) {
			if utf8.RuneCountInString(w) < 2 { // un prefijo de una letra no se busca (§9.5)
				continue
			}
			group := `(` + plainCols + ` : ` + fts(w) + `* OR ` + stemCols + ` : (` + fts(Stem(w, "es")) + ` OR ` + fts(Stem(w, "en")) + `))`
			if negate {
				neg = append(neg, group)
				continue
			}
			pos, or = join(pos, group, or), false
			plain = append(plain, w)
		}
	}
	if len(pos) == 0 {
		// Solo filtros (tag:vpn) es una búsqueda válida. Sin términos positivos FTS5 no puede
		// excluir, así que las exclusiones solas no buscan nada.
		if len(q.Tags) == 0 && q.Path == "" && q.Author == "" && q.Since.IsZero() && !q.Overdue {
			return repo.SearchQuery{}, ErrEmptyQuery
		}
		return q, nil
	}
	// AND explícito: FTS5 no acepta el AND implícito entre grupos con paréntesis.
	q.Match = strings.Join(pos, " AND ")
	for _, n := range neg {
		q.Match = `(` + q.Match + `) NOT ` + n
	}
	q.Words = plain
	return q, nil
}

// join agrega el grupo con AND (implícito) o con OR al anterior.
func join(groups []string, g string, or bool) []string {
	if or && len(groups) > 0 {
		groups[len(groups)-1] = `(` + groups[len(groups)-1] + ` OR ` + g + `)`
		return groups
	}
	return append(groups, g)
}

type token struct {
	text   string
	quoted bool
}

// tokenize separa por espacios respetando las "frases"; una comilla sin cerrar abarca hasta el final.
func tokenize(s string) []token {
	var out []token
	for s = strings.TrimSpace(s); s != ""; s = strings.TrimSpace(s) {
		if s[0] == '"' {
			end := strings.IndexByte(s[1:], '"')
			if end < 0 {
				out, s = append(out, token{s[1:], true}), ""
				continue
			}
			out, s = append(out, token{s[1 : end+1], true}), s[end+2:]
			continue
		}
		end := strings.IndexFunc(s, unicode.IsSpace)
		if end < 0 {
			end = len(s)
		}
		// clave:"valor con espacios" es un solo filtro (autor:"Ana Soto", tag:"mesa de ayuda").
		if key, _, ok := strings.Cut(s[:end], `:"`); ok && key != "" && !strings.ContainsAny(key, `"`) {
			value := s[len(key)+2:]
			quote := strings.IndexByte(value, '"')
			if quote < 0 {
				out, s = append(out, token{text: key + ":" + value}), ""
				continue
			}
			out, s = append(out, token{text: key + ":" + value[:quote]}), value[quote+1:]
			continue
		}
		out, s = append(out, token{text: s[:end]}), s[end:]
	}
	return out
}

// applyFilter interpreta key:valor; si no es un filtro conocido, el token se busca como texto.
// Los nombres valen en español y en inglés (§9.3).
func applyFilter(q *repo.SearchQuery, key, val string) bool {
	switch key {
	case "tag":
		if rest, ok := strings.CutPrefix(val, "any:"); ok {
			q.TagsAny, val = true, rest
		}
		for _, t := range strings.Split(val, ",") {
			if t = strings.TrimSpace(t); t != "" {
				q.Tags = append(q.Tags, t)
			}
		}
	case "path":
		q.Path = strings.Trim(strings.ToLower(val), "/")
	case "autor", "author":
		q.Author = val
	case "desde", "since":
		t, err := time.Parse("2006-01-02", val)
		if err != nil {
			return false
		}
		q.Since = t
	case "estado", "status":
		// Por ahora el único estado es la vigencia vencida (§4.8).
		if v := strings.ToLower(val); v != "vencida" && v != "overdue" {
			return false
		}
		q.Overdue = true
	default:
		return false
	}
	return true
}
