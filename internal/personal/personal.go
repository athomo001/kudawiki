// Package personal es publicar desde el espacio personal en un área (SPEC §7). El espacio en sí
// es un workspace más: lo que tiene de propio es este paso de una contención a otra.
package personal

import (
	"context"
	"errors"
	"strings"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/proposals"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/athomo001/kudawiki/internal/wiki"
)

type store interface {
	EnsurePersonal(ctx context.Context, userID string) (repo.Workspace, error)
	PersonalBySlug(ctx context.Context, slug string) (repo.Workspace, error)
	PageMedia(ctx context.Context, s repo.Scope, pageID string) ([]repo.Media, error)
	PublishPage(ctx context.Context, from, to repo.Scope, m repo.PublishMove) error
}

// pages es wiki.Service: leer la página y la regla de quién publica directo (§5.1).
type pages interface {
	Get(ctx context.Context, sc repo.Scope, slug string) (repo.Page, error)
	MustPropose(ctx context.Context, sc repo.Scope, slug string) (bool, error)
}

type proposer interface {
	Create(ctx context.Context, sc repo.Scope, authorID string, d proposals.Draft) (string, error)
}

// files es media.Service: copiar los archivos al área y borrarlos si la publicación no se hace.
type files interface {
	Copy(fromWs, toWs string, m repo.Media) (repo.Media, error)
	Remove(wsID, id string) error
}

type Service struct {
	store     store
	pages     pages
	proposals proposer
	files     files
	audit     *audit.Recorder
}

func NewService(s store, p pages, pr proposer, f files, rec *audit.Recorder) *Service {
	return &Service{store: s, pages: p, proposals: pr, files: f, audit: rec}
}

// Of es el espacio personal de la persona; si todavía no lo tiene, se crea (§7).
func (s *Service) Of(ctx context.Context, userID string) (repo.Workspace, error) {
	return s.store.EnsurePersonal(ctx, userID)
}

// Audit abre el espacio personal de otra persona en modo auditoría (§7): solo el superadmin, y
// cada pedido (path) queda registrado en la auditoría global y en la del espacio, que su dueño
// también puede ver. Para cualquier otro, el espacio no existe.
func (s *Service) Audit(ctx context.Context, u repo.User, slug, path string) (repo.Workspace, error) {
	if !u.IsGlobalAdmin {
		return repo.Workspace{}, repo.ErrNotFound
	}
	ws, err := s.store.PersonalBySlug(ctx, slug)
	if err != nil {
		return repo.Workspace{}, err
	}
	if ws.OwnerID == u.ID { // el propio entra como miembro; acá no llega
		return repo.Workspace{}, repo.ErrNotFound
	}
	s.audit.Record(ctx, audit.Event{Action: audit.PersonalAuditOpen, WsID: ws.ID, TargetType: "user", TargetID: ws.OwnerID,
		Meta: map[string]any{"path": path}})
	return ws, nil
}

// Space es un workspace con la ruta con que se nombra en las URLs de media.
type Space struct {
	repo.Scope
	Slug string
}

// Result dice en qué terminó: la página ya está en el área, o quedó una propuesta.
type Result struct {
	Proposal string // vacío si se publicó directo
}

// Publish lleva la página slug del espacio personal (from) a la ruta to del área. Con aprobación
// directa la página se mueve con todo su historial; si el área pide aprobación, queda una
// propuesta de página nueva y el borrador sigue en el espacio personal (§7). Quien publica tiene
// que ser editor o admin del área: si no, el área no existe para él.
func (s *Service) Publish(ctx context.Context, actorID string, from, area Space, slug, to string) (Result, error) {
	if area.MaxRank < 2 {
		return Result{}, repo.ErrNotFound
	}
	if !wiki.ValidSlug(to) {
		return Result{}, wiki.ErrInvalidPage
	}
	page, err := s.pages.Get(ctx, from.Scope, slug)
	if err != nil {
		return Result{}, err
	}
	must, err := s.pages.MustPropose(ctx, area.Scope, to)
	if err != nil {
		return Result{}, err
	}
	media, err := s.store.PageMedia(ctx, from.Scope, page.ID)
	if err != nil {
		return Result{}, err
	}
	copies := make([]repo.Media, 0, len(media))
	replace := make(map[string]string, len(media))
	undo := func() {
		for _, c := range copies {
			_ = s.files.Remove(area.WsID, c.ID) // lo que quede lo encuentra doctor como archivo sin fila
		}
	}
	for _, m := range media {
		c, err := s.files.Copy(from.WsID, area.WsID, m)
		if err != nil {
			undo()
			return Result{}, err
		}
		copies = append(copies, c)
		replace[mediaURL(from.Slug, m.ID)] = mediaURL(area.Slug, c.ID)
	}
	markdown := page.Markdown
	for old, cur := range replace {
		markdown = strings.ReplaceAll(markdown, old, cur)
	}

	if must {
		id, err := s.proposals.Create(ctx, area.Scope, actorID, proposals.Draft{
			Slug: to, Title: page.Title, Markdown: markdown, Lang: page.Lang, Source: page.ID, Media: copies,
		})
		if err != nil {
			undo()
			return Result{}, err
		}
		return Result{Proposal: id}, nil
	}
	err = s.store.PublishPage(ctx, from.Scope, area.Scope, repo.PublishMove{
		PageID: page.ID, Slug: to, ActorID: actorID, Media: copies, Replace: replace, Markdown: markdown,
		Links: wiki.Links(markdown), Index: wiki.IndexDoc(page.Title, page.Description, markdown, page.Lang),
	})
	if errors.Is(err, repo.ErrConflict) || errors.Is(err, repo.ErrHidden) {
		undo()
		// Como al crear una página: un solo mensaje, y el intento queda registrado (§19.35).
		s.audit.Record(ctx, audit.Event{Action: audit.PathUnavailable, WsID: area.WsID, TargetType: "page", Meta: map[string]any{"op": "publish"}})
		return Result{}, wiki.ErrPathUnavailable
	}
	if err != nil {
		undo()
		return Result{}, err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.PagePublish, WsID: area.WsID, TargetType: "page", TargetID: page.ID,
		Meta: map[string]any{"revisions": page.Revision + 1, "media": len(copies)}})
	return Result{}, nil
}

func mediaURL(ws, id string) string { return "/api/w/" + ws + "/media/" + id }
