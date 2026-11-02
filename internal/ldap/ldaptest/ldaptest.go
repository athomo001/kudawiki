// Package ldaptest es un servidor LDAPv3 en memoria para las pruebas de internal/ldap (SPEC
// §8.6): bind simple, búsqueda con los filtros que usa Kudawiki, StartTLS y ldaps://, con un
// certificado de una CA propia. Se porta como los directorios reales donde importa a la
// seguridad: un bind con contraseña vacía es un bind anónimo exitoso (RFC 4513 §5.1.2) y sin
// bind solo se lee el Root DSE. De AD imita la *matching rule in chain* (grupos anidados); el
// tokenGroups lo escribe cada test en la entrada, como atributo operativo. Solo lo importan los tests: no llega al binario. Las pruebas
// contra directorios reales (Samba AD, OpenLDAP, FreeIPA, 389 DS) van en contenedores (§17).
package ldaptest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"maps"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	ber "github.com/go-asn1-ber/asn1-ber"
	goldap "github.com/go-ldap/ldap/v3"
)

// Entry es una entrada del directorio. Los valores binarios (objectGUID) van como string de bytes.
type Entry struct {
	DN    string
	Attrs map[string][]string
}

// Options: LDAPS sirve TLS desde el primer byte; si no, StartTLS. Names son los nombres del
// certificado (por defecto localhost y 127.0.0.1) y NotAfter su vencimiento (por defecto un año).
type Options struct {
	LDAPS    bool
	Names    []string
	NotAfter time.Time
}

// Server es el directorio. Operational son los atributos que "*" no devuelve, como entryUUID
// en OpenLDAP. BindDiagnostic es el mensaje del bind fallido: AD lleva ahí el código data.
type Server struct {
	URL            string
	CAPEM          []byte
	RootDSE        map[string][]string
	Operational    []string
	BindDiagnostic string

	mu        sync.Mutex
	entries   []Entry
	passwords map[string]string
	binds     int
	tls       *tls.Config
}

// New levanta el servidor en 127.0.0.1 y lo cierra al terminar el test.
func New(t testing.TB, o Options) *Server {
	t.Helper()
	ca, cert := certificates(t, o)
	s := &Server{CAPEM: ca, passwords: map[string]string{}, RootDSE: map[string][]string{},
		BindDiagnostic: "invalid credentials", tls: &tls.Config{Certificates: []tls.Certificate{cert}}}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	scheme := "ldap"
	if o.LDAPS {
		ln = tls.NewListener(ln, s.tls)
		scheme = "ldaps"
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	s.URL = scheme + "://localhost:" + port
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

// CAFile escribe la CA en un archivo temporal, como la cargaría ca_file.
func (s *Server) CAFile(t testing.TB) string {
	p := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(p, s.CAPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// Add suma una entrada; con password, también se puede hacer bind con ella.
func (s *Server) Add(e Entry, password string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, e)
	if password != "" {
		s.passwords[strings.ToLower(e.DN)] = password
	}
}

// Set reemplaza los valores de un atributo de una entrada: así un test mueve a alguien de grupo
// o la deshabilita. Sin valores, borra el atributo.
func (s *Server) Set(dn, attr string, vals ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, e := range s.entries {
		if !strings.EqualFold(e.DN, dn) {
			continue
		}
		attrs := maps.Clone(e.Attrs)
		for n := range attrs {
			if strings.EqualFold(n, attr) {
				delete(attrs, n)
			}
		}
		if len(vals) > 0 {
			attrs[attr] = vals
		}
		s.entries[i].Attrs = attrs
	}
}

// SetPassword le da (o le cambia) la contraseña a una entrada.
func (s *Server) SetPassword(dn, password string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.passwords[strings.ToLower(dn)] = password
}

// Binds cuenta los bind recibidos, exitosos o no.
func (s *Server) Binds() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.binds
}

func certificates(t testing.TB, o Options) ([]byte, tls.Certificate) {
	t.Helper()
	names := o.Names
	if len(names) == 0 {
		names = []string{"localhost", "127.0.0.1"}
	}
	notAfter := o.NotAfter
	if notAfter.IsZero() {
		notAfter = time.Now().AddDate(1, 0, 0)
	}
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Kudawiki Test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(2, 0, 0),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: names[0]},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	for _, n := range names {
		if ip := net.ParseIP(n); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, n)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// Códigos de resultado y operaciones de RFC 4511.
const (
	success             = 0
	operationsError     = 1
	protocolError       = 2
	sizeLimitExceeded   = 4
	noSuchObject        = 32
	invalidCredentials  = 49
	appBindRequest      = 0
	appBindResponse     = 1
	appUnbindRequest    = 2
	appSearchRequest    = 3
	appSearchEntry      = 4
	appSearchDone       = 5
	appExtendedRequest  = 23
	appExtendedResponse = 24
	startTLSOID         = "1.3.6.1.4.1.1466.20037"
	bitAndRule          = "1.2.840.113556.1.4.803"  // LDAP_MATCHING_RULE_BIT_AND de AD
	inChainRule         = "1.2.840.113556.1.4.1941" // LDAP_MATCHING_RULE_IN_CHAIN de AD
)

func (s *Server) serve(c net.Conn) {
	defer func() { c.Close() }()
	bound := ""
	for {
		p, err := ber.ReadPacket(c)
		if err != nil || len(p.Children) < 2 {
			return
		}
		id, _ := p.Children[0].Value.(int64)
		op := p.Children[1]
		switch op.Tag {
		case appBindRequest:
			code, dn := s.bind(op)
			if code == success {
				bound = dn
			}
			s.write(c, id, result(appBindResponse, code, s.diagnostic(code)))
		case appUnbindRequest:
			return
		case appSearchRequest:
			s.search(c, id, op, bound)
		case appExtendedRequest:
			if len(op.Children) == 0 || op.Children[0].Data.String() != startTLSOID {
				s.write(c, id, result(appExtendedResponse, protocolError, "unsupported extended operation"))
				continue
			}
			s.write(c, id, result(appExtendedResponse, success, ""))
			t := tls.Server(c, s.tls)
			if err := t.Handshake(); err != nil {
				return
			}
			c = t
		default:
			// Abandon y lo demás no se usan: se ignoran.
		}
	}
}

func (s *Server) diagnostic(code int) string {
	if code == invalidCredentials {
		return s.BindDiagnostic
	}
	return ""
}

// bind: contraseña vacía es un bind anónimo exitoso, como en los servidores reales.
func (s *Server) bind(op *ber.Packet) (int, string) {
	if len(op.Children) < 3 {
		return protocolError, ""
	}
	dn, _ := op.Children[1].Value.(string)
	password := op.Children[2].Data.String()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.binds++
	if password == "" {
		return success, ""
	}
	if want, ok := s.passwords[strings.ToLower(dn)]; ok && want == password {
		return success, dn
	}
	return invalidCredentials, ""
}

func (s *Server) search(c net.Conn, id int64, op *ber.Packet, bound string) {
	if len(op.Children) < 8 {
		s.write(c, id, result(appSearchDone, protocolError, ""))
		return
	}
	base, _ := op.Children[0].Value.(string)
	scope, _ := op.Children[1].Value.(int64)
	limit, _ := op.Children[3].Value.(int64)
	filter := op.Children[6]
	var want []string
	for _, a := range op.Children[7].Children {
		want = append(want, a.Data.String())
	}
	if base == "" && scope == goldap.ScopeBaseObject {
		s.write(c, id, s.entry(Entry{Attrs: s.RootDSE}, want))
		s.write(c, id, result(appSearchDone, success, ""))
		return
	}
	// Como AD: sin bind no se busca.
	if bound == "" {
		s.write(c, id, result(appSearchDone, operationsError, "000004DC: LdapErr: DSID-0C090A5C, comment: In order to perform this operation a successful bind must be completed on the connection., data 0, v4563"))
		return
	}
	baseDN, err := goldap.ParseDN(base)
	if err != nil {
		s.write(c, id, result(appSearchDone, protocolError, "invalid DN"))
		return
	}
	s.mu.Lock()
	entries := slices.Clone(s.entries)
	s.mu.Unlock()
	d := directory{}
	for _, e := range entries {
		d[strings.ToLower(e.DN)] = e
	}
	found := false
	var out []Entry
	for _, e := range entries {
		dn, _ := goldap.ParseDN(e.DN)
		if dn.EqualFold(baseDN) {
			found = true
		}
		in := dn.EqualFold(baseDN)
		switch scope {
		case goldap.ScopeSingleLevel:
			in = baseDN.AncestorOfFold(dn) && len(dn.RDNs) == len(baseDN.RDNs)+1
		case goldap.ScopeWholeSubtree:
			in = in || baseDN.AncestorOfFold(dn)
		}
		if in && d.match(filter, e) {
			out = append(out, e)
		}
	}
	if !found {
		s.write(c, id, result(appSearchDone, noSuchObject, ""))
		return
	}
	code := success
	if limit > 0 && int64(len(out)) > limit {
		out, code = out[:limit], sizeLimitExceeded
	}
	for _, e := range out {
		s.write(c, id, s.entry(e, want))
	}
	s.write(c, id, result(appSearchDone, code, ""))
}

// entry arma la respuesta con los atributos pedidos: "*" son los de usuario, "1.1" ninguno, y
// los operativos solo si se nombran.
func (s *Server) entry(e Entry, want []string) *ber.Packet {
	all := len(want) == 0 || slices.Contains(want, "*")
	p := ber.Encode(ber.ClassApplication, ber.TypeConstructed, appSearchEntry, nil, "Search Result Entry")
	p.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, e.DN, "objectName"))
	attrs := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "attributes")
	names := make([]string, 0, len(e.Attrs))
	for n := range e.Attrs {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, name := range names {
		named := slices.ContainsFunc(want, func(w string) bool { return strings.EqualFold(w, name) })
		operational := slices.ContainsFunc(s.Operational, func(o string) bool { return strings.EqualFold(o, name) })
		if !named && (!all || operational) {
			continue
		}
		a := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "attribute")
		a.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, name, "type"))
		vals := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSet, nil, "vals")
		for _, v := range e.Attrs[name] {
			vals.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, v, "value"))
		}
		a.AppendChild(vals)
		attrs.AppendChild(a)
	}
	p.AppendChild(attrs)
	return p
}

func result(tag ber.Tag, code int, diagnostic string) *ber.Packet {
	r := ber.Encode(ber.ClassApplication, ber.TypeConstructed, tag, nil, "Result")
	r.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagEnumerated, int64(code), "resultCode"))
	r.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "matchedDN"))
	r.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, diagnostic, "diagnosticMessage"))
	return r
}

func (s *Server) write(c net.Conn, id int64, op *ber.Packet) {
	p := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "LDAP Response")
	p.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, id, "MessageID"))
	p.AppendChild(op)
	c.Write(p.Bytes())
}

// directory son las entradas por DN en minúsculas, para seguir las cadenas de grupos.
type directory map[string]Entry

// match evalúa los filtros que arma Kudawiki: and, or, not, igualdad, subcadenas, presencia, el
// AND de bits y la cadena de grupos de AD. La igualdad no distingue mayúsculas, como en los
// atributos de texto de AD, salvo en los binarios (objectGUID, objectSid), que van byte a byte.
func (d directory) match(f *ber.Packet, e Entry) bool {
	switch f.Tag {
	case 0:
		for _, c := range f.Children {
			if !d.match(c, e) {
				return false
			}
		}
		return true
	case 1:
		return slices.ContainsFunc(f.Children, func(c *ber.Packet) bool { return d.match(c, e) })
	case 2:
		return len(f.Children) == 1 && !d.match(f.Children[0], e)
	case 3:
		if len(f.Children) != 2 {
			return false
		}
		want := f.Children[1].Data.String()
		return slices.ContainsFunc(get(e, f.Children[0].Data.String()), func(v string) bool { return equal(v, want) })
	case 4:
		if len(f.Children) != 2 {
			return false
		}
		return slices.ContainsFunc(get(e, f.Children[0].Data.String()), func(v string) bool { return substrings(f.Children[1], v) })
	case 7:
		return len(get(e, f.Data.String())) > 0
	case 9:
		var rule, attr, value string
		for _, c := range f.Children {
			switch c.Tag {
			case 1:
				rule = c.Data.String()
			case 2:
				attr = c.Data.String()
			case 3:
				value = c.Data.String()
			}
		}
		switch rule {
		case bitAndRule:
			want, err := strconv.ParseInt(value, 10, 64)
			return err == nil && slices.ContainsFunc(get(e, attr), func(v string) bool {
				n, err := strconv.ParseInt(v, 10, 64)
				return err == nil && n&want == want
			})
		case inChainRule:
			return d.chain(e, attr, strings.ToLower(value), map[string]bool{})
		}
	}
	return false
}

// chain sigue los DN de attr (memberOf o member) hasta encontrar target, como AD: la entrada
// cumple si target está en attr o en el attr de alguna entrada a la que apunta. seen corta ciclos.
func (d directory) chain(e Entry, attr, target string, seen map[string]bool) bool {
	for _, v := range get(e, attr) {
		v = strings.ToLower(v)
		if v == target {
			return true
		}
		if next, ok := d[v]; ok && !seen[v] {
			seen[v] = true
			if d.chain(next, attr, target, seen) {
				return true
			}
		}
	}
	return false
}

func get(e Entry, name string) []string {
	for n, v := range e.Attrs {
		if strings.EqualFold(n, name) {
			return v
		}
	}
	return nil
}

func equal(v, want string) bool {
	if !utf8.ValidString(v) || !utf8.ValidString(want) {
		return v == want
	}
	return strings.EqualFold(v, want)
}

// substrings evalúa (attr=ini*medio*fin) sin distinguir mayúsculas (RFC 4511 §4.5.1.7.2).
func substrings(parts *ber.Packet, v string) bool {
	v = strings.ToLower(v)
	for _, p := range parts.Children {
		s := strings.ToLower(p.Data.String())
		switch p.Tag {
		case 0:
			if !strings.HasPrefix(v, s) {
				return false
			}
			v = v[len(s):]
		case 1:
			i := strings.Index(v, s)
			if i < 0 {
				return false
			}
			v = v[i+len(s):]
		case 2:
			if !strings.HasSuffix(v, s) {
				return false
			}
			v = ""
		}
	}
	return true
}
