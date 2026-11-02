package ldap

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"strings"

	goldap "github.com/go-ldap/ldap/v3"
	"github.com/oklog/ulid/v2"

	"github.com/athomo001/kudawiki/internal/auth"
	"github.com/athomo001/kudawiki/internal/repo"
)

// Login con el directorio (§8.6.2, §8.6.3). Las razones de cada rechazo van al log técnico (sin
// la contraseña); a la persona solo le llega el error genérico, salvo los casos de auth que no
// revelan nada de una cuenta ajena.

// areaMembers aplica a las áreas lo que dan las reglas; lo implementa workspace.
type areaMembers interface {
	SyncDirectory(ctx context.Context, userID string, grants []repo.DirectoryGrant) error
}

// adData es el código "data" que AD pone en el mensaje de un bind fallido (§8.6.2).
var adData = regexp.MustCompile(`(?i)\bdata ([0-9a-f]{3,8})\b`)

// Available: el login acepta un usuario del directorio (ana, EMPRESA\ana), no solo un correo.
// No descifra la contraseña de servicio: se llama en cada carga de la página.
func (s *Service) Available(ctx context.Context) bool {
	c, err := s.settings(ctx, nil, false)
	return err == nil && c.Enabled && c.complete() == nil
}

// PasswordHelpURL es adónde cambia o recupera su contraseña quien entra con el directorio (§8.6.3):
// la wiki no la cambia. Vacío si no hay o si el directorio no está activo.
func (s *Service) PasswordHelpURL(ctx context.Context) string {
	c, err := s.settings(ctx, nil, false)
	if err != nil || !c.Enabled || c.complete() != nil {
		return ""
	}
	return c.PasswordHelpURL
}

// Login valida a la persona y le da su cuenta y sus áreas. Un login que no está en el directorio,
// una contraseña equivocada o una cuenta deshabilitada son auth.ErrInvalidCredentials.
func (s *Service) Login(ctx context.Context, login, password string) (repo.User, error) {
	// Contraseña vacía: rechazo inmediato. La mayoría de los servidores la tratan como un bind
	// anónimo exitoso (RFC 4513 §5.1.2): nunca debe llegar al directorio.
	if password == "" {
		return repo.User{}, auth.ErrInvalidCredentials
	}
	c, err := s.settings(ctx, nil, true)
	if err != nil {
		return repo.User{}, err
	}
	if !c.Enabled || c.complete() != nil {
		return repo.User{}, auth.ErrInvalidCredentials
	}
	c = c.effective()
	login, ok := normalizeLogin(c, login)
	if !ok {
		return repo.User{}, auth.ErrInvalidCredentials
	}
	conn, url, err := s.connect(ctx, c)
	if err != nil {
		s.log.Warn("ldap login: directory unavailable", "err", err)
		s.failed("login_unavailable")
		return repo.User{}, auth.ErrDirectoryUnavailable
	}
	defer conn.Close()
	filter := userFilter(c, login)
	s.debug("login search", "login", login, "filter", filter, "base", c.UserBaseDN, "server", url)
	e, err := s.findUser(conn, c, filter, c.UserBaseDN, goldap.ScopeWholeSubtree)
	if err != nil {
		s.log.Info("ldap login rejected", "login", login, "reason", err)
		return repo.User{}, loginError(err)
	}
	s.debug("login found", "login", login, "dn", e.DN)
	if err := s.bind(ctx, c, url, e.DN, password); err != nil {
		s.log.Info("ldap login rejected", "login", login, "dn", e.DN, "reason", err)
		if errors.Is(err, auth.ErrDirectoryUnavailable) {
			s.failed("login_unavailable")
		}
		return repo.User{}, err
	}
	p, err := readProfile(c, e)
	if err != nil {
		s.log.Warn("ldap login rejected", "login", login, "dn", e.DN, "reason", err)
		return repo.User{}, auth.ErrInvalidCredentials
	}
	// Solo entra quien está en un grupo con regla (§8.6.3): sin eso no se crea la cuenta.
	groups, err := memberships(conn, c, e)
	if err != nil {
		s.log.Warn("ldap login: groups", "dn", e.DN, "err", err)
		return repo.User{}, auth.ErrDirectoryUnavailable
	}
	mappings, err := s.store.LDAPMappings(ctx)
	if err != nil {
		return repo.User{}, err
	}
	gs := grants(c, groups, mappings)
	s.debug("login groups", "dn", e.DN, "groups", len(groups), "areas", len(gs))
	if len(gs) == 0 {
		s.log.Info("ldap login rejected", "login", login, "dn", e.DN, "reason", "no mapped group")
		return repo.User{}, auth.ErrNoDirectoryAccess
	}
	u, err := s.provision(ctx, p)
	if err != nil {
		return repo.User{}, err
	}
	s.photo(ctx, u.ID, p.Photo)
	if err := s.members.SyncDirectory(ctx, u.ID, directoryGrants(gs)); err != nil {
		return repo.User{}, err
	}
	s.loggedIn()
	return s.store.UserByID(ctx, u.ID)
}

func directoryGrants(gs []Grant) []repo.DirectoryGrant {
	out := make([]repo.DirectoryGrant, 0, len(gs))
	for _, g := range gs {
		out = append(out, repo.DirectoryGrant{WsID: g.WsID, Role: g.Role, MappingID: g.MappingID, Group: strings.Join(g.Via, ", ")})
	}
	return out
}

// errRejected es un rechazo por la entrada misma (no está, hay dos, deshabilitada): para la
// persona, el error genérico.
type errRejected string

func (e errRejected) Error() string { return string(e) }

func loginError(err error) error {
	var r errRejected
	if errors.As(err, &r) {
		return auth.ErrInvalidCredentials
	}
	return auth.ErrDirectoryUnavailable
}

// findUser busca exactamente una persona habilitada con el filtro. Dos entradas no identifican a
// una persona, y autenticar "la primera" sería autenticar a quien el directorio devolvió primero.
func (s *Service) findUser(conn *goldap.Conn, c config, filter, base string, scope int) (*goldap.Entry, error) {
	res, err := conn.Search(goldap.NewSearchRequest(base, scope, goldap.NeverDerefAliases, 2, 0, false, filter, profileAttrs(c), nil))
	switch {
	case goldap.IsErrorWithCode(err, goldap.LDAPResultSizeLimitExceeded):
		return nil, errRejected("ambiguous")
	case goldap.IsErrorWithCode(err, goldap.LDAPResultNoSuchObject):
		return nil, errRejected("not found")
	case err != nil:
		return nil, err
	case len(res.Entries) == 0:
		return nil, errRejected("not found")
	case len(res.Entries) > 1:
		return nil, errRejected("ambiguous")
	}
	e := res.Entries[0]
	// Fuera de user_base_dn no entra, aunque el filtro la encuentre (§8.6.2).
	root, _ := goldap.ParseDN(c.UserBaseDN)
	dn, err := goldap.ParseDN(e.DN)
	if err != nil || root == nil || !(root.AncestorOfFold(dn) || root.EqualFold(dn)) {
		return nil, errRejected("outside user_base_dn")
	}
	if c.DisabledFilter != "" {
		res, err := conn.Search(goldap.NewSearchRequest(e.DN, goldap.ScopeBaseObject, goldap.NeverDerefAliases, 1, 0, false,
			c.DisabledFilter, []string{"1.1"}, nil))
		if err != nil {
			return nil, err
		}
		if len(res.Entries) > 0 {
			return nil, errRejected("disabled")
		}
	}
	return e, nil
}

// bind prueba la contraseña en una conexión nueva y desechable: un bind cambia los permisos de
// la conexión, y la de la cuenta de servicio sigue leyendo los grupos. Es el único lugar por
// donde pasa la contraseña.
func (s *Service) bind(ctx context.Context, c config, url, dn, password string) error {
	conn, err := c.dial(ctx, url, &peer{})
	if err != nil {
		return auth.ErrDirectoryUnavailable
	}
	defer conn.Close()
	err = conn.Bind(dn, password)
	switch {
	case err == nil:
		return nil
	case goldap.IsErrorWithCode(err, goldap.LDAPResultInvalidCredentials):
		// AD responde "credenciales inválidas" también a cuentas bloqueadas o vencidas y lo
		// distingue con el código data. Solo 532 y 773 se le dicen a la persona: AD los devuelve
		// únicamente si la contraseña era correcta.
		code := ""
		if m := adData.FindStringSubmatch(err.Error()); m != nil {
			code = strings.ToLower(m[1])
		}
		s.log.Info("ldap bind rejected", "dn", dn, "ad_code", code)
		s.debug("bind rejected", "dn", dn, "ad_code", code)
		if c.Flavor == "ad" && (code == "532" || code == "773") {
			return auth.ErrPasswordExpired
		}
		return auth.ErrInvalidCredentials
	case isNetwork(err):
		return auth.ErrDirectoryUnavailable
	}
	s.log.Warn("ldap bind failed", "dn", dn, "err", err)
	return auth.ErrInvalidCredentials
}

// profile es lo que la wiki toma de una entrada del directorio.
type profile struct {
	DN, GUID, Email, Name string
	Photo                 []byte
}

// profileAttrs son los atributos que se piden de una persona: los mapeados y uid (para
// group_member_filter con {uid}).
func profileAttrs(c config) []string {
	attrs := []string{c.Attrs.Email, c.Attrs.Name, "uid"}
	for _, a := range []string{c.Attrs.GUID, c.Attrs.Photo} {
		if a != "" {
			attrs = append(attrs, a)
		}
	}
	return attrs
}

// readProfile lee la identidad, el correo, el nombre y la foto. Sin identidad o sin correo no hay
// cuenta: es un error de configuración que Probar un usuario muestra (§8.6.5).
func readProfile(c config, e *goldap.Entry) (profile, error) {
	p := profile{DN: e.DN, GUID: e.DN}
	if c.Attrs.GUID != "" {
		if p.GUID = formatGUID(c.Attrs.GUID, rawValue(e, c.Attrs.GUID)); p.GUID == "" {
			return profile{}, errors.New("missing identity attribute " + c.Attrs.GUID)
		}
	}
	p.Email = strings.ToLower(strings.TrimSpace(first(values(e, c.Attrs.Email))))
	if p.Email == "" || !strings.Contains(p.Email, "@") {
		return profile{}, errors.New("missing email attribute " + c.Attrs.Email)
	}
	if p.Name = strings.TrimSpace(first(values(e, c.Attrs.Name))); p.Name == "" {
		p.Name = p.Email
	}
	if c.Attrs.Photo != "" {
		p.Photo = rawValue(e, c.Attrs.Photo)
	}
	return p, nil
}

// provision da la cuenta (JIT, §8.3.3) o la pone al día con el directorio.
func (s *Service) provision(ctx context.Context, p profile) (repo.User, error) {
	u, err := s.store.UserByLDAPGUID(ctx, p.GUID)
	if errors.Is(err, repo.ErrNotFound) {
		return s.create(ctx, p)
	}
	if err != nil {
		return repo.User{}, err
	}
	// Una cuenta desactivada en la wiki (una baja, §8.4) no vuelve a entrar sola.
	if !u.IsActive {
		s.log.Info("ldap login rejected", "dn", p.DN, "reason", "account inactive in the wiki")
		return repo.User{}, auth.ErrInvalidCredentials
	}
	if err := s.refresh(ctx, u, p); err != nil {
		return repo.User{}, err
	}
	return u, nil
}

// create da de alta una cuenta del directorio: al entrar por primera vez (JIT) o antes, en la
// sincronización (§8.3.3). Otra cuenta con el mismo correo no se fusiona sola: quien controle el
// correo en el directorio podría apropiarse de una cuenta local, incluida la de un admin (§8.6.3).
func (s *Service) create(ctx context.Context, p profile) (repo.User, error) {
	if _, err := s.store.UserByEmail(ctx, p.Email); err == nil {
		s.log.Info("ldap account not created", "dn", p.DN, "reason", "another account has this email")
		return repo.User{}, auth.ErrAccountConflict
	} else if !errors.Is(err, repo.ErrNotFound) {
		return repo.User{}, err
	}
	u := repo.User{ID: ulid.Make().String(), Email: p.Email, FullName: p.Name, LDAPGUID: p.GUID, LDAPDN: p.DN,
		Locale: "es", IsActive: true, AuthSource: "ldap"}
	err := s.store.CreateLDAPUser(ctx, u, p.DN)
	if errors.Is(err, repo.ErrConflict) {
		return repo.User{}, auth.ErrAccountConflict
	}
	return u, err
}

// refresh trae del directorio el correo, el nombre y el DN, si cambiaron.
func (s *Service) refresh(ctx context.Context, u repo.User, p profile) error {
	if u.Email == p.Email && u.FullName == p.Name && u.LDAPDN == p.DN {
		return nil
	}
	err := s.store.UpdateLDAPUser(ctx, u.ID, p.Email, p.Name, p.DN)
	if errors.Is(err, repo.ErrConflict) {
		s.log.Info("ldap account not updated", "dn", p.DN, "reason", "another account has the new email")
		return auth.ErrAccountConflict
	}
	return err
}

// photo guarda la foto del directorio, re-codificada como la que sube la persona (§8.6.3). Si el
// directorio no tiene, queda la que haya subido ella. Una foto que no se puede leer no impide
// entrar: queda en el log.
func (s *Service) photo(ctx context.Context, userID string, raw []byte) {
	if len(raw) == 0 {
		return
	}
	png, err := auth.AvatarPNG(raw)
	if err != nil {
		s.log.Warn("ldap photo ignored", "user", userID, "err", err)
		return
	}
	if cur, err := s.store.Avatar(ctx, userID); err == nil && bytes.Equal(cur, png) {
		return
	}
	if err := s.store.SetAvatar(ctx, userID, png); err != nil {
		s.log.Error("ldap photo not saved", "user", userID, "err", err)
	}
}

// findByIdentity busca a una persona por su identidad guardada, no por el DN (pudo moverse), con
// las mismas condiciones que el login: dentro de user_base_dn y habilitada.
func (s *Service) findByIdentity(conn *goldap.Conn, c config, guid string) (*goldap.Entry, error) {
	switch {
	case c.Attrs.GUID == "":
		// Sin atributo de identidad, la identidad es el DN.
		return s.findUser(conn, c, c.UserFilter, guid, goldap.ScopeBaseObject)
	case strings.EqualFold(c.Attrs.GUID, "objectGUID"):
		b, ok := guidBytes(guid)
		if !ok {
			return nil, errRejected("not found")
		}
		return s.findUser(conn, c, "(&"+c.UserFilter+"(objectGUID="+escapeBytes(b)+"))", c.UserBaseDN, goldap.ScopeWholeSubtree)
	}
	return s.findUser(conn, c, "(&"+c.UserFilter+"("+c.Attrs.GUID+"="+goldap.EscapeFilter(guid)+"))", c.UserBaseDN, goldap.ScopeWholeSubtree)
}

// VerifyPassword es la reautenticación de una cuenta del directorio (§8.2): se busca por su
// identidad, no por el DN guardado (pudo moverse), y se prueba la contraseña con un bind.
func (s *Service) VerifyPassword(ctx context.Context, u repo.User, password string) error {
	if password == "" {
		return auth.ErrWrongPassword
	}
	c, err := s.settings(ctx, nil, true)
	if err != nil {
		return err
	}
	if !c.Enabled || c.complete() != nil {
		return auth.ErrDirectoryUnavailable
	}
	c = c.effective()
	conn, url, err := s.connect(ctx, c)
	if err != nil {
		return auth.ErrDirectoryUnavailable
	}
	defer conn.Close()
	e, err := s.findByIdentity(conn, c, u.LDAPGUID)
	if err != nil {
		s.log.Info("ldap reauth rejected", "user", u.ID, "reason", err)
		if loginError(err) == auth.ErrInvalidCredentials {
			return auth.ErrWrongPassword
		}
		return auth.ErrDirectoryUnavailable
	}
	err = s.bind(ctx, c, url, e.DN, password)
	if errors.Is(err, auth.ErrInvalidCredentials) || errors.Is(err, auth.ErrPasswordExpired) {
		return auth.ErrWrongPassword
	}
	return err
}
