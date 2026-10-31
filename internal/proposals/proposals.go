// Package proposals es el modo draft: cambios que un admin del área aprueba antes de publicarse
// (SPEC §5).
package proposals

import (
	"context"
	"errors"
	"strings"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/athomo001/kudawiki/internal/wiki"
	"github.com/oklog/ulid/v2"
)

var (
	ErrNoChanges       = errors.New("no_changes")
	ErrCommentRequired = errors.New("comment_required")
	// ErrClosed: la propuesta ya no está en un estado que permita eso (p. ej. aprobar una rechazada).
	ErrClosed = errors.New("proposal_closed")
	// ErrSelfApproval: un admin no aprueba su propia propuesta si hay otro admin en el área (§5.2).
	ErrSelfApproval = errors.New("self_approval")
)

type store interface {
	WorkspaceByID(ctx context.Context, id string) (repo.Workspace, error)
	Members(ctx context.Context, s repo.Scope) ([]repo.Member, error)
	PageBySlug(ctx context.Context, s repo.Scope, slug string) (repo.Page, error)
	RevisionByNumber(ctx context.Context, s repo.Scope, pageID string, n int) (repo.Revision, error)
	CreateProposal(ctx context.Context, s repo.Scope, p repo.Proposal) error
	ProposalByID(ctx context.Context, s repo.Scope, id string) (repo.Proposal, error)
	Proposals(ctx context.Context, s repo.Scope, f repo.ProposalFilter) ([]repo.Proposal, error)
	UpdateProposal(ctx context.Context, s repo.Scope, p repo.Proposal) error
	SetProposalStatus(ctx context.Context, s repo.Scope, id string, from []string, to, reviewerID, comment string) error
}

// publisher publica la revisión aprobada (wiki.Service.Publish) y aplica el resumen obligatorio
// del área (§4.3) con la misma regla que guardar directo.
type publisher interface {
	Publish(ctx context.Context, sc repo.Scope, base int, w repo.PageWrite) (string, int, error)
	CheckSummary(ctx context.Context, sc repo.Scope, base int, summary string) error
}

type Service struct {
	store store
	pages publisher
	audit *audit.Recorder
}

func NewService(s store, pages publisher, rec *audit.Recorder) *Service {
	return &Service{store: s, pages: pages, audit: rec}
}

// Draft es lo que se propone. Base es la revisión sobre la que se escribió; 0, una página nueva.
// Source es la página del espacio personal de la que sale una página nueva (§7) y Media, las
// copias de su media en el área.
type Draft struct {
	Slug, Title, Markdown, Lang, Summary string
	Base                                 int
	Source                               string
	Media                                []repo.Media
}

func (d *Draft) clean() error {
	d.Title, d.Summary = strings.TrimSpace(d.Title), strings.TrimSpace(d.Summary)
	if d.Base < 0 {
		return wiki.ErrInvalidPage
	}
	return wiki.CheckPage(d.Slug, d.Title, d.Markdown, d.Lang)
}

const admin = 3

func (s *Service) record(ctx context.Context, sc repo.Scope, a audit.Action, targetType, id string, meta map[string]any) {
	s.audit.Record(ctx, audit.Event{Action: a, WsID: sc.WsID, TargetType: targetType, TargetID: id, Meta: meta})
}

// Create envía una propuesta (§5.2). Proponen los editores y los admins, y los lectores si el
// área lo permite (§19.5); un lector propone cambios a lo que lee, no páginas nuevas. Lo que no
// le corresponde es el 404 uniforme.
func (s *Service) Create(ctx context.Context, sc repo.Scope, authorID string, d Draft) (string, error) {
	if err := d.clean(); err != nil {
		return "", err
	}
	if sc.MaxRank == 1 {
		ws, err := s.store.WorkspaceByID(ctx, sc.WsID)
		if err != nil {
			return "", err
		}
		if !ws.ViewersCanPropose || d.Base == 0 {
			return "", repo.ErrNotFound
		}
	}
	if err := s.pages.CheckSummary(ctx, sc, d.Base, d.Summary); err != nil {
		return "", err
	}
	p := repo.Proposal{ID: ulid.Make().String(), Slug: d.Slug, Title: d.Title, Markdown: d.Markdown, Lang: d.Lang, Summary: d.Summary, AuthorID: authorID}
	if d.Base == 0 {
		p.SourcePageID, p.Media = d.Source, d.Media
	}
	if d.Base > 0 {
		page, err := s.store.PageBySlug(ctx, sc, d.Slug)
		if err != nil {
			return "", err
		}
		if d.Base > page.Revision {
			return "", wiki.ErrInvalidPage
		}
		if p.Lang == "" {
			p.Lang = page.Lang
		}
		if page.Revision == d.Base && page.Title == p.Title && page.Markdown == p.Markdown && page.Lang == p.Lang {
			return "", ErrNoChanges
		}
		p.PageID, p.Base = page.ID, d.Base
	} else if p.Lang == "" {
		p.Lang = "es"
	}
	err := s.store.CreateProposal(ctx, sc, p)
	if errors.Is(err, repo.ErrConflict) || errors.Is(err, repo.ErrHidden) {
		// Igual que al crear la página: un solo mensaje, y el intento queda registrado (§19.35).
		s.record(ctx, sc, audit.PathUnavailable, "page", "", map[string]any{"op": "propose"})
		return "", wiki.ErrPathUnavailable
	}
	if err != nil {
		return "", err
	}
	s.record(ctx, sc, audit.ProposalSubmit, "proposal", p.ID, map[string]any{"new_page": p.PageID == ""})
	return p.ID, nil
}

// visible devuelve la propuesta si quien pregunta es su autor o un admin del área; si no, el 404
// uniforme, igual que si no existiera.
func (s *Service) visible(ctx context.Context, sc repo.Scope, userID, id string) (repo.Proposal, error) {
	p, err := s.store.ProposalByID(ctx, sc, id)
	if err != nil {
		return repo.Proposal{}, err
	}
	if sc.MaxRank < admin && p.AuthorID != userID {
		return repo.Proposal{}, repo.ErrNotFound
	}
	return p, nil
}

// own devuelve la propuesta solo a su autor.
func (s *Service) own(ctx context.Context, sc repo.Scope, userID, id string) (repo.Proposal, error) {
	p, err := s.store.ProposalByID(ctx, sc, id)
	if err == nil && p.AuthorID != userID {
		err = repo.ErrNotFound
	}
	return p, err
}

// List es la bandeja (§5.2): el admin ve las del área; los demás, solo las suyas. pending son
// las que esperan; resolved, las aprobadas y las rechazadas; mine, todas las propias.
func (s *Service) List(ctx context.Context, sc repo.Scope, userID, view string) ([]repo.Proposal, error) {
	f := repo.ProposalFilter{Limit: 200}
	switch view {
	case "pending":
		f.Status = []string{"pending"}
	case "resolved":
		f.Status = []string{"merged", "rejected"}
	case "mine":
		f.AuthorID = userID
	default:
		return nil, ErrInvalid
	}
	if sc.MaxRank < admin {
		f.AuthorID = userID
	}
	return s.store.Proposals(ctx, sc, f)
}

// ErrInvalid: un pedido mal formado (una bandeja que no existe).
var ErrInvalid = errors.New("invalid_request")

// Detail es lo que se revisa: los cambios contra la revisión base y, si la página avanzó desde
// entonces, si se mezcla sola o tiene conflicto (§5.2).
type Detail struct {
	repo.Proposal
	Changes []wiki.DiffLine
	// PageChanged: la página tiene revisiones nuevas desde la base.
	PageChanged bool
	Conflict    bool
	// AgainstCurrent: de la versión actual a lo que quedaría publicado (o a la propuesta, si hay
	// conflicto). PageChanges: lo que la página cambió desde la base, para rehacerlo encima.
	AgainstCurrent []wiki.DiffLine
	PageChanges    []wiki.DiffLine
	// Result y ResultTitle son lo que quedaría publicado: la vista previa, y de donde parte el
	// admin que la edita antes de aprobar.
	Result, ResultTitle string
}

func (s *Service) Review(ctx context.Context, sc repo.Scope, userID, id string) (Detail, error) {
	p, err := s.visible(ctx, sc, userID, id)
	if err != nil {
		return Detail{}, err
	}
	d := Detail{Proposal: p, Result: p.Markdown, ResultTitle: p.Title}
	if p.PageID == "" || p.Status == "merged" {
		// Página nueva, o ya publicada: los cambios son los de la propuesta tal cual se guardó.
		base := ""
		if p.Base > 0 {
			if rev, err := s.store.RevisionByNumber(ctx, sc, p.PageID, p.Base); err == nil {
				base = rev.Markdown
			}
		}
		d.Changes = wiki.Diff(base, p.Markdown)
		return d, nil
	}
	m, err := s.merge(ctx, sc, p, p.Base, p.Title, p.Markdown)
	if err != nil && !errors.Is(err, wiki.ErrMergeConflict) {
		return Detail{}, err
	}
	d.Changes = wiki.Diff(m.base.Markdown, p.Markdown)
	if d.PageChanged = m.page.Revision > p.Base; d.PageChanged {
		d.Conflict = err != nil
		if d.Conflict {
			d.AgainstCurrent, d.PageChanges = wiki.Diff(m.page.Markdown, p.Markdown), wiki.Diff(m.base.Markdown, m.page.Markdown)
		} else {
			d.AgainstCurrent, d.Result, d.ResultTitle = wiki.Diff(m.page.Markdown, m.markdown), m.markdown, m.title
		}
	}
	return d, nil
}

// merged es la propuesta mezclada con la versión actual de su página.
type merged struct {
	page            repo.Page
	base            repo.Revision
	title, markdown string
}

// merge mezcla a 3 vías el título y el contenido propuestos sobre base con la versión actual de
// la página (§5.2). Si la página no avanzó, no hay nada que mezclar.
func (s *Service) merge(ctx context.Context, sc repo.Scope, p repo.Proposal, base int, title, markdown string) (merged, error) {
	page, err := s.store.PageBySlug(ctx, sc, p.Slug)
	if err != nil {
		return merged{}, err
	}
	rev, err := s.store.RevisionByNumber(ctx, sc, page.ID, base)
	if err != nil {
		return merged{}, err
	}
	m := merged{page: page, base: rev, title: title, markdown: markdown}
	if page.Revision == base {
		return m, nil
	}
	t, md, err := wiki.MergePage(rev.Title, rev.Markdown, page.Title, page.Markdown, title, markdown)
	if err != nil {
		return m, err
	}
	m.title, m.markdown = t, md
	return m, nil
}

// Update guarda lo que el autor cambió: corregir una rechazada antes de reenviarla, o rehacer
// sobre la versión actual una que tiene conflicto (§5.2).
func (s *Service) Update(ctx context.Context, sc repo.Scope, userID, id string, d Draft) error {
	p, err := s.own(ctx, sc, userID, id)
	if err != nil {
		return err
	}
	if p.Status != "pending" && p.Status != "rejected" {
		return ErrClosed
	}
	d.Slug = p.Slug
	if p.PageID == "" {
		d.Base = 0
	}
	if err := d.clean(); err != nil {
		return err
	}
	if p.PageID != "" && (d.Base == 0 || d.Base > p.Current) {
		return wiki.ErrInvalidPage
	}
	if err := s.pages.CheckSummary(ctx, sc, d.Base, d.Summary); err != nil {
		return err
	}
	if d.Lang == "" {
		d.Lang = p.Lang
	}
	return s.store.UpdateProposal(ctx, sc, repo.Proposal{ID: id, AuthorID: userID, Title: d.Title, Markdown: d.Markdown, Lang: d.Lang, Summary: d.Summary, Base: d.Base})
}

// Resubmit vuelve a enviar una propuesta rechazada (§5.2).
func (s *Service) Resubmit(ctx context.Context, sc repo.Scope, userID, id string) error {
	if _, err := s.own(ctx, sc, userID, id); err != nil {
		return err
	}
	if err := s.transition(ctx, sc, id, []string{"rejected"}, "pending", "", ""); err != nil {
		return err
	}
	s.record(ctx, sc, audit.ProposalSubmit, "proposal", id, map[string]any{"resubmit": true})
	return nil
}

// Withdraw la retira su autor: queda cerrada, sin publicarse.
func (s *Service) Withdraw(ctx context.Context, sc repo.Scope, userID, id string) error {
	if _, err := s.own(ctx, sc, userID, id); err != nil {
		return err
	}
	if err := s.transition(ctx, sc, id, []string{"pending", "rejected"}, "withdrawn", "", ""); err != nil {
		return err
	}
	s.record(ctx, sc, audit.ProposalWithdraw, "proposal", id, nil)
	return nil
}

// Reject la devuelve al autor con un comentario, que es obligatorio: sin él, el autor no sabe
// qué corregir.
func (s *Service) Reject(ctx context.Context, sc repo.Scope, reviewerID, id, comment string) error {
	if _, err := s.visible(ctx, sc, reviewerID, id); err != nil {
		return err
	}
	if sc.MaxRank < admin {
		return repo.ErrNotFound
	}
	if comment = strings.TrimSpace(comment); comment == "" {
		return ErrCommentRequired
	}
	if err := s.transition(ctx, sc, id, []string{"pending"}, "rejected", reviewerID, comment); err != nil {
		return err
	}
	s.record(ctx, sc, audit.ProposalReject, "proposal", id, nil)
	return nil
}

func (s *Service) transition(ctx context.Context, sc repo.Scope, id string, from []string, to, reviewerID, comment string) error {
	err := s.store.SetProposalStatus(ctx, sc, id, from, to, reviewerID, comment)
	if errors.Is(err, repo.ErrConflict) {
		return ErrClosed
	}
	return err
}

// Approve publica la propuesta, con los cambios del admin si los trae edit (§5.2: "aprobar con
// modificaciones"). La revisión queda a nombre del autor, con el admin como aprobador. Si la
// página avanzó desde la base se mezcla a 3 vías; con conflicto no se publica nada y la
// propuesta sigue pendiente (ErrMergeConflict). Devuelve la revisión publicada.
func (s *Service) Approve(ctx context.Context, sc repo.Scope, reviewerID, id, comment string, edit *Draft) (int, error) {
	p, err := s.visible(ctx, sc, reviewerID, id)
	if err != nil {
		return 0, err
	}
	if sc.MaxRank < admin {
		return 0, repo.ErrNotFound
	}
	if p.Status != "pending" {
		return 0, ErrClosed
	}
	if err := s.selfApproval(ctx, sc, p, reviewerID); err != nil {
		return 0, err
	}
	title, markdown, lang, base := p.Title, p.Markdown, p.Lang, p.Base
	if edit != nil {
		edit.Slug = p.Slug
		if err := edit.clean(); err != nil {
			return 0, err
		}
		title, markdown = edit.Title, edit.Markdown
		if edit.Lang != "" {
			lang = edit.Lang
		}
		if p.PageID != "" && edit.Base > 0 {
			base = edit.Base // el admin lo rehízo sobre esa revisión
		}
	}
	w := repo.PageWrite{Slug: p.Slug, Title: title, Markdown: markdown, Lang: lang, Summary: p.Summary, AuthorID: p.AuthorID,
		Merge: &repo.ProposalMerge{ID: p.ID, ReviewerID: reviewerID, Comment: strings.TrimSpace(comment)}}
	if p.PageID != "" {
		m, err := s.merge(ctx, sc, p, base, title, markdown)
		if err != nil {
			return 0, err
		}
		w.Title, w.Markdown, base = m.title, m.markdown, m.page.Revision
	}
	pageID, rev, err := s.pages.Publish(ctx, sc, base, w)
	if errors.Is(err, repo.ErrProposalClosed) {
		return 0, ErrClosed // otro admin la resolvió en el mismo momento
	}
	if err != nil {
		return 0, err
	}
	if rev == 0 {
		// La página ya decía exactamente eso: no hay revisión nueva, pero la propuesta se cierra.
		pageID = p.PageID
		if err := s.transition(ctx, sc, id, []string{"pending"}, "merged", reviewerID, w.Merge.Comment); err != nil {
			return 0, err
		}
	}
	s.record(ctx, sc, audit.ProposalApprove, "page", pageID, map[string]any{"proposal": id, "revision": rev, "modified": edit != nil})
	return rev, nil
}

func (s *Service) selfApproval(ctx context.Context, sc repo.Scope, p repo.Proposal, reviewerID string) error {
	if p.AuthorID != reviewerID {
		return nil
	}
	members, err := s.store.Members(ctx, sc)
	if err != nil {
		return err
	}
	for _, m := range members {
		if m.Role == "admin" && m.UserID != reviewerID {
			return ErrSelfApproval
		}
	}
	return nil
}
