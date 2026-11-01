package ldap

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	goldap "github.com/go-ldap/ldap/v3"

	"github.com/athomo001/kudawiki/internal/audit"
)

// Diagnóstico (§8.6.5): lo más difícil de LDAP es saber por qué falla, así que cada paso dice
// si pasó y, si no, un código que el panel traduce a qué hacer.

// Step es un paso de la prueba de un servidor. Status: ok | warn | fail.
type Step struct {
	Name   string `json:"name"` // tcp | tls | bind | user_base | group_base | users
	Status string `json:"status"`
	Code   string `json:"code,omitempty"`
	Detail string `json:"detail,omitempty"` // lo que contestó el servidor, para el superadmin
}

// Cert es el certificado que presentó el servidor.
type Cert struct {
	Subject  string `json:"subject"`
	Issuer   string `json:"issuer"`
	NotAfter string `json:"not_after"`
	DaysLeft int    `json:"days_left"`
	Version  string `json:"tls_version"`
}

type Server struct {
	URL   string `json:"url"`
	Steps []Step `json:"steps"`
	Cert  *Cert  `json:"cert,omitempty"`
	Users *int   `json:"users,omitempty"` // cuántos cumplen el filtro de usuarios
}

// Suggestion sale del Root DSE del primer servidor que respondió: el superadmin la confirma.
type Suggestion struct {
	Flavor string `json:"flavor"`
	GUID   string `json:"guid"`
	Vendor string `json:"vendor,omitempty"`
}

// Report es el resultado de *Probar conexión*. OK solo si todos los servidores pasan todos los
// pasos: un error de tipeo en el segundo controlador también es un error.
type Report struct {
	OK         bool        `json:"ok"`
	Servers    []Server    `json:"servers"`
	Suggestion *Suggestion `json:"suggestion,omitempty"`
}

// expiringDays: con menos días que esto, el certificado se avisa.
const expiringDays = 30

func (r *Server) add(step, status, code string, err error) {
	st := Step{Name: step, Status: status, Code: code}
	if err != nil {
		st.Detail = err.Error()
	}
	r.Steps = append(r.Steps, st)
}

func (r *Server) search(step string, err error) {
	if err != nil {
		r.add(step, "fail", searchCode(err), err)
	} else {
		r.add(step, "ok", "", nil)
	}
}

func (r *Server) fail(err error) {
	var se *stepError
	if errors.As(err, &se) {
		r.add(se.step, "fail", se.code, se.err)
		return
	}
	r.add("tcp", "fail", "unreachable", err)
}

// TestConnection prueba la configuración que está en el formulario, sin guardarla.
func (s *Service) TestConnection(ctx context.Context, p Patch) (Report, error) {
	c, err := s.candidate(ctx, p)
	if err != nil {
		return Report{}, err
	}
	if err := c.complete(); err != nil {
		return Report{}, err
	}
	r := s.testConnection(ctx, c.config)
	s.audit.Record(ctx, audit.Event{Action: audit.LDAPTest, TargetType: "site", Meta: map[string]any{"kind": "connection", "ok": r.OK}})
	return r, nil
}

func (s *Service) testConnection(ctx context.Context, c config) Report {
	c = c.effective()
	r := Report{OK: true, Servers: []Server{}}
	for _, u := range c.URLs {
		srv := Server{URL: u, Steps: []Step{}}
		sug := s.testServer(ctx, c, &srv, r.Suggestion == nil)
		if sug != nil {
			r.Suggestion = sug
		}
		if slices.ContainsFunc(srv.Steps, func(st Step) bool { return st.Status == "fail" }) {
			r.OK = false
		}
		r.Servers = append(r.Servers, srv)
	}
	return r
}

func (s *Service) testServer(ctx context.Context, c config, srv *Server, suggest bool) *Suggestion {
	var p peer
	conn, err := c.dial(ctx, srv.URL, &p)
	if err != nil {
		var se *stepError
		if errors.As(err, &se) && se.step == "tls" {
			srv.add("tcp", "ok", "", nil)
		}
		srv.Cert = s.cert(p)
		srv.fail(err)
		return nil
	}
	defer conn.Close()
	srv.add("tcp", "ok", "", nil)
	srv.Cert = s.cert(p)
	switch {
	case c.InsecureSkipVerify:
		srv.add("tls", "warn", "tls_insecure", nil)
	case srv.Cert != nil && srv.Cert.DaysLeft < expiringDays:
		srv.add("tls", "warn", "cert_expiring", nil)
	default:
		srv.add("tls", "ok", "", nil)
	}
	if err := conn.Bind(c.BindDN, c.password); err != nil {
		srv.fail(bindError(err))
		return nil
	}
	srv.add("bind", "ok", "", nil)
	var sug *Suggestion
	if suggest {
		sug = rootDSE(conn)
	}
	srv.search("user_base", exists(conn, c.UserBaseDN))
	if c.GroupBaseDN != "" {
		srv.search("group_base", exists(conn, c.GroupBaseDN))
	}
	// Los conteos van paginados: AD corta en 1000 resultados sin paginar.
	res, err := conn.SearchWithPaging(goldap.NewSearchRequest(c.UserBaseDN, goldap.ScopeWholeSubtree, goldap.NeverDerefAliases,
		0, 0, false, c.UserFilter, []string{"1.1"}, nil), 500)
	if err != nil {
		srv.add("users", "fail", searchCode(err), err)
		return sug
	}
	n := len(res.Entries)
	srv.Users = &n
	if n == 0 {
		srv.add("users", "warn", "no_users", nil)
	} else {
		srv.add("users", "ok", "", nil)
	}
	return sug
}

func (s *Service) cert(p peer) *Cert {
	if p.cert == nil {
		return nil
	}
	return &Cert{
		Subject:  p.cert.Subject.String(),
		Issuer:   p.cert.Issuer.String(),
		NotAfter: p.cert.NotAfter.UTC().Format("2006-01-02"),
		DaysLeft: int(p.cert.NotAfter.Sub(s.now()).Hours() / 24),
		Version:  tls.VersionName(p.version),
	}
}

func exists(conn *goldap.Conn, dn string) error {
	_, err := conn.Search(goldap.NewSearchRequest(dn, goldap.ScopeBaseObject, goldap.NeverDerefAliases, 1, 0, false,
		"(objectClass=*)", []string{"1.1"}, nil))
	return err
}

func searchCode(err error) string {
	switch {
	case goldap.IsErrorWithCode(err, goldap.LDAPResultNoSuchObject):
		return "base_not_found"
	case isNetwork(err):
		return "timeout"
	}
	return "search_failed"
}

// OIDs y nombres con los que cada directorio se presenta en su Root DSE.
const adCapability = "1.2.840.113556.1.4.800" // LDAP_CAP_ACTIVE_DIRECTORY_OID, también en Samba AD

// rootDSE sugiere el flavor y el atributo de identidad (§8.6.1). Si no se puede leer, no sugiere.
func rootDSE(conn *goldap.Conn) *Suggestion {
	res, err := conn.Search(goldap.NewSearchRequest("", goldap.ScopeBaseObject, goldap.NeverDerefAliases, 1, 0, false,
		"(objectClass=*)", []string{"vendorName", "supportedCapabilities", "objectClass", "namingContexts"}, nil))
	if err != nil || len(res.Entries) != 1 {
		return nil
	}
	e := res.Entries[0]
	vendor := strings.Join(values(e, "vendorName"), " ")
	has := func(attr, want string) bool {
		return slices.ContainsFunc(values(e, attr), func(v string) bool { return strings.EqualFold(v, want) })
	}
	lower := strings.ToLower(vendor)
	switch {
	case has("supportedCapabilities", adCapability):
		return &Suggestion{Flavor: "ad", GUID: "objectGUID", Vendor: vendor}
	case has("namingContexts", "o=ipaca"): // FreeIPA guarda su CA en o=ipaca
		return &Suggestion{Flavor: "openldap", GUID: "ipaUniqueID", Vendor: vendor}
	case strings.Contains(lower, "389") || strings.Contains(lower, "red hat") || strings.Contains(lower, "fedora"):
		return &Suggestion{Flavor: "openldap", GUID: "nsUniqueId", Vendor: vendor}
	case has("objectClass", "OpenLDAProotDSE"), strings.Contains(lower, "oracle"), strings.Contains(lower, "sun"):
		return &Suggestion{Flavor: "openldap", GUID: "entryUUID", Vendor: vendor}
	}
	return &Suggestion{Flavor: "generic", Vendor: vendor}
}

// values lee un atributo sin importar mayúsculas: los servidores no siempre devuelven el nombre
// como se pidió.
func values(e *goldap.Entry, attr string) []string {
	for _, a := range e.Attributes {
		if strings.EqualFold(a.Name, attr) {
			return a.Values
		}
	}
	return nil
}

func rawValue(e *goldap.Entry, attr string) []byte {
	for _, a := range e.Attributes {
		if strings.EqualFold(a.Name, attr) && len(a.ByteValues) > 0 {
			return a.ByteValues[0]
		}
	}
	return nil
}

// UserReport es el resultado de *Probar un usuario* (§8.6.5). De los atributos muestra solo los
// nombres, nunca sus valores, salvo los mapeados.
type UserReport struct {
	URL        string   `json:"url"`
	Login      string   `json:"login"`  // lo escrito, normalizado
	Filter     string   `json:"filter"` // el filtro que se usó, con lo escrito escapado
	Status     string   `json:"status"` // found | not_found | ambiguous
	DN         string   `json:"dn,omitempty"`
	DNs        []string `json:"dns,omitempty"` // con ambiguous: el filtro no identifica a una persona
	Enabled    bool     `json:"enabled"`
	Attributes []string `json:"attributes"`
	Email      string   `json:"email"`
	Name       string   `json:"name"`
	GUID       string   `json:"guid"`
	Missing    []string `json:"missing"`  // atributos mapeados que la entrada no trae
	Warnings   []string `json:"warnings"` // guid_from_dn: sin atributo de identidad, renombrar la entrada crea otra cuenta
}

// UnavailableError lleva lo que contestó el directorio cuando no se pudo conectar ni buscar.
type UnavailableError struct{ Err error }

func (e *UnavailableError) Error() string { return ErrUnavailable.Error() }
func (e *UnavailableError) Unwrap() error { return ErrUnavailable }
func (e *UnavailableError) Detail() string {
	return e.Err.Error()
}

// TestUser busca a alguien con la configuración del formulario, sin su contraseña.
func (s *Service) TestUser(ctx context.Context, p Patch, login string) (UserReport, error) {
	c, err := s.candidate(ctx, p)
	if err != nil {
		return UserReport{}, err
	}
	if err := c.complete(); err != nil {
		return UserReport{}, err
	}
	cfg := c.effective()
	login, ok := normalizeLogin(cfg, login)
	if !ok {
		return UserReport{}, &FieldError{Field: "login", Err: ErrInvalid}
	}
	r, err := s.testUser(ctx, cfg, login)
	s.audit.Record(ctx, audit.Event{Action: audit.LDAPTest, TargetType: "site",
		Meta: map[string]any{"kind": "user", "login": login, "ok": err == nil && r.Status == "found"}})
	return r, err
}

func (s *Service) testUser(ctx context.Context, c config, login string) (UserReport, error) {
	conn, url, err := s.connect(ctx, c)
	if err != nil {
		return UserReport{}, &UnavailableError{err}
	}
	defer conn.Close()
	r := UserReport{URL: url, Login: login, Filter: userFilter(c, login), Attributes: []string{}, Missing: []string{}, Warnings: []string{}}
	attrs := []string{"*", c.Attrs.Email, c.Attrs.Name}
	if c.Attrs.GUID != "" {
		attrs = append(attrs, c.Attrs.GUID)
	}
	// Con dos alcanza para saber que hay más de uno.
	res, err := conn.Search(goldap.NewSearchRequest(c.UserBaseDN, goldap.ScopeWholeSubtree, goldap.NeverDerefAliases,
		2, 0, false, r.Filter, attrs, nil))
	if err != nil && !goldap.IsErrorWithCode(err, goldap.LDAPResultSizeLimitExceeded) {
		return UserReport{}, &UnavailableError{err}
	}
	entries := res.Entries
	switch {
	case len(entries) == 0:
		r.Status = "not_found"
		return r, nil
	case len(entries) > 1 || err != nil:
		r.Status = "ambiguous"
		for _, e := range entries {
			r.DNs = append(r.DNs, e.DN)
		}
		return r, nil
	}
	e := entries[0]
	r.Status, r.DN = "found", e.DN
	for _, a := range e.Attributes {
		if !slices.ContainsFunc(r.Attributes, func(n string) bool { return strings.EqualFold(n, a.Name) }) {
			r.Attributes = append(r.Attributes, a.Name)
		}
	}
	slices.SortFunc(r.Attributes, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	r.Email, r.Name = first(values(e, c.Attrs.Email)), first(values(e, c.Attrs.Name))
	if r.Email == "" {
		r.Missing = append(r.Missing, c.Attrs.Email)
	}
	if r.Name == "" {
		r.Missing = append(r.Missing, c.Attrs.Name)
	}
	switch {
	case c.Attrs.GUID == "":
		r.Warnings = append(r.Warnings, "guid_from_dn")
	default:
		r.GUID = formatGUID(c.Attrs.GUID, rawValue(e, c.Attrs.GUID))
		if r.GUID == "" {
			r.Missing = append(r.Missing, c.Attrs.GUID)
		}
	}
	r.Enabled = true
	if c.DisabledFilter != "" {
		res, err := conn.Search(goldap.NewSearchRequest(e.DN, goldap.ScopeBaseObject, goldap.NeverDerefAliases, 1, 0, false,
			c.DisabledFilter, []string{"1.1"}, nil))
		if err != nil {
			return UserReport{}, &UnavailableError{err}
		}
		r.Enabled = len(res.Entries) == 0
	}
	return r, nil
}

// userFilter arma (&<user_filter>(|(attr=x)…)) con lo escrito escapado según RFC 4515: crudo
// sería inyección LDAP, y "*)(uid=*" encontraría a cualquiera (§8.6.2).
func userFilter(c config, login string) string {
	var b strings.Builder
	b.WriteString("(&" + c.UserFilter + "(|")
	x := goldap.EscapeFilter(login)
	for _, a := range c.LoginAttrs {
		b.WriteString("(" + a + "=" + x + ")")
	}
	b.WriteString("))")
	return b.String()
}

// normalizeLogin acepta ana, EMPRESA\ana, ana@empresa.local o el correo (§8.6.2). El prefijo
// del dominio se quita solo si es el configurado, y solo en AD.
func normalizeLogin(c config, login string) (string, bool) {
	login = strings.TrimSpace(login)
	if c.Flavor == "ad" && c.DomainNetbios != "" {
		if domain, user, ok := strings.Cut(login, `\`); ok && strings.EqualFold(domain, c.DomainNetbios) {
			login = user
		}
	}
	if login == "" || utf8.RuneCountInString(login) > 256 || strings.ContainsFunc(login, unicode.IsControl) {
		return "", false
	}
	return login, true
}

func first(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

// formatGUID muestra la identidad como texto. objectGUID de AD son 16 bytes con los tres
// primeros grupos en little-endian; los demás directorios la guardan como texto.
func formatGUID(attr string, v []byte) string {
	switch {
	case len(v) == 0:
		return ""
	case strings.EqualFold(attr, "objectGUID") && len(v) == 16:
		return fmt.Sprintf("%x-%x-%x-%x-%x", []byte{v[3], v[2], v[1], v[0]}, []byte{v[5], v[4]}, []byte{v[7], v[6]}, v[8:10], v[10:])
	case utf8.Valid(v) && !strings.ContainsFunc(string(v), unicode.IsControl):
		return string(v)
	}
	return hex.EncodeToString(v)
}
