package wiki

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/repo"
)

// ErrInvalidAnnouncement: anuncio sin título, o con el título o el texto demasiado largos (§4.17).
var ErrInvalidAnnouncement = errors.New("invalid_announcement")

const maxAnnouncement = 20 << 10 // 20 KiB: un aviso, no una página

type announcementStore interface {
	CreateAnnouncement(ctx context.Context, s repo.Scope, a repo.Announcement) (string, error)
	AnnouncementByID(ctx context.Context, s repo.Scope, id string) (repo.Announcement, error)
	DeleteAnnouncement(ctx context.Context, s repo.Scope, id string) error
}

// CanDeleteAnnouncement: quien lo escribió o un admin del área (§4.17).
func CanDeleteAnnouncement(sc repo.Scope, actorID, authorID string) bool {
	return actorID == authorID || sc.MaxRank >= repo.RoleRank("admin")
}

// Announce publica un anuncio en el inicio del área. La auditoría guarda el id, nunca el texto.
func (s *Service) Announce(ctx context.Context, sc repo.Scope, actorID, title, body string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" || utf8.RuneCountInString(title) > maxTitle || len(body) > maxAnnouncement {
		return "", ErrInvalidAnnouncement
	}
	id, err := s.store.CreateAnnouncement(ctx, sc, repo.Announcement{Title: title, Markdown: body, AuthorID: actorID})
	if err != nil {
		return "", err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.AnnouncementCreate, WsID: sc.WsID, TargetType: "announcement", TargetID: id})
	return id, nil
}

// DeleteAnnouncement borra un anuncio. Para quien no puede borrarlo, no existe: el mismo 404
// que uno inexistente (§3.2).
func (s *Service) DeleteAnnouncement(ctx context.Context, sc repo.Scope, actorID, id string) error {
	a, err := s.store.AnnouncementByID(ctx, sc, id)
	if err != nil {
		return err
	}
	if !CanDeleteAnnouncement(sc, actorID, a.AuthorID) {
		return repo.ErrNotFound
	}
	if err := s.store.DeleteAnnouncement(ctx, sc, id); err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.AnnouncementDelete, WsID: sc.WsID, TargetType: "announcement", TargetID: id})
	return nil
}
