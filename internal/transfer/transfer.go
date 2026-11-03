// Package transfer es el traslado de páginas entre áreas (SPEC §4.13): la única forma de que
// contenido normal cruce de un área a otra. Solo el superadmin, con reautenticación (la ruta la
// exige) y siempre después de ver la vista previa: rutas y niveles en el destino, quién pierde y
// quién gana acceso y qué enlaces del origen quedan rotos.
package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/athomo001/kudawiki/internal/wiki"
	"github.com/oklog/ulid/v2"
)

var (
	// ErrInvalid: rutas mal formadas, un nivel que no existe o el mismo área de origen y destino.
	ErrInvalid = errors.New("invalid_transfer")
	// ErrTaken: alguna ruta del destino ya está ocupada; la vista previa dice cuáles.
	ErrTaken = errors.New("transfer_taken")
)

type store interface {
	WorkspaceByID(ctx context.Context, id string) (repo.Workspace, error)
	WorkspaceAliases(ctx context.Context, wsID string) ([]string, error)
	Subtree(ctx context.Context, s repo.Scope, root string) ([]repo.Page, error)
	SealedSubtree(ctx context.Context, s repo.Scope, root string) ([]repo.SealedInTree, error)
	AncestorRank(ctx context.Context, s repo.Scope, slug string) (int, error)
	TransferCheck(ctx context.Context, from, to repo.Scope, ids, newSlugs []string) (repo.TransferImpact, error)
	TransferPages(ctx context.Context, m repo.TransferMove) (repo.TransferResult, error)
	Members(ctx context.Context, s repo.Scope) ([]repo.Member, error)
	AddNotifications(ctx context.Context, wsID string, ns []repo.NewNotification) error
}

// files mueve los archivos de la media en el disco.
type files interface {
	MoveFiles(fromWs, toWs string, ids []string) error
}

type Service struct {
	store store
	files files
	audit *audit.Recorder
	log   *slog.Logger
}

func NewService(s store, f files, rec *audit.Recorder, log *slog.Logger) *Service {
	return &Service{store: s, files: f, audit: rec, log: log}
}

// Request es lo que pide el superadmin: la página (y su subárbol) del área From, a la ruta To del
// área Dest, con el nivel Level para todas (0: cada una conserva el suyo).
type Request struct {
	From, Slug, Dest, To string
	Level                int
}

// Plan es una página o sellado tal como queda.
type Plan struct {
	Slug, NewSlug   string
	Level, NewLevel int
}

// Preview es lo que se muestra antes de confirmar.
type Preview struct {
	From, Dest repo.Workspace
	Pages      []Plan
	Sealed     []Plan // sin nivel: un sellado tiene sus propias llaves
	Impact     repo.TransferImpact
	// Lose y Gain son personas (nombres) que pierden o ganan acceso a alguna de las páginas.
	Lose, Gain []string
}

const adminRank = 3

type plan struct {
	Preview
	pages  []repo.Page
	sealed []repo.SealedInTree
	from   repo.Scope
	to     repo.Scope
}

func (s *Service) plan(ctx context.Context, req Request) (plan, error) {
	var p plan
	if req.From == req.Dest || !wiki.ValidSlug(req.Slug) || !wiki.ValidSlug(req.To) || req.Level < 0 || req.Level > adminRank {
		return p, ErrInvalid
	}
	var err error
	for _, x := range []struct {
		id  string
		dst *repo.Workspace
	}{{req.From, &p.From}, {req.Dest, &p.Dest}} {
		w, err := s.store.WorkspaceByID(ctx, x.id)
		// Un área archivada no recibe ni entrega: es solo lectura (§3.5). Desactivada o por
		// borrar, el panel la ve, pero aquí da lo mismo que no exista.
		if err != nil || w.Kind != "area" || w.Status != "active" || w.DeleteAt != nil {
			return p, repo.ErrNotFound
		}
		*x.dst = w
	}
	p.from, p.to = repo.Scope{WsID: p.From.ID, MaxRank: adminRank}, repo.Scope{WsID: p.Dest.ID, MaxRank: adminRank}
	if p.pages, err = s.store.Subtree(ctx, p.from, req.Slug); err != nil {
		return p, err
	}
	if len(p.pages) == 0 || p.pages[0].Slug != req.Slug {
		return p, repo.ErrNotFound
	}
	if p.sealed, err = s.store.SealedSubtree(ctx, p.from, req.Slug); err != nil {
		return p, err
	}
	anc, err := s.store.AncestorRank(ctx, p.to, req.To)
	if err != nil {
		return p, err
	}
	moved := func(slug string) string { return req.To + strings.TrimPrefix(slug, req.Slug) }
	// El nivel de cada una: el suyo o el pedido, sin quedar debajo de su padre en el destino; el
	// subárbol va ordenado de padres a hijas, así cada hija ve el nivel ya calculado de su padre.
	levels := map[string]int{}
	ids := make([]string, len(p.pages))
	slugs := make([]string, 0, len(p.pages)+len(p.sealed))
	for i, pg := range p.pages {
		lvl := pg.MinRank
		if req.Level > 0 {
			lvl = req.Level
		}
		lvl = max(lvl, anc)
		if parent, ok := levels[parentOf(pg.Slug)]; ok {
			lvl = max(lvl, parent)
		}
		levels[pg.Slug] = lvl
		ids[i] = pg.ID
		slugs = append(slugs, moved(pg.Slug))
		p.Pages = append(p.Pages, Plan{Slug: pg.Slug, NewSlug: moved(pg.Slug), Level: pg.MinRank, NewLevel: lvl})
	}
	for _, sd := range p.sealed {
		slugs = append(slugs, moved(sd.Slug))
		p.Sealed = append(p.Sealed, Plan{Slug: sd.Slug, NewSlug: moved(sd.Slug)})
	}
	if p.Impact, err = s.store.TransferCheck(ctx, p.from, p.to, ids, slugs); err != nil {
		return p, err
	}
	p.Lose, p.Gain, err = s.access(ctx, p)
	return p, err
}

func parentOf(slug string) string {
	if i := strings.LastIndex(slug, "/"); i >= 0 {
		return slug[:i]
	}
	return ""
}

// access compara quién ve cada página antes y después (§3.4): en el origen, los miembros con
// rango para su nivel; en el destino, los del destino con rango para el nivel nuevo.
func (s *Service) access(ctx context.Context, p plan) (lose, gain []string, err error) {
	rank := func(sc repo.Scope) (map[string]int, map[string]string, error) {
		ms, err := s.store.Members(ctx, sc)
		ranks, names := map[string]int{}, map[string]string{}
		for _, m := range ms {
			ranks[m.UserID], names[m.UserID] = repo.RoleRank(m.Role), m.FullName
		}
		return ranks, names, err
	}
	src, srcNames, err := rank(p.from)
	if err != nil {
		return nil, nil, err
	}
	dst, dstNames, err := rank(p.to)
	if err != nil {
		return nil, nil, err
	}
	lost, won := map[string]bool{}, map[string]bool{}
	for _, pl := range p.Pages {
		for id, r := range src {
			if r >= pl.Level && dst[id] < pl.NewLevel {
				lost[id] = true
			}
		}
		for id, r := range dst {
			if r >= pl.NewLevel && src[id] < pl.Level {
				won[id] = true
			}
		}
	}
	for id := range lost {
		lose = append(lose, srcNames[id])
	}
	for id := range won {
		gain = append(gain, dstNames[id])
	}
	slices.Sort(lose)
	slices.Sort(gain)
	return lose, gain, nil
}

func (s *Service) Preview(ctx context.Context, req Request) (Preview, error) {
	p, err := s.plan(ctx, req)
	return p.Preview, err
}

// Transfer hace el traslado. Vuelve a armar el plan: lo que se confirma es lo que hay ahora, y
// si una página cambió desde la vista previa, el repo lo rechaza (ErrChanged).
func (s *Service) Transfer(ctx context.Context, actor repo.User, req Request) (Preview, error) {
	p, err := s.plan(ctx, req)
	if err != nil {
		return p.Preview, err
	}
	if len(p.Impact.Taken) > 0 {
		return p.Preview, ErrTaken
	}
	// Las URL de media del origen, con su slug y los viejos (B17), pasan a las del destino.
	aliases, err := s.store.WorkspaceAliases(ctx, p.From.ID)
	if err != nil {
		return p.Preview, err
	}
	replace := map[string]string{}
	for _, slug := range append([]string{p.From.Slug}, aliases...) {
		replace["/api/w/"+slug+"/media/"] = "/api/w/" + p.Dest.Slug + "/media/"
	}
	m := repo.TransferMove{From: p.from, To: p.to, ActorID: actor.ID, Replace: replace}
	for i, pg := range p.pages {
		md := pg.Markdown
		for old, cur := range replace {
			md = strings.ReplaceAll(md, old, cur)
		}
		m.Pages = append(m.Pages, repo.TransferredPage{ID: pg.ID, Slug: pg.Slug, NewSlug: p.Pages[i].NewSlug, Revision: pg.Revision,
			NewLevel: p.Pages[i].NewLevel, Markdown: md, Links: wiki.Links(md), Index: wiki.IndexDoc(pg.Title, pg.Description, md, pg.Lang)})
	}
	for i, sd := range p.sealed {
		m.Sealed = append(m.Sealed, repo.SealedInTree{ID: sd.ID, Slug: p.Sealed[i].NewSlug, AuthorID: sd.AuthorID})
	}
	res, err := s.store.TransferPages(ctx, m)
	if errors.Is(err, repo.ErrConflict) {
		return p.Preview, ErrTaken
	}
	if err != nil {
		return p.Preview, err
	}
	if err := s.files.MoveFiles(p.From.ID, p.Dest.ID, res.Media); err != nil {
		// Las filas ya están en el destino: lo que no se movió lo encuentra doctor como faltante.
		s.log.Error("transfer: move files", "from", p.From.ID, "to", p.Dest.ID, "err", err)
	}
	s.record(ctx, p, res, actor)
	return p.Preview, nil
}

// record deja el traslado en la auditoría de las dos áreas (page.transfer_out en el origen y
// page.transfer_in en el destino), unidas por un id que solo cruza el superadmin: el admin del
// origen no ve a dónde se fue (§4.13). Y avisa a los autores de las propuestas retiradas y de
// los sellados que se movieron.
func (s *Service) record(ctx context.Context, p plan, res repo.TransferResult, actor repo.User) {
	id := ulid.Make().String()
	for i, pg := range p.pages {
		s.audit.Record(ctx, audit.Event{Action: audit.PageTransferOut, WsID: p.From.ID, TargetType: "page", TargetID: pg.ID,
			Meta: map[string]any{"transfer": id, "slug": pg.Slug}})
		s.audit.Record(ctx, audit.Event{Action: audit.PageTransferIn, WsID: p.Dest.ID, TargetType: "page", TargetID: pg.ID,
			Meta: map[string]any{"transfer": id, "slug": p.Pages[i].NewSlug, "level": p.Pages[i].NewLevel}})
	}
	for i, sd := range p.sealed {
		s.audit.Record(ctx, audit.Event{Action: audit.PageTransferOut, WsID: p.From.ID, TargetType: "sealed", TargetID: sd.ID,
			Meta: map[string]any{"transfer": id, "slug": sd.Slug}})
		s.audit.Record(ctx, audit.Event{Action: audit.PageTransferIn, WsID: p.Dest.ID, TargetType: "sealed", TargetID: sd.ID,
			Meta: map[string]any{"transfer": id, "slug": p.Sealed[i].NewSlug}})
	}
	for _, e := range res.Ended {
		s.audit.Record(ctx, audit.Event{Action: audit.ShareEnd, WsID: p.From.ID, TargetType: "share", TargetID: e.ID,
			Meta: map[string]any{"page": e.PageID, "reason": e.Reason}})
	}
	for _, w := range res.Withdrawn {
		s.notify(ctx, p.From.ID, w.AuthorID, "proposal_transferred", "my_proposals", map[string]string{"title": w.Title})
	}
	// El autor de un sellado lo sigue abriendo por /sealed/{id}; se le avisa en el área donde estaba.
	for _, sd := range p.sealed {
		if sd.AuthorID != actor.ID {
			s.notify(ctx, p.From.ID, sd.AuthorID, "sealed_transferred", "account", map[string]string{"title": sd.Slug, "sealed": sd.ID})
		}
	}
}

func (s *Service) notify(ctx context.Context, wsID, userID, kind, category string, payload map[string]string) {
	b, _ := json.Marshal(payload)
	if err := s.store.AddNotifications(ctx, wsID, []repo.NewNotification{{UserID: userID, Kind: kind, Category: category, Payload: string(b)}}); err != nil {
		s.log.Error("transfer notice", "kind", kind, "err", err)
	}
}
