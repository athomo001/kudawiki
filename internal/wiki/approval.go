package wiki

import (
	"context"
	"errors"
	"strings"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/repo"
)

var (
	// ErrApprovalRequired: en esa ruta, quien escribe no publica directo; lo suyo va como
	// propuesta (§5.1). La página ya la ve, así que decirlo no delata nada.
	ErrApprovalRequired = errors.New("approval_required")
	ErrInvalidOverride  = errors.New("invalid_override")
)

type approvalStore interface {
	WorkspaceByID(ctx context.Context, id string) (repo.Workspace, error)
	ApprovalOverrides(ctx context.Context, s repo.Scope) ([]repo.ApprovalOverride, error)
	SetApprovalOverride(ctx context.Context, s repo.Scope, o repo.ApprovalOverride) error
	DeleteApprovalOverride(ctx context.Context, s repo.Scope, prefix string) error
}

// effectiveMode aplica §5.2.1: gana la excepción de la ruta más larga que contiene a slug; sin
// ninguna, el modo del área.
func effectiveMode(area string, overrides []repo.ApprovalOverride, slug string) string {
	mode, longest := area, -1
	for _, o := range overrides {
		if (slug == o.Prefix || strings.HasPrefix(slug, o.Prefix+"/")) && len(o.Prefix) > longest {
			mode, longest = o.Mode, len(o.Prefix)
		}
	}
	return mode
}

// MustPropose dice si quien tiene el Scope publica en slug solo por propuesta (§5.1): los admins
// siempre publican directo; los lectores, nunca; los editores, salvo que la ruta sea `required`.
func (s *Service) MustPropose(ctx context.Context, sc repo.Scope, slug string) (bool, error) {
	switch sc.MaxRank {
	case 3:
		return false, nil
	case 1:
		return true, nil
	}
	ws, err := s.store.WorkspaceByID(ctx, sc.WsID)
	if err != nil {
		return false, err
	}
	overrides, err := s.store.ApprovalOverrides(ctx, sc)
	if err != nil {
		return false, err
	}
	return effectiveMode(ws.ApprovalMode, overrides, slug) == "required", nil
}

// direct deja pasar un cambio publicado directo, o responde ErrApprovalRequired. Vale para todo
// lo que cambia lo publicado: guardar, revertir, mover, borrar, el nivel y los metadatos, porque
// en un área `required` todo cambio de un editor pasa por propuesta (§5.1).
func (s *Service) direct(ctx context.Context, sc repo.Scope, slugs ...string) error {
	for _, slug := range slugs {
		must, err := s.MustPropose(ctx, sc, slug)
		if err != nil {
			return err
		}
		if must {
			return ErrApprovalRequired
		}
	}
	return nil
}

// ApprovalOverrides lista las excepciones por subárbol del área (§5.2.1).
func (s *Service) ApprovalOverrides(ctx context.Context, sc repo.Scope) ([]repo.ApprovalOverride, error) {
	return s.store.ApprovalOverrides(ctx, sc)
}

// SetApprovalOverride crea o cambia la excepción de un subárbol. Puede exigir propuesta en una
// parte de un área `direct` o liberar una parte de un área `required`; el nivel de cada página
// sigue mandando sobre quién la edita (§5.2.1). La auditoría no guarda la ruta (§11.2).
func (s *Service) SetApprovalOverride(ctx context.Context, sc repo.Scope, prefix, mode string) error {
	if !ValidSlug(prefix) || (mode != "direct" && mode != "required") {
		return ErrInvalidOverride
	}
	if err := s.store.SetApprovalOverride(ctx, sc, repo.ApprovalOverride{Prefix: prefix, Mode: mode}); err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.WsUpdate, WsID: sc.WsID, TargetType: "workspace", TargetID: sc.WsID,
		Meta: map[string]any{"changed": []string{"approval_overrides"}}})
	return nil
}

func (s *Service) DeleteApprovalOverride(ctx context.Context, sc repo.Scope, prefix string) error {
	if err := s.store.DeleteApprovalOverride(ctx, sc, prefix); err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.WsUpdate, WsID: sc.WsID, TargetType: "workspace", TargetID: sc.WsID,
		Meta: map[string]any{"changed": []string{"approval_overrides"}}})
	return nil
}

// Publish publica una revisión aprobada: la de una propuesta (§5.2). El autor de la revisión es
// el de la propuesta y w.Merge dice quién la aprobó. No pasa por las reglas de MustPropose: la
// aprobación es justamente lo que esas reglas piden.
func (s *Service) Publish(ctx context.Context, sc repo.Scope, base int, w repo.PageWrite) (string, int, error) {
	return s.save(ctx, sc, w.AuthorID, base, w)
}
