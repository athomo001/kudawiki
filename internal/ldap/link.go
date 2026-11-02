package ldap

import (
	"context"
	"errors"

	goldap "github.com/go-ldap/ldap/v3"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/auth"
	"github.com/athomo001/kudawiki/internal/repo"
)

// Vincular una cuenta local con el directorio (§8.6.3). Una cuenta del directorio no se fusiona
// sola con una local del mismo correo: quien controle el correo en el directorio podría
// apropiarse de ella. La vincula el superadmin, con reautenticación (la ruta la pide).

var (
	// ErrAlreadyLinked: la cuenta ya es del directorio, o esa persona del directorio ya tiene otra.
	ErrAlreadyLinked = errors.New("ldap_already_linked")
	// ErrLastLocalSuperadmin: siempre queda un superadmin local que entra aunque el directorio no
	// responda (§8.3.4).
	ErrLastLocalSuperadmin = errors.New("last_local_superadmin")
	// ErrNoMappedGroup: sin un grupo con regla no podría entrar, y la sincronización la daría de
	// baja (§8.6.3).
	ErrNoMappedGroup = errors.New("ldap_no_mapped_group")
	ErrUserNotFound  = errors.New("ldap_user_not_found")
	ErrUserInactive  = errors.New("user_inactive")
)

// Link deja la cuenta userID como cuenta del directorio de quien se encuentra con login: pierde
// su contraseña local y sus sesiones, y conserva su contenido y sus áreas. Su correo y su nombre
// pasan a ser los del directorio, y recibe las áreas que dan sus grupos.
func (s *Service) Link(ctx context.Context, userID, login string) (repo.User, error) {
	u, err := s.store.UserByID(ctx, userID)
	if err != nil {
		return repo.User{}, err
	}
	switch {
	case u.AuthSource == "ldap":
		return repo.User{}, ErrAlreadyLinked
	case !u.IsActive:
		return repo.User{}, ErrUserInactive
	case u.IsGlobalAdmin && u.PasswordHash != "":
		n, err := s.store.LocalSuperadmins(ctx)
		if err != nil {
			return repo.User{}, err
		}
		if n <= 1 {
			return repo.User{}, ErrLastLocalSuperadmin
		}
	}
	conn, c, err := s.directory(ctx)
	if err != nil {
		return repo.User{}, err
	}
	defer conn.Close()
	login, ok := normalizeLogin(c, login)
	if !ok {
		return repo.User{}, ErrUserNotFound
	}
	e, err := s.findUser(conn, c, userFilter(c, login), c.UserBaseDN, goldap.ScopeWholeSubtree)
	if err != nil {
		var r errRejected
		if errors.As(err, &r) {
			return repo.User{}, ErrUserNotFound
		}
		return repo.User{}, &UnavailableError{err}
	}
	p, err := readProfile(c, e)
	if err != nil {
		return repo.User{}, ErrUserNotFound
	}
	if other, err := s.store.UserByLDAPGUID(ctx, p.GUID); err == nil && other.ID != u.ID {
		return repo.User{}, ErrAlreadyLinked
	} else if err != nil && !errors.Is(err, repo.ErrNotFound) {
		return repo.User{}, err
	}
	groups, err := memberships(conn, c, e)
	if err != nil {
		return repo.User{}, &UnavailableError{err}
	}
	mappings, err := s.store.LDAPMappings(ctx)
	if err != nil {
		return repo.User{}, err
	}
	gs := grants(c, groups, mappings)
	if len(gs) == 0 {
		return repo.User{}, ErrNoMappedGroup
	}
	closed, err := s.store.LinkLDAPUser(ctx, u.ID, p.GUID, p.DN, p.Email, p.Name)
	if errors.Is(err, repo.ErrConflict) {
		return repo.User{}, auth.ErrAccountConflict // el correo del directorio es de una tercera cuenta
	}
	if err != nil {
		return repo.User{}, err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.LDAPAccountLink, TargetType: "user", TargetID: u.ID,
		Meta: map[string]any{"dn": p.DN, "previous_email": u.Email, "sessions_closed": closed}})
	s.photo(ctx, u.ID, p.Photo)
	if err := s.members.SyncDirectory(ctx, u.ID, directoryGrants(gs)); err != nil {
		return repo.User{}, err
	}
	return s.store.UserByID(ctx, u.ID)
}
