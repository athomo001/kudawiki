// Package workspace tiene las reglas de áreas y miembros (SPEC §3.1).
package workspace

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/oklog/ulid/v2"
)

var (
	ErrInvalidArea  = errors.New("invalid_area")
	ErrSlugTaken    = errors.New("slug_taken")
	ErrInvalidRole  = errors.New("invalid_role")
	ErrUserNotFound = errors.New("user_not_found")
	ErrForbidden    = errors.New("forbidden")
)

// El slug va en las URL (/w/{slug}/…): minúsculas, números y guiones, de 2 a 40 caracteres.
var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,38}[a-z0-9]$`)

type store interface {
	CreateWorkspace(ctx context.Context, w repo.Workspace) error
	WorkspaceByID(ctx context.Context, id string) (repo.Workspace, error)
	UpdateWorkspace(ctx context.Context, s repo.Scope, w repo.Workspace) error
	UserByEmail(ctx context.Context, email string) (repo.User, error)
	MemberRole(ctx context.Context, s repo.Scope, userID string) (string, error)
	SetMember(ctx context.Context, s repo.Scope, userID, role string) error
	RemoveMember(ctx context.Context, s repo.Scope, userID string) error
}

// Actor es quien hace el cambio.
type Actor struct {
	UserID     string
	Superadmin bool
}

type Service struct {
	store store
	audit *audit.Recorder
}

func NewService(s store, rec *audit.Recorder) *Service { return &Service{store: s, audit: rec} }

// Create es solo del superadmin; la ruta ya lo exige.
func (s *Service) Create(ctx context.Context, slug, name string) (repo.Workspace, error) {
	w := repo.Workspace{ID: ulid.Make().String(), Slug: strings.TrimSpace(slug), Name: strings.TrimSpace(name)}
	if !slugRe.MatchString(w.Slug) || w.Name == "" {
		return repo.Workspace{}, ErrInvalidArea
	}
	err := s.store.CreateWorkspace(ctx, w)
	if errors.Is(err, repo.ErrConflict) {
		return repo.Workspace{}, ErrSlugTaken
	}
	if err != nil {
		return repo.Workspace{}, err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.WsCreate, WsID: w.ID, TargetType: "workspace", TargetID: w.ID})
	return w, nil
}

// maxDescription: la descripción del área es un párrafo, no una página.
const maxDescription = 500

// Settings es lo que el admin del área cambia en su configuración; lo que viene en nil queda
// como estaba.
type Settings struct {
	Name, Description *string
	ApprovalMode      *string // direct | required (§5.1)
	ViewersCanPropose *bool   // §19.5
	// Historial (§4.3). KeepLast y KeepDays: 0 = sin límite; no pueden ir los dos.
	RequireChangeSummary *bool
	KeepLast, KeepDays   *int
	// Vigencia (§4.8): intervalo de revisión por defecto; 0 = las páginas no vencen (§19.11).
	ReviewDays *int
	// Cuestionarios (§4.16.7): encendidos por defecto.
	QuizzesEnabled *bool
}

// validRetention: las últimas N revisiones (hasta 10 000) o las de los últimos N días (hasta
// 100 años), o ninguna de las dos (todas); las dos a la vez no (§4.3).
func validRetention(last, days int) bool {
	return last >= 0 && last <= 10000 && days >= 0 && days <= 36500 && (last == 0 || days == 0)
}

// Update cambia la configuración del área. Solo su admin (la ruta lo exige, §3.1). La auditoría
// dice qué campos cambiaron, no el texto (§11.2).
func (s *Service) Update(ctx context.Context, sc repo.Scope, in Settings) error {
	w, err := s.store.WorkspaceByID(ctx, sc.WsID)
	if err != nil {
		return err
	}
	var changed []string
	if in.Name != nil && strings.TrimSpace(*in.Name) != w.Name {
		w.Name, changed = strings.TrimSpace(*in.Name), append(changed, "name")
	}
	if in.Description != nil && strings.TrimSpace(*in.Description) != w.Description {
		w.Description, changed = strings.TrimSpace(*in.Description), append(changed, "description")
	}
	if in.ApprovalMode != nil && *in.ApprovalMode != w.ApprovalMode {
		w.ApprovalMode, changed = *in.ApprovalMode, append(changed, "approval_mode")
	}
	if in.ViewersCanPropose != nil && *in.ViewersCanPropose != w.ViewersCanPropose {
		w.ViewersCanPropose, changed = *in.ViewersCanPropose, append(changed, "viewers_can_propose")
	}
	if in.RequireChangeSummary != nil && *in.RequireChangeSummary != w.RequireChangeSummary {
		w.RequireChangeSummary, changed = *in.RequireChangeSummary, append(changed, "require_change_summary")
	}
	if in.KeepLast != nil && *in.KeepLast != w.KeepLast {
		w.KeepLast, changed = *in.KeepLast, append(changed, "revisions_keep_last")
	}
	if in.KeepDays != nil && *in.KeepDays != w.KeepDays {
		w.KeepDays, changed = *in.KeepDays, append(changed, "revisions_keep_days")
	}
	if in.ReviewDays != nil && *in.ReviewDays != w.ReviewDays {
		w.ReviewDays, changed = *in.ReviewDays, append(changed, "default_review_days")
	}
	if in.QuizzesEnabled != nil && *in.QuizzesEnabled != w.QuizzesEnabled {
		w.QuizzesEnabled, changed = *in.QuizzesEnabled, append(changed, "quizzes_enabled")
	}
	if w.Name == "" || utf8.RuneCountInString(w.Description) > maxDescription || (w.ApprovalMode != "direct" && w.ApprovalMode != "required") ||
		!validRetention(w.KeepLast, w.KeepDays) || w.ReviewDays < 0 || w.ReviewDays > 3650 {
		return ErrInvalidArea
	}
	if len(changed) == 0 {
		return nil
	}
	if err := s.store.UpdateWorkspace(ctx, sc, w); err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.WsUpdate, WsID: sc.WsID, TargetType: "workspace", TargetID: sc.WsID, Meta: map[string]any{"changed": changed}})
	return nil
}

// SetMember agrega a alguien al área o le cambia el rol.
func (s *Service) SetMember(ctx context.Context, a Actor, sc repo.Scope, email, role string) error {
	if repo.RoleRank(role) == 0 {
		return ErrInvalidRole
	}
	u, err := s.store.UserByEmail(ctx, strings.TrimSpace(email))
	if errors.Is(err, repo.ErrNotFound) || (err == nil && !u.IsActive) {
		return ErrUserNotFound
	}
	if err != nil {
		return err
	}
	if err := s.authorize(ctx, a, sc, u.ID, role); err != nil {
		return err
	}
	before, err := s.store.MemberRole(ctx, sc, u.ID)
	if err != nil {
		return err
	}
	if err := s.store.SetMember(ctx, sc, u.ID, role); err != nil {
		return err
	}
	e := audit.Event{Action: audit.WsMemberAdd, WsID: sc.WsID, TargetType: "user", TargetID: u.ID, Meta: map[string]any{"role": role}}
	switch before {
	case role:
		return nil // no cambió nada
	case "":
	default:
		e.Action, e.Meta["from"] = audit.WsMemberRole, before
	}
	s.audit.Record(ctx, e)
	return nil
}

func (s *Service) RemoveMember(ctx context.Context, a Actor, sc repo.Scope, userID string) error {
	if err := s.authorize(ctx, a, sc, userID, ""); err != nil {
		return err
	}
	before, err := s.store.MemberRole(ctx, sc, userID)
	if err != nil || before == "" {
		return err // quitar a alguien que no es miembro no hace nada y no se registra
	}
	if err := s.store.RemoveMember(ctx, sc, userID); err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.WsMemberRemove, WsID: sc.WsID, TargetType: "user", TargetID: userID,
		Meta: map[string]any{"role": before}})
	return nil
}

// authorize aplica §3.1 y §19.10: el rol admin lo da y lo quita solo el superadmin, así un
// admin de área no nombra cómplices ni saca a otro admin; y nadie toca su propia membresía.
func (s *Service) authorize(ctx context.Context, a Actor, sc repo.Scope, target, newRole string) error {
	if a.Superadmin {
		return nil
	}
	if target == a.UserID || newRole == "admin" {
		return ErrForbidden
	}
	current, err := s.store.MemberRole(ctx, sc, target)
	if err != nil {
		return err
	}
	if current == "admin" {
		return ErrForbidden
	}
	return nil
}
