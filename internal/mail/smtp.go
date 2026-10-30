package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/smtp"
	"net/url"
	"strconv"
	"time"
)

// sendTimeout es todo lo que puede tardar una entrega, de la conexión al QUIT: un servidor que
// no contesta no deja colgado al panel.
const sendTimeout = 30 * time.Second

// SendError es un fallo de la entrega: el servidor SMTP lo rechazó o no se pudo hablar con él.
// Detail es lo que contestó, para que el superadmin lo vea en el panel.
type SendError struct{ Detail string }

func (e *SendError) Error() string { return "smtp_failed: " + e.Detail }

// deliver entrega un mensaje ya armado. starttls exige que el servidor lo ofrezca: sin él la
// contraseña viajaría en claro. Con none, net/smtp se niega a autenticarse fuera de localhost
// por la misma razón.
func (s *Service) deliver(ctx context.Context, c config, to string, msg []byte) error {
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	addr := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	tlsConfig := &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12, RootCAs: s.roots}
	var conn net.Conn
	var err error
	if c.Security == "tls" {
		conn, err = (&tls.Dialer{Config: tlsConfig}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return &SendError{err.Error()}
	}
	deadline, _ := ctx.Deadline()
	conn.SetDeadline(deadline)
	if err := s.session(conn, c, tlsConfig, to, msg); err != nil {
		conn.Close()
		return &SendError{err.Error()}
	}
	return nil
}

func (s *Service) session(conn net.Conn, c config, tlsConfig *tls.Config, to string, msg []byte) error {
	cl, err := smtp.NewClient(conn, c.Host)
	if err != nil {
		return err
	}
	defer cl.Close()
	// El saludo lleva el nombre de la wiki, no "localhost": algunos servidores lo exigen.
	if u, err := url.Parse(s.baseURL); err == nil && u.Hostname() != "" {
		if err := cl.Hello(u.Hostname()); err != nil {
			return err
		}
	}
	if c.Security == "starttls" {
		if ok, _ := cl.Extension("STARTTLS"); !ok {
			return errors.New("the server does not offer STARTTLS")
		}
		if err := cl.StartTLS(tlsConfig); err != nil {
			return err
		}
	}
	if c.Username != "" {
		if err := cl.Auth(smtp.PlainAuth("", c.Username, c.password, c.Host)); err != nil {
			return err
		}
	}
	if err := cl.Mail(c.From); err != nil {
		return err
	}
	if err := cl.Rcpt(to); err != nil {
		return err
	}
	w, err := cl.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return cl.Quit()
}
