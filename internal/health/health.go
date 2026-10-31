// Package health es la salud del contenido (SPEC §13.7): chequeos que encuentran lo que está
// roto para quien lee (error) o desordenado (warning). Solo lee; los arreglos los hace
// `kudawiki doctor --fix`. Los sellados se suman con su fase (F7).
package health

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/athomo001/kudawiki/internal/wiki"
)

type Severity string

const (
	Error   Severity = "error"   // roto para quien lee
	Warning Severity = "warning" // desordenado
)

// Problem es un hallazgo. Check es un código estable (la interfaz lo traduce); Page es la
// ruta afectada, si la hay; Detail, el dato concreto (la ruta rota, el id del archivo).
type Problem struct {
	Check    string   `json:"check"`
	Severity Severity `json:"severity"`
	Area     string   `json:"area,omitempty"`
	Page     string   `json:"page,omitempty"`
	Detail   string   `json:"detail,omitempty"`
}

// unattachedGrace: la media sin página va sola a la papelera a los 7 días (§4.4); pasado un día
// más, es que la purga diaria no está corriendo.
const unattachedGrace = 8 * 24 * time.Hour

// store es lo que health usa de repo, declarado aquí (SPEC §16.2).
type store interface {
	Tree(ctx context.Context, s repo.Scope) ([]repo.PageRef, error)
	BrokenLinks(ctx context.Context, s repo.Scope) ([]repo.BrokenLink, error)
	ShadowedAliases(ctx context.Context, s repo.Scope) ([]repo.ShadowedAlias, error)
	OrphanPages(ctx context.Context, s repo.Scope) ([]string, error)
	PagesListingChildren(ctx context.Context, s repo.Scope) ([]string, error)
	MediaRows(ctx context.Context, s repo.Scope, unattachedBefore time.Time) ([]repo.MediaRow, error)
	PagesWithoutIndex(ctx context.Context, s repo.Scope) ([]string, error)
	OrphanIndexRows(ctx context.Context) (int, error)
	Proposals(ctx context.Context, s repo.Scope, f repo.ProposalFilter) ([]repo.Proposal, error)
	ProposalByID(ctx context.Context, s repo.Scope, id string) (repo.Proposal, error)
	PageBySlug(ctx context.Context, s repo.Scope, slug string) (repo.Page, error)
	RevisionByNumber(ctx context.Context, s repo.Scope, pageID string, n int) (repo.Revision, error)
	ReviewProblems(ctx context.Context, s repo.Scope, now time.Time) ([]repo.ReviewProblem, error)
}

type Options struct {
	StorageDir string
	Areas      []repo.Workspace
	// Global suma lo que no es de ningún área (filas del índice sin página): doctor y el superadmin.
	Global bool
	// Hash lee cada archivo para comparar su SHA-256: con muchos GB tarda minutos, así que lo
	// hace doctor y no la pantalla, que compara el tamaño.
	Hash bool
	Now  time.Time
	// RemindAfter es approval.remind_after_days: el mismo plazo del recordatorio al aprobador y
	// del "Esperando hace N días" de la bandeja (§5.3), para que la salud no tenga otro.
	RemindAfter time.Duration
}

// Check corre todos los chequeos sobre las áreas dadas, con el rango de admin: la salud la
// ven el admin del área y el superadmin (§13.7).
func Check(ctx context.Context, st store, o Options) ([]Problem, error) {
	var out []Problem
	for _, w := range o.Areas {
		ps, err := area(ctx, st, o, w)
		if err != nil {
			return nil, fmt.Errorf("area %s: %w", w.Slug, err)
		}
		out = append(out, ps...)
	}
	if o.Global {
		n, err := st.OrphanIndexRows(ctx)
		if err != nil {
			return nil, err
		}
		if n > 0 {
			out = append(out, Problem{Check: "search.orphan_index_rows", Severity: Error, Detail: fmt.Sprint(n)})
		}
	}
	return out, nil
}

func area(ctx context.Context, st store, o Options, w repo.Workspace) ([]Problem, error) {
	sc := repo.Scope{WsID: w.ID, MaxRank: 3}
	var out []Problem
	add := func(check string, sev Severity, page, detail string) {
		out = append(out, Problem{Check: check, Severity: sev, Area: w.Slug, Page: page, Detail: detail})
	}

	links, err := st.BrokenLinks(ctx, sc)
	if err != nil {
		return nil, err
	}
	for _, l := range links {
		add("links.broken", Error, l.FromSlug, l.ToSlug)
	}
	aliases, err := st.ShadowedAliases(ctx, sc)
	if err != nil {
		return nil, err
	}
	for _, a := range aliases {
		add("links.alias_shadowed", Warning, a.PageSlug, a.OldSlug)
	}

	// Árbol: en Go, con el árbol del área en un mapa; en SQL, buscar los padres de cada página
	// con LIKE recorrería el área una vez por página.
	refs, err := st.Tree(ctx, sc)
	if err != nil {
		return nil, err
	}
	rank := make(map[string]int, len(refs))
	for _, r := range refs {
		rank[r.Slug] = r.MinRank
	}
	for _, r := range refs {
		if !wiki.ValidSlug(r.Slug) {
			add("tree.invalid_slug", Warning, r.Slug, "")
			continue
		}
		parts := strings.Split(r.Slug, "/")
		for i := len(parts) - 1; i > 0; i-- {
			parent := strings.Join(parts[:i], "/")
			if pr, ok := rank[parent]; ok && pr > r.MinRank {
				add("tree.level_below_parent", Error, r.Slug, parent) // §3.2: no debería poder pasar
				break
			}
		}
	}
	orphans, err := st.OrphanPages(ctx, sc)
	if err != nil {
		return nil, err
	}
	listing, err := st.PagesListingChildren(ctx, sc)
	if err != nil {
		return nil, err
	}
	for _, slug := range orphans {
		// Una página de primer nivel se ve desde el inicio del área, y una cuyo padre lista sus
		// hijas con [[children]] aparece en él; una dentro de una carpeta a la que nadie enlaza
		// ni lista es la que se olvida.
		i := strings.LastIndex(slug, "/")
		if i > 0 && !slices.Contains(listing, slug[:i]) {
			add("tree.orphan", Warning, slug, "")
		}
	}

	if err := media(ctx, st, o, sc, add); err != nil {
		return nil, err
	}

	missing, err := st.PagesWithoutIndex(ctx, sc)
	if err != nil {
		return nil, err
	}
	for _, slug := range missing {
		add("search.page_not_indexed", Warning, slug, "")
	}

	if err := proposalChecks(ctx, st, o, sc, add); err != nil {
		return nil, err
	}
	if err := reviewChecks(ctx, st, o, sc, add); err != nil {
		return nil, err
	}
	return out, nil
}

// reviewChecks es la vigencia (§4.8): páginas vencidas, y páginas sin dueño (o con uno que ya
// no puede editarlas en el área) o con el dueño desactivado. Esas no avisan a nadie al vencer.
func reviewChecks(ctx context.Context, st store, o Options, sc repo.Scope, add func(check string, sev Severity, page, detail string)) error {
	problems, err := st.ReviewProblems(ctx, sc, o.Now)
	if err != nil {
		return err
	}
	for _, p := range problems {
		if p.Overdue {
			add("review.overdue", Warning, p.Slug, "")
		}
		switch p.Owner {
		case "none":
			add("review.no_owner", Warning, p.Slug, "")
		case "inactive":
			add("review.owner_inactive", Warning, p.Slug, "")
		}
	}
	return nil
}

// proposalChecks encuentra las pendientes que pasaron el plazo de revisión y las que chocan con la
// versión actual de su página, que alguien tiene que rehacer (§5.2, §13.7). Detail es el id de
// la propuesta: la pantalla enlaza a ella, no a la página, que puede no existir todavía.
func proposalChecks(ctx context.Context, st store, o Options, sc repo.Scope, add func(check string, sev Severity, page, detail string)) error {
	// Todas: un límite haría que la salud callara justo cuando hay muchas esperando.
	pending, err := st.Proposals(ctx, sc, repo.ProposalFilter{Status: []string{"pending"}, Limit: math.MaxInt32})
	if err != nil {
		return err
	}
	for _, p := range pending {
		if o.Now.Sub(p.CreatedAt) > o.RemindAfter {
			add("proposals.waiting", Warning, p.Slug, p.ID)
		}
		// Solo choca si la página avanzó desde la base; el listado no trae el contenido.
		if p.PageID == "" || p.Current <= p.Base {
			continue
		}
		conflict, err := conflicts(ctx, st, sc, p.ID)
		if err != nil {
			return err
		}
		if conflict {
			add("proposals.conflict", Warning, p.Slug, p.ID)
		}
	}
	return nil
}

// conflicts mezcla la propuesta con la versión actual como lo hace la bandeja (wiki.MergePage).
func conflicts(ctx context.Context, st store, sc repo.Scope, id string) (bool, error) {
	p, err := st.ProposalByID(ctx, sc, id)
	if err != nil {
		return false, err
	}
	page, err := st.PageBySlug(ctx, sc, p.Slug)
	if err != nil {
		return false, err
	}
	base, err := st.RevisionByNumber(ctx, sc, page.ID, p.Base)
	if err != nil {
		return false, err
	}
	_, _, err = wiki.MergePage(base.Title, base.Markdown, page.Title, page.Markdown, p.Title, p.Markdown)
	if errors.Is(err, wiki.ErrMergeConflict) {
		return true, nil
	}
	return false, err
}

// media compara las filas con el disco: storage/ws_<área>/<id> y <id>.thumb (§4.4).
func media(ctx context.Context, st store, o Options, sc repo.Scope, add func(check string, sev Severity, page, detail string)) error {
	rows, err := st.MediaRows(ctx, sc, o.Now.Add(-unattachedGrace))
	if err != nil {
		return err
	}
	dir := filepath.Join(o.StorageDir, "ws_"+sc.WsID)
	known := map[string]bool{}
	for _, m := range rows {
		known[m.ID] = true
		if m.HasThumbnail {
			known[m.ID+".thumb"] = true
		}
		path := filepath.Join(dir, m.ID)
		info, err := os.Stat(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			add("media.missing_file", Error, "", m.ID)
			continue
		case err != nil:
			return err
		case info.Size() != m.Size:
			add("media.sha_mismatch", Error, "", m.ID) // otro tamaño: el contenido no es el subido
			continue
		}
		if o.Hash {
			sum, err := fileSHA256(path)
			if err != nil {
				return err
			}
			if sum != m.SHA256 {
				add("media.sha_mismatch", Error, "", m.ID)
			}
		}
		if m.UnattachedOld {
			add("media.unattached", Warning, "", m.ID)
		}
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var stray []string
	for _, e := range entries {
		if !known[e.Name()] {
			stray = append(stray, e.Name())
		}
	}
	slices.Sort(stray)
	for _, name := range stray {
		add("media.stray_file", Warning, "", name)
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
