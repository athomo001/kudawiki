package wiki

import (
	"context"
	"errors"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/repo"
)

// ErrInvalidReview: un intervalo fuera de rango o un dueño que no puede editar la página (§4.8).
var ErrInvalidReview = errors.New("invalid_review")

// maxReviewDays: diez años. Un intervalo más largo es no querer que venza, y eso es no fijarlo.
const maxReviewDays = 3650

// validReviewDays: 0 es "el del área".
func validReviewDays(days int) bool { return days >= 0 && days <= maxReviewDays }

// mayReview dice si quien actúa cuida la vigencia de la página: su dueño o un admin del área.
func mayReview(sc repo.Scope, actorID string, p repo.Page) bool {
	return p.OwnerID == actorID || sc.MaxRank >= 3
}

// Verify es "Marcar como vigente" (§4.8): el dueño o un admin confirma el contenido sin editarlo.
// Para cualquier otro, aunque la lea, es el 404 uniforme (§3.3).
func (s *Service) Verify(ctx context.Context, sc repo.Scope, actorID, slug string) error {
	p, err := s.store.PageBySlug(ctx, sc, slug)
	if err != nil {
		return err
	}
	if !mayReview(sc, actorID, p) {
		return repo.ErrNotFound
	}
	if err := s.store.VerifyPage(ctx, sc, p.ID, actorID); err != nil {
		return err
	}
	s.record(ctx, sc, audit.PageVerify, p.ID, nil)
	return nil
}

// SetReview cambia el dueño (solo un admin) o el intervalo de revisión (el dueño o un admin) de
// una página; lo que viene en nil queda como estaba. El dueño nuevo tiene que estar activo y
// poder editarla: es quien la va a revisar.
func (s *Service) SetReview(ctx context.Context, sc repo.Scope, actorID, slug string, ownerID *string, days *int) error {
	// El intervalo se valida antes de buscar la página: la respuesta es la misma exista o no.
	if days != nil && !validReviewDays(*days) {
		return ErrInvalidReview
	}
	p, err := s.store.PageBySlug(ctx, sc, slug)
	if err != nil {
		return err
	}
	if !mayReview(sc, actorID, p) || (ownerID != nil && *ownerID != p.OwnerID && sc.MaxRank < 3) {
		return repo.ErrNotFound
	}
	owner, interval := p.OwnerID, p.ReviewDays
	if days != nil {
		interval = *days
	}
	if ownerID != nil && *ownerID != p.OwnerID {
		rank, err := s.store.OwnerRank(ctx, sc, *ownerID)
		if err != nil {
			return err
		}
		if rank < max(2, p.MinRank) {
			return ErrInvalidReview
		}
		owner = *ownerID
	}
	if owner == p.OwnerID && interval == p.ReviewDays {
		return nil
	}
	if err := s.store.SetPageReview(ctx, sc, p.ID, owner, interval); err != nil {
		return err
	}
	s.record(ctx, sc, audit.PageReviewUpdate, p.ID, map[string]any{"owner_changed": owner != p.OwnerID, "interval_days": interval})
	return nil
}
