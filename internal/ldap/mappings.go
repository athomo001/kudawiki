package ldap

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	goldap "github.com/go-ldap/ldap/v3"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/repo"
)

// Reglas de mapeo grupo → (área, rol) (§8.3.1). Las administra solo el superadmin, desde el
// panel: las áreas se crean en tiempo de ejecución. Usan la conexión guardada, no el formulario.

// ErrMappingExists: el grupo ya tiene una regla en esa área; se le cambia el rol a esa.
var ErrMappingExists = errors.New("ldap_mapping_exists")

// MappingInput es una regla por crear o por previsualizar. Del grupo llega solo su identidad: el
// DN y el nombre se leen del directorio.
type MappingInput struct {
	GroupGUID string `json:"group_guid"`
	WsID      string `json:"workspace_id"`
	Role      string `json:"role"`
}

// Preview es lo que la regla haría, para confirmarla antes de aplicarla (§8.3.4). Members cuenta
// a las personas habilitadas del grupo; de ellas, Accounts ya entraron a la wiki, y de esas Added
// entran al área y Raised suben de rol. Las demás reciben el rol en su primer ingreso.
type Preview struct {
	Group     Group  `json:"group"`
	Role      string `json:"role"`
	Members   int    `json:"members"`
	Truncated bool   `json:"truncated"`
	Accounts  int    `json:"accounts"`
	Added     int    `json:"added"`
	Raised    int    `json:"raised"`
}

// groupLimit: cuántos grupos devuelve una búsqueda; con más, se pide escribir más del nombre.
const groupLimit = 50

// directory es la conexión guardada, completa y con donde buscar grupos.
func (s *Service) directory(ctx context.Context) (*goldap.Conn, config, error) {
	c, err := s.settings(ctx, nil, true)
	if err != nil {
		return nil, config{}, err
	}
	if err := c.complete(); err != nil {
		return nil, config{}, err
	}
	if c.GroupBaseDN == "" {
		return nil, config{}, fieldErr("auth.ldap.group_base_dn", ErrIncomplete)
	}
	c = c.effective()
	conn, _, err := s.connect(ctx, c)
	if err != nil {
		return nil, config{}, &UnavailableError{err}
	}
	return conn, c, nil
}

// Groups busca grupos por nombre bajo group_base_dn, para elegir el de una regla.
func (s *Service) Groups(ctx context.Context, q string) ([]Group, bool, error) {
	q = strings.TrimSpace(q)
	if utf8.RuneCountInString(q) > 256 || strings.ContainsFunc(q, func(r rune) bool { return r < ' ' }) {
		return nil, false, &FieldError{Field: "q", Err: ErrInvalid}
	}
	conn, c, err := s.directory(ctx)
	if err != nil {
		return nil, false, err
	}
	defer conn.Close()
	groups, truncated, err := searchGroups(conn, c, q, groupLimit)
	if err != nil {
		return nil, false, &UnavailableError{err}
	}
	if groups == nil {
		groups = []Group{}
	}
	return groups, truncated, nil
}

func (s *Service) Mappings(ctx context.Context) ([]repo.LDAPMapping, error) {
	return s.store.LDAPMappings(ctx)
}

// checkMapping valida el área y el rol. Solo áreas existentes: el mapeo nunca crea áreas, y un espacio
// personal no es un área (§8.3.1, §7). El rol nunca es superadmin: no está entre los roles.
func (s *Service) checkMapping(ctx context.Context, in MappingInput) error {
	if repo.RoleRank(in.Role) == 0 {
		return &FieldError{Field: "role", Err: ErrInvalid}
	}
	ws, err := s.store.WorkspaceByID(ctx, in.WsID)
	if errors.Is(err, repo.ErrNotFound) || (err == nil && ws.Kind != "area") {
		return &FieldError{Field: "workspace_id", Err: ErrInvalid}
	}
	return err
}

func (s *Service) group(conn *goldap.Conn, c config, guid string) (Group, error) {
	g, err := findGroup(conn, c, strings.TrimSpace(guid))
	if errors.Is(err, ErrGroupNotFound) {
		return Group{}, &FieldError{Field: "group_guid", Err: err}
	}
	if err != nil {
		return Group{}, &UnavailableError{err}
	}
	return g, nil
}

// Preview dice a quién le daría acceso la regla, sin guardarla.
func (s *Service) Preview(ctx context.Context, in MappingInput) (Preview, error) {
	if err := s.checkMapping(ctx, in); err != nil {
		return Preview{}, err
	}
	conn, c, err := s.directory(ctx)
	if err != nil {
		return Preview{}, err
	}
	defer conn.Close()
	g, err := s.group(conn, c, in.GroupGUID)
	if err != nil {
		return Preview{}, err
	}
	entries, truncated, err := members(conn, c, g, idAttrs(c), maxVisits)
	if err != nil {
		return Preview{}, &UnavailableError{err}
	}
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = identity(c, e)
	}
	accounts, err := s.store.LDAPAccounts(ctx, in.WsID, ids)
	if err != nil {
		return Preview{}, err
	}
	p := Preview{Group: g, Role: in.Role, Members: len(ids), Truncated: truncated, Accounts: len(accounts)}
	for _, a := range accounts {
		switch {
		case a.Role == "":
			p.Added++
		case repo.RoleRank(a.Role) < repo.RoleRank(in.Role):
			p.Raised++
		}
	}
	return p, nil
}

// CreateMapping guarda la regla con el grupo tal como está en el directorio.
func (s *Service) CreateMapping(ctx context.Context, actorID string, in MappingInput) (repo.LDAPMapping, error) {
	if err := s.checkMapping(ctx, in); err != nil {
		return repo.LDAPMapping{}, err
	}
	conn, c, err := s.directory(ctx)
	if err != nil {
		return repo.LDAPMapping{}, err
	}
	g, err := s.group(conn, c, in.GroupGUID)
	conn.Close()
	if err != nil {
		return repo.LDAPMapping{}, err
	}
	id, err := s.store.CreateLDAPMapping(ctx, repo.LDAPMapping{GroupGUID: g.GUID, GroupSID: g.SID, GroupDN: g.DN, GroupName: g.Name,
		WsID: in.WsID, Role: in.Role, CreatedBy: actorID})
	if errors.Is(err, repo.ErrConflict) {
		return repo.LDAPMapping{}, ErrMappingExists
	}
	if err != nil {
		return repo.LDAPMapping{}, err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.LDAPMappingCreate, WsID: in.WsID, TargetType: "ldap_mapping", TargetID: id,
		Meta: map[string]any{"group": g.Name, "role": in.Role}})
	return s.store.LDAPMappingByID(ctx, id)
}

// UpdateMapping cambia el rol que da la regla.
func (s *Service) UpdateMapping(ctx context.Context, id, role string) (repo.LDAPMapping, error) {
	if repo.RoleRank(role) == 0 {
		return repo.LDAPMapping{}, &FieldError{Field: "role", Err: ErrInvalid}
	}
	m, err := s.store.LDAPMappingByID(ctx, id)
	if err != nil || m.Role == role {
		return m, err
	}
	if err := s.store.SetLDAPMappingRole(ctx, id, role); err != nil {
		return repo.LDAPMapping{}, err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.LDAPMappingUpdate, WsID: m.WsID, TargetType: "ldap_mapping", TargetID: id,
		Meta: map[string]any{"group": m.GroupName, "from": m.Role, "to": role}})
	m.Role = role
	return m, nil
}

func (s *Service) DeleteMapping(ctx context.Context, id string) error {
	m, err := s.store.LDAPMappingByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.store.DeleteLDAPMapping(ctx, id); err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.LDAPMappingDelete, WsID: m.WsID, TargetType: "ldap_mapping", TargetID: id,
		Meta: map[string]any{"group": m.GroupName, "role": m.Role}})
	return nil
}
