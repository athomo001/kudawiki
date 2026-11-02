// Package shares son los artículos compartidos entre áreas (SPEC §4.19, ADR-0010): un editor
// pide compartir una página N1 con otras áreas, la aprueba un admin del área de origen y aparece
// en *Compartido* de cada destino, en solo lectura y siempre en su versión vigente.
package shares

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/oklog/ulid/v2"
)

var (
	// ErrInvalid: un pedido mal formado (sin destinos, demasiados o un área que no es destino).
	ErrInvalid = errors.New("invalid_share")
	// ErrCommentRequired: rechazar sin decir por qué deja a quien pidió sin saber qué cambiar.
	ErrCommentRequired = errors.New("comment_required")
	// ErrClosed: el pedido ya no está pendiente (otro admin lo resolvió).
	ErrClosed = errors.New("share_closed")
)

// maxTargets es el tope de áreas por pedido: alcanza para cualquier empresa y corta un pedido abusivo.
const maxTargets = 100

type store interface {
	PageBySlug(ctx context.Context, s repo.Scope, slug string) (repo.Page, error)
	UserByID(ctx context.Context, id string) (repo.User, error)
	WorkspaceByID(ctx context.Context, id string) (repo.Workspace, error)
	AreaAdmins(ctx context.Context, wsID string) ([]string, error)
	AddNotifications(ctx context.Context, wsID string, ns []repo.NewNotification) error
	ShareTargets(ctx context.Context, s repo.Scope) ([]repo.ShareTarget, error)
	RequestShares(ctx context.Context, s repo.Scope, pageID, actorID string, targets, ids []string, approved bool) ([]string, error)
	Shares(ctx context.Context, s repo.Scope, pageID string) ([]repo.Share, error)
	ShareByID(ctx context.Context, s repo.Scope, id string) (repo.Share, error)
	ReviewShare(ctx context.Context, s repo.Scope, id, reviewerID string, approve bool, comment string) error
	EndShare(ctx context.Context, s repo.Scope, id, actorID, reason string) error
	EndInvalidShares(ctx context.Context, s repo.Scope, actorID string) ([]repo.EndedShare, error)
	ShareAudience(ctx context.Context, s repo.Scope, pageID string) ([]repo.ShareAudience, error)
	SharedWith(ctx context.Context, s repo.Scope) ([]repo.SharedArticle, error)
	SharedArticleByID(ctx context.Context, s repo.Scope, id string) (repo.SharedArticle, error)
	SearchShared(ctx context.Context, s repo.Scope, match string, limit int) ([]repo.SharedSearchHit, error)
}

type Service struct {
	store store
	audit *audit.Recorder
	log   *slog.Logger
}

func NewService(s store, rec *audit.Recorder, log *slog.Logger) *Service {
	return &Service{store: s, audit: rec, log: log}
}

const admin = 3

// Targets son las áreas a las que se puede pedir compartir (§19.40 a: todas, solo los nombres).
func (s *Service) Targets(ctx context.Context, sc repo.Scope) ([]repo.ShareTarget, error) {
	return s.store.ShareTargets(ctx, sc)
}

// Request pide compartir la página con esas áreas. Si pide un admin del origen, ya queda
// aprobado: es quien aprueba (§4.19). Devuelve si quedó activo.
func (s *Service) Request(ctx context.Context, sc repo.Scope, actorID, slug string, targets []string) (bool, error) {
	targets = unique(targets)
	if len(targets) == 0 || len(targets) > maxTargets {
		return false, ErrInvalid
	}
	valid, err := s.store.ShareTargets(ctx, sc)
	if err != nil {
		return false, err
	}
	names := make(map[string]string, len(valid))
	for _, t := range valid {
		names[t.ID] = t.Name
	}
	for _, t := range targets {
		if _, ok := names[t]; !ok {
			return false, ErrInvalid
		}
	}
	page, err := s.store.PageBySlug(ctx, sc, slug)
	if err != nil {
		return false, err
	}
	ids := make([]string, len(targets))
	for i := range ids {
		ids[i] = ulid.Make().String()
	}
	approved := sc.MaxRank >= admin
	created, err := s.store.RequestShares(ctx, sc, page.ID, actorID, targets, ids, approved)
	if err != nil {
		return false, err
	}
	actor := s.name(ctx, actorID)
	for i, id := range ids {
		if !slices.Contains(created, id) {
			continue
		}
		s.both(ctx, audit.ShareRequest, sc.WsID, targets[i], id, map[string]any{"page": page.ID, "approved": approved})
		if approved {
			s.received(ctx, targets[i], id, page.Title, actor, sc.WsID)
		}
	}
	if !approved && len(created) > 0 {
		s.notifyAdmins(ctx, sc.WsID, actorID, repo.NewNotification{Kind: "share_pending", Category: "proposals", PageID: page.ID,
			MinRank: admin, GroupKey: "share_pending:" + sc.WsID, Payload: payload{Title: page.Title, Actor: actor}.json()})
	}
	return approved, nil
}

// List es la bandeja del admin del origen: lo pendiente y lo compartido. pageID vacío: todas.
func (s *Service) List(ctx context.Context, sc repo.Scope, pageID string) ([]repo.Share, error) {
	return s.store.Shares(ctx, sc, pageID)
}

// Approve deja activo un pedido: el artículo aparece en *Compartido* del destino sin otro paso
// (§19.40 b), y su admin recibe el aviso.
func (s *Service) Approve(ctx context.Context, sc repo.Scope, reviewerID, id string) error {
	sh, err := s.review(ctx, sc, reviewerID, id, true, "")
	if err != nil {
		return err
	}
	reviewer := s.name(ctx, reviewerID)
	s.resolved(ctx, sc, sh, "share_approved", reviewer, reviewerID)
	s.received(ctx, sh.TargetID, sh.ID, sh.PageTitle, reviewer, sc.WsID)
	s.both(ctx, audit.ShareApprove, sc.WsID, sh.TargetID, sh.ID, map[string]any{"page": sh.PageID})
	return nil
}

// Reject devuelve el pedido con un comentario, que es obligatorio.
func (s *Service) Reject(ctx context.Context, sc repo.Scope, reviewerID, id, comment string) error {
	comment = strings.TrimSpace(comment)
	if comment == "" {
		return ErrCommentRequired
	}
	sh, err := s.review(ctx, sc, reviewerID, id, false, comment)
	if err != nil {
		return err
	}
	s.resolved(ctx, sc, sh, "share_rejected", s.name(ctx, reviewerID), reviewerID)
	s.both(ctx, audit.ShareReject, sc.WsID, sh.TargetID, sh.ID, map[string]any{"page": sh.PageID})
	return nil
}

func (s *Service) review(ctx context.Context, sc repo.Scope, reviewerID, id string, approve bool, comment string) (repo.Share, error) {
	sh, err := s.store.ShareByID(ctx, sc, id)
	if err != nil {
		return repo.Share{}, err
	}
	err = s.store.ReviewShare(ctx, sc, id, reviewerID, approve, comment)
	if errors.Is(err, repo.ErrConflict) {
		return repo.Share{}, ErrClosed
	}
	return sh, err
}

// Unshare la retira el admin del origen, esté pendiente o activa.
func (s *Service) Unshare(ctx context.Context, sc repo.Scope, actorID, id string) error {
	sh, err := s.store.ShareByID(ctx, sc, id)
	if err != nil {
		return err
	}
	err = s.store.EndShare(ctx, sc, id, actorID, "unshared")
	if errors.Is(err, repo.ErrNotFound) {
		return ErrClosed // existe pero ya estaba resuelto o terminado: el admin lo ve, no es un 404
	}
	if err != nil {
		return err
	}
	s.both(ctx, audit.ShareEnd, sc.WsID, sh.TargetID, id, map[string]any{"page": sh.PageID, "reason": "unshared"})
	return nil
}

// Shared es el *Compartido* del área de sc.
func (s *Service) Shared(ctx context.Context, sc repo.Scope) ([]repo.SharedArticle, error) {
	return s.store.SharedWith(ctx, sc)
}

// Article es un artículo del *Compartido* del área de sc.
func (s *Service) Article(ctx context.Context, sc repo.Scope, id string) (repo.SharedArticle, error) {
	return s.store.SharedArticleByID(ctx, sc, id)
}

// Remove la saca el admin del destino de su *Compartido*; no vuelve salvo que el origen la
// comparta de nuevo con esa área (§4.19).
func (s *Service) Remove(ctx context.Context, sc repo.Scope, actorID, id string) error {
	a, err := s.store.SharedArticleByID(ctx, sc, id)
	if err != nil {
		return err
	}
	if err := s.store.EndShare(ctx, sc, id, actorID, "removed"); err != nil {
		return err
	}
	s.both(ctx, audit.ShareEnd, a.SourceWsID, sc.WsID, id, map[string]any{"page": a.PageID, "reason": "removed"})
	return nil
}

// Search busca en el *Compartido* del área de sc.
func (s *Service) Search(ctx context.Context, sc repo.Scope, match string, limit int) ([]repo.SharedSearchHit, error) {
	return s.store.SearchShared(ctx, sc, match, limit)
}

// Audience es con qué áreas, y cuántas personas de cada una, se comparte la página (§3.4).
func (s *Service) Audience(ctx context.Context, sc repo.Scope, pageID string) ([]repo.ShareAudience, error) {
	return s.store.ShareAudience(ctx, sc, pageID)
}

// Sweep termina lo que ya no se puede compartir después de subir el nivel, mover o borrar
// páginas del área de sc, lo registra en las dos áreas y avisa a los admins del origen (§4.19).
// Lo llama quien cambió las páginas, después de que el cambio tuvo éxito.
func (s *Service) Sweep(ctx context.Context, sc repo.Scope, actorID string) {
	ended, err := s.store.EndInvalidShares(ctx, sc, actorID)
	if err != nil {
		// La lectura del destino ya no muestra esas páginas: solo queda el estado sin poner al día.
		s.log.Error("end invalid shares", "workspace", sc.WsID, "err", err)
		return
	}
	notified := map[string]bool{}
	for _, e := range ended {
		s.both(ctx, audit.ShareEnd, sc.WsID, e.TargetID, e.ID, map[string]any{"page": e.PageID, "reason": e.Reason})
		if e.Reason == "level" && !notified[e.PageID] {
			notified[e.PageID] = true
			s.notifyAdmins(ctx, sc.WsID, "", repo.NewNotification{Kind: "share_ended", Category: "proposals", PageID: e.PageID,
				MinRank: admin, Payload: payload{Title: e.PageTitle, Actor: s.name(ctx, actorID)}.json()})
		}
	}
}

// payload es lo que lleva cada aviso, como en internal/notify: el título y quién, nunca contenido.
// From es el área de origen (para el destino), Target el área de destino (para el origen) y Share
// el vínculo, para llegar al artículo desde el destino.
type payload struct {
	Title  string `json:"title"`
	Actor  string `json:"actor,omitempty"`
	From   string `json:"from,omitempty"`
	Target string `json:"target,omitempty"`
	Share  string `json:"share,omitempty"`
}

func (p payload) json() string {
	b, _ := json.Marshal(p)
	return string(b)
}

// received avisa a los admins del destino que llegó un artículo a su *Compartido*. Sin página:
// la del origen no es del destino; el aviso lleva al vínculo.
func (s *Service) received(ctx context.Context, targetID, shareID, title, actor, sourceID string) {
	var from string
	if ws, err := s.store.WorkspaceByID(ctx, sourceID); err == nil {
		from = ws.Name
	}
	s.notifyAdmins(ctx, targetID, "", repo.NewNotification{Kind: "shared_received", Category: "proposals", MinRank: admin,
		Payload: payload{Title: title, Actor: actor, From: from, Share: shareID}.json()})
}

// resolved avisa a quien pidió que el admin aprobó o rechazó su pedido.
func (s *Service) resolved(ctx context.Context, sc repo.Scope, sh repo.Share, kind, reviewer, reviewerID string) {
	if sh.RequestedByID == reviewerID {
		return
	}
	err := s.store.AddNotifications(ctx, sc.WsID, []repo.NewNotification{{UserID: sh.RequestedByID, Kind: kind, Category: "my_proposals",
		PageID: sh.PageID, Payload: payload{Title: sh.PageTitle, Actor: reviewer, Target: sh.TargetName}.json()}})
	if err != nil {
		s.log.Error("share notice failed", "kind", kind, "err", err)
	}
}

// notifyAdmins deja el aviso a cada admin del área menos a quien hizo la acción. Un aviso que no
// sale no deshace la acción: ya está hecha y en la auditoría.
func (s *Service) notifyAdmins(ctx context.Context, wsID, actorID string, n repo.NewNotification) {
	admins, err := s.store.AreaAdmins(ctx, wsID)
	if err != nil {
		s.log.Error("share notice failed", "kind", n.Kind, "err", err)
		return
	}
	ns := make([]repo.NewNotification, 0, len(admins))
	for _, id := range admins {
		if id != actorID {
			n.UserID = id
			ns = append(ns, n)
		}
	}
	if len(ns) == 0 {
		return
	}
	if err := s.store.AddNotifications(ctx, wsID, ns); err != nil {
		s.log.Error("share notice failed", "kind", n.Kind, "err", err)
	}
}

// both registra el evento en la auditoría de las dos áreas (§4.19): cada admin ve en la suya qué
// se compartió con quién, o qué le llegó de dónde. meta lleva solo ids.
func (s *Service) both(ctx context.Context, a audit.Action, sourceID, targetID, shareID string, meta map[string]any) {
	meta["source"], meta["target"] = sourceID, targetID
	for _, ws := range []string{sourceID, targetID} {
		s.audit.Record(ctx, audit.Event{Action: a, WsID: ws, TargetType: "share", TargetID: shareID, Meta: meta})
	}
}

func (s *Service) name(ctx context.Context, userID string) string {
	if u, err := s.store.UserByID(ctx, userID); err == nil {
		return u.FullName
	}
	return ""
}

func unique(list []string) []string {
	seen := make(map[string]bool, len(list))
	out := make([]string, 0, len(list))
	for _, v := range list {
		if v = strings.TrimSpace(v); v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
