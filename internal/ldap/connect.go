package ldap

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/url"
	"os"
	"time"

	goldap "github.com/go-ldap/ldap/v3"
)

// peer es lo que se supo del certificado del servidor, aunque la verificación haya fallado:
// el diagnóstico muestra el emisor y el vencimiento justamente cuando algo no cuadra (§8.6.5).
type peer struct {
	cert    *x509.Certificate
	version uint16
	err     error // el error de la verificación, con su tipo
}

// stepError es un paso que falló, con el código que el panel traduce a "qué hacer".
type stepError struct {
	step, code string
	err        error
}

func (e *stepError) Error() string { return e.step + ": " + e.code + ": " + e.err.Error() }
func (e *stepError) Unwrap() error { return e.err }

// roots son las CA del sistema más la interna de ca_file: se suma, no las reemplaza (§8.6.4).
func (c config) roots() (*x509.CertPool, error) {
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	if c.CAFile == "" {
		return pool, nil
	}
	pem, err := os.ReadFile(c.CAFile)
	if err != nil {
		return nil, err
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, errors.New("no PEM certificate in ca_file")
	}
	return pool, nil
}

// tlsConfig verifica el certificado en VerifyConnection en lugar de dejarlo a crypto/tls: así
// el error queda con su tipo (CA desconocida, nombre, vencido) también con StartTLS, donde
// go-ldap lo devuelve como texto. Es la verificación estándar de x509, la misma que haría tls.
func (c config) tlsConfig(host string, pool *x509.CertPool, p *peer) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         host,
		InsecureSkipVerify: true, // ver VerifyConnection
		VerifyConnection: func(cs tls.ConnectionState) error {
			p.version = cs.Version
			if len(cs.PeerCertificates) == 0 {
				p.err = errors.New("the server sent no certificate")
				return p.err
			}
			p.cert = cs.PeerCertificates[0]
			if c.InsecureSkipVerify {
				return nil
			}
			opts := x509.VerifyOptions{DNSName: host, Roots: pool, Intermediates: x509.NewCertPool()}
			for _, ic := range cs.PeerCertificates[1:] {
				opts.Intermediates.AddCert(ic)
			}
			_, p.err = p.cert.Verify(opts)
			return p.err
		},
	}
}

// dial abre una conexión cifrada a una URL, sin bind. Cada paso que falla vuelve como
// stepError: tcp, tls.
func (c config) dial(ctx context.Context, rawURL string, p *peer) (*goldap.Conn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, &stepError{"tcp", "invalid_url", err}
	}
	port := u.Port()
	if port == "" {
		port = "389"
		if u.Scheme == "ldaps" {
			port = "636"
		}
	}
	pool, err := c.roots()
	if err != nil {
		return nil, &stepError{"tls", "ca_file", err}
	}
	timeout := c.timeout()
	d := net.Dialer{Timeout: timeout}
	raw, err := d.DialContext(ctx, "tcp", net.JoinHostPort(u.Hostname(), port))
	if err != nil {
		return nil, &stepError{"tcp", "unreachable", err}
	}
	// El plazo cubre el handshake, también el de StartTLS, que go-ldap hace sin plazo propio.
	raw.SetDeadline(time.Now().Add(timeout))
	tc := c.tlsConfig(u.Hostname(), pool, p)
	var conn *goldap.Conn
	if u.Scheme == "ldaps" {
		t := tls.Client(raw, tc)
		if err := t.HandshakeContext(ctx); err != nil {
			raw.Close()
			return nil, tlsError(p, err)
		}
		conn = goldap.NewConn(t, true)
		conn.Start()
	} else {
		conn = goldap.NewConn(raw, false)
		conn.Start()
		conn.SetTimeout(timeout)
		if err := conn.StartTLS(tc); err != nil {
			conn.Close()
			return nil, tlsError(p, err)
		}
	}
	raw.SetDeadline(time.Time{})
	conn.SetTimeout(timeout)
	return conn, nil
}

func tlsError(p *peer, err error) error {
	if p.err != nil {
		err = p.err
	}
	var ua x509.UnknownAuthorityError
	var he x509.HostnameError
	var ci x509.CertificateInvalidError
	code := "tls_failed"
	switch {
	case errors.As(err, &ua):
		code = "tls_unknown_ca"
	case errors.As(err, &he):
		code = "tls_hostname"
	case errors.As(err, &ci) && ci.Reason == x509.Expired:
		code = "tls_expired"
	}
	return &stepError{"tls", code, err}
}

// bindError distingue la contraseña equivocada del resto: es el error más común al configurar.
func bindError(err error) error {
	if goldap.IsErrorWithCode(err, goldap.LDAPResultInvalidCredentials) {
		return &stepError{"bind", "bind_invalid_credentials", err}
	}
	if isNetwork(err) {
		return &stepError{"bind", "timeout", err}
	}
	return &stepError{"bind", "bind_failed", err}
}

func isNetwork(err error) bool {
	return goldap.IsErrorWithCode(err, goldap.ErrorNetwork)
}

// connect prueba las URLs en orden, empezando por la que respondió la última vez, y deja la
// conexión con el bind de la cuenta de servicio (§8.6.2). Devuelve el último error si ninguna
// responde.
func (s *Service) connect(ctx context.Context, c config) (*goldap.Conn, string, error) {
	s.mu.Lock()
	last := s.last
	s.mu.Unlock()
	urls := make([]string, 0, len(c.URLs))
	for _, u := range c.URLs {
		if u == last {
			urls = append([]string{u}, urls...)
		} else {
			urls = append(urls, u)
		}
	}
	var lastErr error
	for _, u := range urls {
		conn, err := c.dial(ctx, u, &peer{})
		if err == nil {
			if err = conn.Bind(c.BindDN, c.password); err != nil {
				conn.Close()
				err = bindError(err)
			}
		}
		if err != nil {
			s.log.Warn("ldap: server did not answer", "url", u, "err", err)
			lastErr = err
			continue
		}
		s.mu.Lock()
		s.last = u
		s.mu.Unlock()
		return conn, u, nil
	}
	return nil, "", lastErr
}
