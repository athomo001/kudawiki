// Package workspace tiene las reglas de áreas y miembros (SPEC §3.1).
package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
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
	// ErrManagedByDirectory: el rol viene de un grupo del directorio y se cambia ahí (§8.3.2).
	ErrManagedByDirectory = errors.New("managed_by_directory")
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
	RemoveMember(ctx context.Context, s repo.Scope, userID, receiver string) (repo.Transfer, error)
	OldestAdmin(ctx context.Context, s repo.Scope, except string) (string, error)
	UserByID(ctx context.Context, id string) (repo.User, error)
	AddNotifications(ctx context.Context, wsID string, ns []repo.NewNotification) error
	// Membresías que da el directorio (§8.3.2).
	MemberSourceOf(ctx context.Context, s repo.Scope, userID string) (repo.MemberSource, error)
	MemberSources(ctx context.Context, userID string) ([]repo.MemberSource, error)
	SetLDAPRole(ctx context.Context, s repo.Scope, userID, role, mappingID string) error
	DropLDAPRole(ctx context.Context, s repo.Scope, userID string) error
	DropManualRole(ctx context.Context, s repo.Scope, userID string) error
	DetachLDAPRole(ctx context.Context, s repo.Scope, userID string) error
}

// Actor es quien hace el cambio.
type Actor struct {
	UserID     string
	Superadmin bool
}

type Service struct {
	store store
	audit *audit.Recorder
	log   *slog.Logger
}

func NewService(s store, rec *audit.Recorder, log *slog.Logger) *Service {
	return &Service{store: s, audit: rec, log: log}
}

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
	// Marca de agua en N2 (§4.15): apagada por defecto; en N3 y sellados va siempre.
	WatermarkN2 *bool
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
	if in.WatermarkN2 != nil && *in.WatermarkN2 != w.WatermarkN2 {
		w.WatermarkN2, changed = *in.WatermarkN2, append(changed, "watermark_n2")
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
	src, err := s.store.MemberSourceOf(ctx, sc, u.ID)
	if err != nil {
		return err
	}
	// Una membresía que solo da el directorio es de solo lectura para el admin del área: se
	// cambia el grupo en el directorio. El superadmin sí puede sumarle un rol a mano (§8.3.2).
	if src.ManualRole == "" && src.LDAPRole != "" && !a.Superadmin {
		return ErrManagedByDirectory
	}
	if err := s.store.SetMember(ctx, sc, u.ID, role); err != nil {
		return err
	}
	after := higher(role, src.LDAPRole)
	e := audit.Event{Action: audit.WsMemberAdd, WsID: sc.WsID, TargetType: "user", TargetID: u.ID, Meta: map[string]any{"role": after}}
	switch src.Role {
	case after:
		return nil // no cambió nada
	case "":
	default:
		e.Action, e.Meta["from"] = audit.WsMemberRole, src.Role
	}
	s.audit.Record(ctx, e)
	s.roleNotice(ctx, sc.WsID, a.UserID, u.ID, after)
	return nil
}

// roleNotice avisa a quien recibió un rol, o uno distinto, en el área (§12.1, de la cuenta: no se
// apaga). Solo los cambios que hace una persona: lo que da el directorio se ve en el área como
// "Gestionado por AD". Salir del área no avisa: la bandeja solo muestra avisos de áreas donde la
// persona está. Un cambio nuevo sin leer reemplaza al anterior (group_key por área).
func (s *Service) roleNotice(ctx context.Context, wsID, actorID, userID, role string) {
	if userID == actorID {
		return
	}
	var name string
	if u, err := s.store.UserByID(ctx, actorID); err == nil {
		name = u.FullName
	}
	p, _ := json.Marshal(map[string]string{"actor": name, "role": role})
	err := s.store.AddNotifications(ctx, wsID, []repo.NewNotification{{UserID: userID, Kind: "role_changed", Category: "account",
		GroupKey: "role:" + wsID, Payload: string(p)}})
	if err != nil {
		// El rol ya cambió y quedó en la auditoría; sin el aviso, la persona lo ve igual en el área.
		s.log.Error("role notice failed", "workspace", wsID, "err", err)
	}
}

// higher es el rol más alto de los dos; "" cuenta como ninguno.
func higher(a, b string) string {
	if repo.RoleRank(b) > repo.RoleRank(a) {
		return b
	}
	return a
}

func (s *Service) RemoveMember(ctx context.Context, a Actor, sc repo.Scope, userID string) error {
	if err := s.authorize(ctx, a, sc, userID, ""); err != nil {
		return err
	}
	src, err := s.store.MemberSourceOf(ctx, sc, userID)
	if err != nil || src.Role == "" {
		return err // quitar a alguien que no es miembro no hace nada y no se registra
	}
	before := src.Role
	// Quien está por un grupo del directorio sigue en el área: se le quita solo lo que se le dio a
	// mano. Si no tiene nada manual, no hay qué quitar aquí; se cambia el grupo en el directorio.
	if src.LDAPRole != "" {
		if src.ManualRole == "" {
			return ErrManagedByDirectory
		}
		if err := s.store.DropManualRole(ctx, sc, userID); err != nil {
			return err
		}
		if src.LDAPRole != before {
			s.audit.Record(ctx, audit.Event{Action: audit.WsMemberRole, WsID: sc.WsID, TargetType: "user", TargetID: userID,
				Meta: map[string]any{"from": before, "role": src.LDAPRole, "source": "ldap_group"}})
			s.roleNotice(ctx, sc.WsID, a.UserID, userID, src.LDAPRole)
		}
		return nil
	}
	// Lo suyo en esta área pasa al admin más antiguo que queda; si no queda ninguno, a quien lo
	// sacó, que entonces es el superadmin (§8.4).
	receiver, err := s.store.OldestAdmin(ctx, sc, userID)
	if err != nil {
		return err
	}
	if receiver == "" {
		receiver = a.UserID
	}
	t, err := s.store.RemoveMember(ctx, sc, userID, receiver)
	if err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.WsMemberRemove, WsID: sc.WsID, TargetType: "user", TargetID: userID,
		Meta: map[string]any{"role": before}})
	s.transferred(ctx, t, userID, a.UserID)
	return nil
}

// Detach desancla una membresía del directorio (§8.3.2): lo que daba un grupo pasa a ser manual,
// como excepción que sobrevive a que la persona salga del grupo. Solo el superadmin: el admin
// del área no decide sobre lo que viene del directorio. Si sigue en el grupo, el directorio vuelve
// a marcar su parte en la próxima sincronización; el rol manual queda.
func (s *Service) Detach(ctx context.Context, a Actor, sc repo.Scope, userID string) error {
	if !a.Superadmin {
		return ErrForbidden
	}
	src, err := s.store.MemberSourceOf(ctx, sc, userID)
	if err != nil {
		return err
	}
	if src.LDAPRole == "" {
		return nil // no hay nada del directorio que desanclar
	}
	if err := s.store.DetachLDAPRole(ctx, sc, userID); err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.LDAPMembershipDetach, WsID: sc.WsID, TargetType: "user", TargetID: userID,
		Meta: map[string]any{"role": src.Role, "mapping_id": src.MappingID}})
	return nil
}

// transferred registra cada página que cambió de dueño y avisa a quien la recibió, salvo que
// sea quien hizo el cambio: ya lo sabe.
func (s *Service) transferred(ctx context.Context, t repo.Transfer, from, actorID string) {
	for _, id := range t.PageIDs {
		s.audit.Record(ctx, audit.Event{Action: audit.PageOwnerTransfer, WsID: t.WsID, TargetType: "page", TargetID: id,
			Meta: map[string]any{"from": from, "to": t.To}})
	}
	if t.Empty() || t.To == actorID {
		return
	}
	u, err := s.store.UserByID(ctx, from)
	if err == nil {
		err = s.store.AddNotifications(ctx, t.WsID, []repo.NewNotification{t.Notice(u.FullName)})
	}
	if err != nil {
		// El cambio de dueño ya está hecho y auditado; sin el aviso, la persona lo ve igual en el área.
		s.log.Error("ownership notice failed", "workspace", t.WsID, "err", err)
	}
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

// SyncDirectory deja las membresías de una persona como las dan las reglas de grupos del
// directorio (§8.3.2, §8.3.3): suma o cambia el rol del directorio en las áreas de grants y lo
// quita en las demás. Nunca toca lo que se dio a mano. Quien sale de un área que solo tenía por
// un grupo deja lo suyo al admin más antiguo, como al quitarlo a mano (§8.4); si el área no tiene
// admin, lo suyo queda donde está. Lo hace el directorio: la auditoría no tiene actor.
func (s *Service) SyncDirectory(ctx context.Context, userID string, grants []repo.DirectoryGrant) error {
	current, err := s.store.MemberSources(ctx, userID)
	if err != nil {
		return err
	}
	have := map[string]repo.MemberSource{}
	for _, m := range current {
		have[m.WsID] = m
	}
	granted := map[string]bool{}
	for _, g := range grants {
		granted[g.WsID] = true
		before := have[g.WsID]
		if before.LDAPRole == g.Role && before.MappingID == g.MappingID {
			continue
		}
		if err := s.store.SetLDAPRole(ctx, repo.Scope{WsID: g.WsID}, userID, g.Role, g.MappingID); err != nil {
			return err
		}
		after := higher(before.ManualRole, g.Role)
		e := audit.Event{Action: audit.WsMemberAdd, WsID: g.WsID, TargetType: "user", TargetID: userID, System: "ldap",
			Meta: map[string]any{"role": after, "source": "ldap_group", "group": g.Group}}
		switch before.Role {
		case after:
			continue // cambió la regla que lo da, no el rol
		case "":
		default:
			e.Action, e.Meta["from"] = audit.WsMemberRole, before.Role
		}
		s.audit.Record(ctx, e)
	}
	for _, m := range current {
		if m.LDAPRole == "" || granted[m.WsID] {
			continue
		}
		sc := repo.Scope{WsID: m.WsID}
		if m.ManualRole != "" {
			if err := s.store.DropLDAPRole(ctx, sc, userID); err != nil {
				return err
			}
			if m.ManualRole != m.Role {
				s.audit.Record(ctx, audit.Event{Action: audit.WsMemberRole, WsID: m.WsID, TargetType: "user", TargetID: userID, System: "ldap",
					Meta: map[string]any{"from": m.Role, "role": m.ManualRole, "source": "ldap_group"}})
			}
			continue
		}
		receiver, err := s.store.OldestAdmin(ctx, sc, userID)
		if err != nil {
			return err
		}
		t, err := s.store.RemoveMember(ctx, sc, userID, receiver)
		if err != nil {
			return err
		}
		s.audit.Record(ctx, audit.Event{Action: audit.WsMemberRemove, WsID: m.WsID, TargetType: "user", TargetID: userID, System: "ldap",
			Meta: map[string]any{"role": m.Role, "source": "ldap_group"}})
		s.transferred(ctx, t, userID, "")
	}
	return nil
}
