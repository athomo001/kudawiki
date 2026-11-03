package wikijs

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/athomo001/kudawiki/internal/areaio"
	"github.com/athomo001/kudawiki/internal/wiki"
	"go.yaml.in/yaml/v3"
)

// Rule lleva un prefijo de ruta de Wiki.js a un área: "ti/**" → ti. La ruta de la página en el
// área es lo que sigue al prefijo; "**" toma todo lo que no tomó una regla anterior.
type Rule struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

// Mapping es el archivo YAML del mapeo:
//
//	mappings:
//	  - from: ti/**
//	    to: ti
//	  - from: rrhh/**
//	    to: rrhh
type Mapping struct {
	Rules []Rule `yaml:"mappings"`
}

// ParseMapping lee y valida el mapeo. Gana la primera regla que calza.
func ParseMapping(b []byte) (Mapping, error) {
	var m Mapping
	if err := yaml.Unmarshal(b, &m); err != nil {
		return m, err
	}
	if len(m.Rules) == 0 {
		return m, errors.New("the mapping has no rules (mappings: - from: ti/** to: ti)")
	}
	for i, r := range m.Rules {
		from := strings.Trim(r.From, "/")
		if from != "**" && (!strings.HasSuffix(from, "/**") || strings.Contains(strings.TrimSuffix(from, "/**"), "*")) {
			return m, fmt.Errorf("rule %d: %q must be a path prefix ending in /** or just **", i+1, r.From)
		}
		if !wiki.ValidSlug(r.To) {
			return m, fmt.Errorf("rule %d: %q is not an area", i+1, r.To)
		}
		m.Rules[i].From = from
	}
	return m, nil
}

// Map devuelve el área y la ruta de una página de Wiki.js, o ok=false si ninguna regla la toma.
// La página que está justo en el prefijo ("ti" para "ti/**") queda con el último tramo del
// prefijo como ruta: el área no tiene una página raíz.
func (m Mapping) Map(p string) (area, slug string, ok bool) {
	for _, r := range m.Rules {
		if r.From == "**" {
			return r.To, areaio.SlugFromPath(p), true
		}
		prefix := strings.TrimSuffix(r.From, "/**")
		if p == prefix {
			return r.To, areaio.SlugFromPath(prefix[strings.LastIndex(prefix, "/")+1:]), true
		}
		if rest, found := strings.CutPrefix(p, prefix+"/"); found {
			return r.To, areaio.SlugFromPath(rest), true
		}
	}
	return "", "", false
}

// Plan es lo que se importaría: las páginas de cada área, ya convertidas, lo que no se importa y
// los avisos de conversión.
type Plan struct {
	Areas    map[string][]areaio.Page
	Skipped  []areaio.Skip
	Warnings []areaio.Warning
}

// AreaNames son las áreas del plan, en orden.
func (p Plan) AreaNames() []string {
	out := make([]string, 0, len(p.Areas))
	for a := range p.Areas {
		out = append(out, a)
	}
	slices.Sort(out)
	return out
}

// Build convierte las páginas de Wiki.js según el mapeo. Una página sin publicar se importa con
// nivel 3 (solo admins): en Wiki.js no la veía cualquiera, y aquí se decide después quién sí.
func Build(pages []Page, m Mapping) Plan {
	plan := Plan{Areas: map[string][]areaio.Page{}}
	seen := map[string]string{}
	for _, pg := range pages {
		file := pg.Path + ".md"
		area, slug, ok := m.Map(pg.Path)
		if !ok {
			plan.Skipped = append(plan.Skipped, areaio.Skip{Path: file, Reason: "unmapped"})
			continue
		}
		if !wiki.ValidSlug(slug) {
			plan.Skipped = append(plan.Skipped, areaio.Skip{Path: file, Reason: "invalid_path"})
			continue
		}
		key := area + "/" + slug
		if other, dup := seen[key]; dup {
			plan.Skipped = append(plan.Skipped, areaio.Skip{Path: file, Reason: "duplicate_path:" + other})
			continue
		}
		warn := func(reason, detail string) {
			plan.Warnings = append(plan.Warnings, areaio.Warning{Path: file, Reason: reason, Detail: detail})
		}
		md, err := convert(pg.Content, pg.HTML, pg.Locale)
		if err != nil {
			plan.Skipped = append(plan.Skipped, areaio.Skip{Path: file, Reason: "unreadable"})
			continue
		}
		seen[key] = file
		if pg.HTML {
			warn("html_converted", "")
		}
		for _, c := range UnsupportedClasses(md) {
			warn("unsupported_class", c)
		}
		out := areaio.Page{Path: file, Slug: slug, Markdown: md, Summary: "Wiki.js",
			FrontMatter: areaio.FrontMatter{Title: pg.Title, Description: pg.Description, Tags: pg.Tags, Lang: langOf(pg.Locale)}}
		if !pg.Published {
			out.Level = 3
			warn("unpublished", "")
		}
		for _, v := range pg.History {
			vmd, err := convert(v.Content, v.HTML, pg.Locale)
			if err != nil {
				continue
			}
			out.History = append(out.History, areaio.Version{Title: v.Title, Markdown: vmd, Summary: summary(v)})
		}
		plan.Areas[area] = append(plan.Areas[area], out)
	}
	for a := range plan.Areas {
		// De padres a hijas: cada página nace con el nivel de su padre ya puesto (§3.2).
		slices.SortFunc(plan.Areas[a], func(x, y areaio.Page) int { return strings.Compare(x.Slug, y.Slug) })
	}
	return plan
}

func summary(v Version) string {
	parts := []string{"Wiki.js"}
	for _, s := range []string{v.Author, v.At} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " · ")
}

func langOf(locale string) string {
	if l, _, _ := strings.Cut(locale, "-"); l == "es" || l == "en" {
		return l
	}
	return ""
}

func convert(content string, isHTML bool, locale string) (string, error) {
	md := content
	if isHTML {
		var err error
		if md, err = HTMLToMarkdown(content); err != nil {
			return "", err
		}
	}
	return stripLocale(ConvertTabsets(md), locale), nil
}

// stripLocale quita el idioma del comienzo de los enlaces ("/es/ti/red" → "/ti/red"): con los
// espacios de nombres por idioma de Wiki.js las rutas lo llevan, pero las páginas no.
func stripLocale(md, locale string) string {
	if locale == "" {
		return md
	}
	re := regexp.MustCompile(`\]\(/` + regexp.QuoteMeta(locale) + `/`)
	return re.ReplaceAllString(md, "](/")
}

// Fingerprint resume el plan: la importación real exige que sea la misma que la del último
// --dry-run, así lo que se importa es lo que se revisó (§13.8).
func (p Plan) Fingerprint() string {
	h := sha256.New()
	for _, a := range p.AreaNames() {
		for _, pg := range p.Areas[a] {
			fmt.Fprintf(h, "%s\x00%s\x00%d\x00%d\x00", a, pg.Slug, pg.Level, len(pg.History))
			h.Write([]byte(pg.Markdown))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}
