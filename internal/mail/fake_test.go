package mail

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP es un servidor SMTP en memoria, lo justo para probar la entrega: STARTTLS opcional,
// AUTH PLAIN y DATA. Guarda lo que recibió.
type fakeSMTP struct {
	addr     string
	tls      *tls.Config // nil: no ofrece STARTTLS
	rejectTo string      // un RCPT a esta dirección responde 550

	mu       sync.Mutex
	got      []received
	authUser string
	authPass string
	authTLS  bool // la autenticación llegó cifrada
}

type received struct {
	from, to string
	data     string
}

func startFakeSMTP(t *testing.T, withTLS bool) (*fakeSMTP, *x509.CertPool) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	f := &fakeSMTP{addr: ln.Addr().String()}
	var pool *x509.CertPool
	if withTLS {
		cert, p := selfSigned(t)
		f.tls, pool = &tls.Config{Certificates: []tls.Certificate{cert}}, p
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(conn)
		}
	}()
	return f, pool
}

func (f *fakeSMTP) serve(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	r, w := bufio.NewReader(conn), conn
	say := func(s string) { w.Write([]byte(s + "\r\n")) }
	say("220 fake ESMTP")
	secure := false
	var cur received
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		cmd := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
		switch cmd {
		case "EHLO", "HELO":
			if f.tls != nil && !secure {
				say("250-fake")
				say("250-STARTTLS")
			} else {
				say("250-fake")
			}
			say("250 AUTH PLAIN")
		case "STARTTLS":
			say("220 go ahead")
			tc := tls.Server(conn, f.tls)
			if err := tc.Handshake(); err != nil {
				return
			}
			conn, secure = tc, true
			r, w = bufio.NewReader(tc), tc
		case "AUTH":
			parts := strings.Fields(line)
			raw, _ := base64.StdEncoding.DecodeString(parts[len(parts)-1])
			cred := strings.Split(string(raw), "\x00")
			f.mu.Lock()
			f.authUser, f.authPass, f.authTLS = cred[1], cred[2], secure
			f.mu.Unlock()
			say("235 ok")
		case "MAIL":
			cur = received{from: strings.Trim(strings.TrimPrefix(line, "MAIL FROM:"), "<>")}
			say("250 ok")
		case "RCPT":
			cur.to = strings.Trim(strings.TrimPrefix(line, "RCPT TO:"), "<>")
			if f.rejectTo != "" && cur.to == f.rejectTo {
				say("550 5.1.1 mailbox unavailable")
				continue
			}
			say("250 ok")
		case "DATA":
			say("354 go")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(strings.TrimPrefix(l, "."))
			}
			cur.data = b.String()
			f.mu.Lock()
			f.got = append(f.got, cur)
			f.mu.Unlock()
			say("250 queued")
		case "QUIT":
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

func (f *fakeSMTP) messages() []received {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]received(nil), f.got...)
}

func selfSigned(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

func decodeBase64(t *testing.T, b []byte) []byte {
	t.Helper()
	out, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(string(b)), ""))
	if err != nil {
		t.Fatal(err)
	}
	return out
}
