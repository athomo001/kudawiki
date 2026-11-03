// Package areaio exporta e importa un área como Markdown (SPEC §13.8): un ZIP con un .md por
// página (front matter YAML), su media y un manifest.json. Lo que sale respeta el nivel de quien
// exporta, y los sellados nunca salen, ni cifrados (§6). Lo que entra se crea como revisión 1 con
// las mismas reglas que una página escrita a mano.
package areaio

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/athomo001/kudawiki/internal/repo"
	"go.yaml.in/yaml/v3"
)

// Format identifica el ZIP: el importador reconoce el suyo y sabe que las páginas están en pages/.
const Format = "kudawiki-area-export"

type exportStore interface {
	ExportPages(ctx context.Context, s repo.Scope) ([]repo.ExportPage, error)
	ExportMedia(ctx context.Context, s repo.Scope) ([]repo.Media, error)
}

// opener abre el archivo de una media, con el mismo control de nivel que al servirla.
type opener interface {
	Open(ctx context.Context, sc repo.Scope, id string, thumb bool) (repo.Media, *os.File, error)
}

// FrontMatter es la cabecera de cada .md. Los mismos campos se leen al importar.
type FrontMatter struct {
	Title       string   `yaml:"title"`
	Description string   `yaml:"description,omitempty"`
	Tags        []string `yaml:"tags,omitempty"`
	Level       int      `yaml:"level,omitempty"`
	Owner       string   `yaml:"owner,omitempty"`
	Lang        string   `yaml:"lang,omitempty"`
}

// Manifest es manifest.json.
type Manifest struct {
	Format     string    `json:"format"`
	Version    int       `json:"version"`
	Area       string    `json:"area"`
	Name       string    `json:"name"`
	ExportedAt time.Time `json:"exported_at"`
	Pages      int       `json:"pages"`
	Media      int       `json:"media"`
}

var mediaURLRe = regexp.MustCompile(`/api/w/[a-z0-9~-]+/media/([0-9A-HJKMNP-TV-Z]{26})`)

// Export escribe el ZIP del área en w. sc es el Scope de quien exporta: lo que su rango no ve, no
// sale. La media va como media/<id>-<nombre> y los enlaces del contenido apuntan ahí con una ruta
// relativa, así el ZIP se lee también fuera de Kudawiki.
func Export(ctx context.Context, st exportStore, files opener, sc repo.Scope, area repo.Workspace, w io.Writer, now time.Time) (Manifest, error) {
	m := Manifest{Format: Format, Version: 1, Area: area.Slug, Name: area.Name, ExportedAt: now.UTC()}
	pages, err := st.ExportPages(ctx, sc)
	if err != nil {
		return m, err
	}
	media, err := st.ExportMedia(ctx, sc)
	if err != nil {
		return m, err
	}
	zw := zip.NewWriter(w)
	names := map[string]string{} // id → ruta dentro del ZIP
	for _, md := range media {
		name := "media/" + md.ID + "-" + safeName(md.Filename)
		_, f, err := files.Open(ctx, sc, md.ID, false)
		if err != nil {
			continue // borrada mientras tanto: la salud del área lo muestra
		}
		dst, err := zw.Create(name)
		if err == nil {
			_, err = io.Copy(dst, f)
		}
		f.Close()
		if err != nil {
			return m, err
		}
		names[md.ID] = name
		m.Media++
	}
	for _, p := range pages {
		file := "pages/" + p.Slug + ".md"
		up := strings.Repeat("../", strings.Count(file, "/"))
		body := mediaURLRe.ReplaceAllStringFunc(p.Markdown, func(u string) string {
			if name, ok := names[mediaURLRe.FindStringSubmatch(u)[1]]; ok {
				return up + name
			}
			return u
		})
		fm, err := yaml.Marshal(FrontMatter{Title: p.Title, Description: p.Description, Tags: p.Tags, Level: p.Level, Owner: p.Owner, Lang: p.Lang})
		if err != nil {
			return m, err
		}
		dst, err := zw.Create(file)
		if err != nil {
			return m, err
		}
		if _, err := io.WriteString(dst, "---\n"+string(fm)+"---\n\n"+body); err != nil {
			return m, err
		}
		m.Pages++
	}
	mj, _ := json.MarshalIndent(m, "", "  ")
	dst, err := zw.Create("manifest.json")
	if err != nil {
		return m, err
	}
	if _, err := dst.Write(mj); err != nil {
		return m, err
	}
	return m, zw.Close()
}

// safeName deja un nombre de archivo que ningún sistema rechaza ni puede usar para salirse de
// la carpeta.
func safeName(name string) string {
	name = path.Base(strings.ReplaceAll(name, `\`, "/"))
	var b bytes.Buffer
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if s := strings.Trim(b.String(), "."); s != "" {
		return s
	}
	return "archivo"
}
