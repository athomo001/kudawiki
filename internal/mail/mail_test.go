package mail

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/branding"
	"github.com/athomo001/kudawiki/internal/database"
	"github.com/athomo001/kudawiki/internal/database/dbtest"
	"github.com/athomo001/kudawiki/internal/repo"
)

var key = bytes.Repeat([]byte{9}, 32)

type fixture struct {
	svc   *Service
	brand *branding.Service
	repo  *repo.Repo
	db    *database.DB
	super repo.User
}

func newFixture(t *testing.T, fromConfig Patch) fixture {
	t.Helper()
	db := dbtest.Open(t)
	r := repo.New(db)
	ctx := context.Background()
	super := repo.User{ID: "super", Email: "super@acme.cl", FullName: "Super", IsGlobalAdmin: true, IsActive: true, Locale: "es"}
	if err := r.CreateUser(ctx, super); err != nil {
		t.Fatal(err)
	}
	rec := audit.NewRecorder(r, time.Now, slog.New(slog.DiscardHandler))
	b, err := branding.NewService(r, rec, branding.Patch{})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewService(r, b, rec, key, fromConfig, os.DirFS("../../ui/src/lib/i18n"), "https://wiki.acme.cl/", time.Now)
	if err != nil {
		t.Fatal(err)
	}
	return fixture{svc: svc, brand: b, repo: r, db: db, super: super}
}

func (f fixture) lastEvent(t *testing.T) repo.AuditEntry {
	t.Helper()
	ev, err := f.repo.AuditEvents(context.Background(), repo.AuditFilter{Limit: 1})
	if err != nil || len(ev) == 0 {
		t.Fatalf("audit: %v", err)
	}
	return ev[0]
}

func hostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	h, p, _ := net.SplitHostPort(addr)
	n, _ := strconv.Atoi(p)
	return h, n
}

func TestSettingsAndPassword(t *testing.T) {
	f := newFixture(t, Patch{})
	ctx := context.Background()
	s, err := f.svc.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.Port != 587 || s.Security != "starttls" || s.Enabled || s.Password != "" || len(s.Locked) != 0 {
		t.Fatalf("defaults = %+v", s)
	}

	s, err = f.svc.Update(ctx, f.super.ID, Patch{Host: new("smtp.acme.cl"), Username: new("wiki"), Password: new("clave-del-correo"),
		From: new(" wiki@acme.cl ")})
	if err != nil {
		t.Fatal(err)
	}
	if !s.Enabled || s.From != "wiki@acme.cl" || s.Password != Mask {
		t.Errorf("after update = %+v", s)
	}
	// La auditoría dice qué claves cambiaron, nunca sus valores.
	ev := f.lastEvent(t)
	if ev.Action != "site.smtp_update" || !strings.Contains(ev.Meta, `"keys":["from","host","password","username"]`) ||
		strings.Contains(ev.Meta, "clave-del-correo") || strings.Contains(ev.Meta, "smtp.acme.cl") {
		t.Errorf("audit = %s %s", ev.Action, ev.Meta)
	}

	// La contraseña está cifrada en la base: ni la fila ni el archivo la tienen en claro.
	var enc []byte
	var version int
	if err := f.db.Read.QueryRow(`SELECT secret_enc, key_version FROM site_settings WHERE key = 'smtp.password' AND value_json IS NULL`).Scan(&enc, &version); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(enc, []byte("clave-del-correo")) || version != 1 {
		t.Errorf("stored password: version %d, plaintext visible %v", version, bytes.Contains(enc, []byte("clave-del-correo")))
	}
	f.db.Write.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	var files []byte
	for _, name := range []string{"wiki.db", "wiki.db-wal"} {
		b, _ := os.ReadFile(filepath.Join(filepath.Dir(dbPath(t, f.db)), name))
		files = append(files, b...)
	}
	if bytes.Contains(files, []byte("clave-del-correo")) {
		t.Error("the SMTP password is in plaintext in the database file")
	}

	// Reenviar la máscara no cambia la contraseña; vacía la borra.
	if _, err := f.svc.Update(ctx, f.super.ID, Patch{Password: new(Mask), Port: new(465), Security: new("tls")}); err != nil {
		t.Fatal(err)
	}
	c, err := f.svc.settings(ctx, true)
	if err != nil || c.password != "clave-del-correo" || c.Port != 465 {
		t.Errorf("after resending the mask: %q %d %v", c.password, c.Port, err)
	}
	if ev := f.lastEvent(t); strings.Contains(ev.Meta, "password") {
		t.Errorf("the mask counted as a password change: %s", ev.Meta)
	}
	if s, _ := f.svc.Update(ctx, f.super.ID, Patch{Password: new("")}); s.Password != "" {
		t.Errorf("empty password should delete it: %+v", s)
	}

	for name, p := range map[string]Patch{
		"port":         {Port: new(70000)},
		"security":     {Security: new("ssl")},
		"from w/ name": {From: new("Wiki <wiki@acme.cl>")},
		"from bad":     {From: new("no-es-un-correo")},
		"host spaces":  {Host: new("smtp acme")},
		"host url":     {Host: new("smtp://acme.cl")},
		"newline":      {Username: new("wiki\r\nRCPT TO:<x@y>")},
		"pw control":   {Password: new("a\nb")},
	} {
		if _, err := f.svc.Update(ctx, f.super.ID, p); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want invalid", name, err)
		}
	}
}

func dbPath(t *testing.T, db *database.DB) string {
	t.Helper()
	var seq int
	var name, file string
	if err := db.Read.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &file); err != nil {
		t.Fatal(err)
	}
	return file
}

// Lo que fija el archivo gana, se ve bloqueado y no se puede cambiar; su contraseña no va a la base.
func TestConfigFileWins(t *testing.T) {
	f := newFixture(t, Patch{Host: new("smtp.acme.cl"), Password: new("del-archivo")})
	ctx := context.Background()
	s, _ := f.svc.Settings(ctx)
	if s.Host != "smtp.acme.cl" || s.Password != Mask || !slices.Equal(s.Locked, []string{"host", "password"}) {
		t.Fatalf("settings = %+v", s)
	}
	if _, err := f.svc.Update(ctx, f.super.ID, Patch{Host: new("otro.acme.cl")}); !errors.Is(err, ErrLocked) {
		t.Errorf("locked host: %v", err)
	}
	if _, err := f.svc.Update(ctx, f.super.ID, Patch{Password: new("otra")}); !errors.Is(err, ErrLocked) {
		t.Errorf("locked password: %v", err)
	}
	if _, err := f.svc.Update(ctx, f.super.ID, Patch{Password: new(Mask), From: new("wiki@acme.cl")}); err != nil {
		t.Errorf("the mask on a locked password is no change: %v", err)
	}
	var n int
	f.db.Read.QueryRow(`SELECT count(*) FROM site_settings WHERE key = 'smtp.password'`).Scan(&n)
	if n != 0 {
		t.Error("the password from the file was stored")
	}
	if _, err := NewService(f.repo, f.brand, nil, key, Patch{Port: new(0x10000)}, os.DirFS("../../ui/src/lib/i18n"), "https://x", time.Now); err == nil {
		t.Error("a bad port in the file should stop the start")
	}
}

// parts lee el mensaje recibido: encabezados y cada parte MIME por su tipo.
func parts(t *testing.T, data string) (*mail.Message, map[string][]byte, map[string]string) {
	t.Helper()
	msg, err := mail.ReadMessage(strings.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	out, ids := map[string][]byte{}, map[string]string{}
	var walk func(r io.Reader, ctype string)
	walk = func(r io.Reader, ctype string) {
		mt, params, err := mime.ParseMediaType(ctype)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(mt, "multipart/") {
			return
		}
		mr := multipart.NewReader(r, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			pt, _, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
			if strings.HasPrefix(pt, "multipart/") {
				walk(p, p.Header.Get("Content-Type"))
				continue
			}
			body, _ := io.ReadAll(p) // multipart decodifica quoted-printable solo
			if p.Header.Get("Content-Transfer-Encoding") == "base64" {
				body = decodeBase64(t, body)
			}
			out[pt] = body
			ids[pt] = p.Header.Get("Content-Id")
		}
	}
	walk(msg.Body, msg.Header.Get("Content-Type"))
	return msg, out, ids
}

func TestSendTest(t *testing.T) {
	f := newFixture(t, Patch{})
	ctx := context.Background()
	if err := f.svc.SendTest(ctx, f.super); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("without SMTP: %v", err)
	}
	srv, _ := startFakeSMTP(t, false)
	host, port := hostPort(t, srv.addr)
	if _, err := f.svc.Update(ctx, f.super.ID, Patch{Host: new(host), Port: new(port), Security: new("none"), From: new("wiki@acme.cl")}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.brand.Update(ctx, f.super.ID, branding.Patch{Title: new("Wiki ACME"), Company: new("ACME Ltda."), ColorPrimary: new("#c8102e")}); err != nil {
		t.Fatal(err)
	}
	logo := image.NewRGBA(image.Rect(0, 0, 300, 60))
	for i := range logo.Pix {
		logo.Pix[i] = 0xff
	}
	logo.Set(1, 1, color.Black)
	var b bytes.Buffer
	png.Encode(&b, logo)
	if _, err := f.brand.Upload(ctx, f.super.ID, "logo", b.Bytes()); err != nil {
		t.Fatal(err)
	}

	if err := f.svc.SendTest(ctx, f.super); err != nil {
		t.Fatal(err)
	}
	got := srv.messages()
	if len(got) != 1 || got[0].from != "wiki@acme.cl" || got[0].to != "super@acme.cl" {
		t.Fatalf("received = %+v", got)
	}
	msg, body, ids := parts(t, got[0].data)
	subject, _ := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if from := msg.Header.Get("From"); from != `"Wiki ACME" <wiki@acme.cl>` || subject != "Correo de prueba de Wiki ACME" {
		t.Errorf("From %q, Subject %q", from, subject)
	}
	text, html := string(body["text/plain"]), string(body["text/html"])
	for _, want := range []string{"El correo funciona", "Abrir la wiki:\r\nhttps://wiki.acme.cl/", "ACME Ltda. · Este correo lo envió Wiki ACME"} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
	for _, want := range []string{`src="cid:logo"`, `width="200" height="40"`, "background:#c8102e", `href="https://wiki.acme.cl/"`, "El correo funciona"} {
		if !strings.Contains(html, want) {
			t.Errorf("html lacks %q:\n%s", want, html)
		}
	}
	if _, err := png.DecodeConfig(bytes.NewReader(body["image/png"])); err != nil || ids["image/png"] != "<logo>" {
		t.Errorf("inline logo: id %q, %v", ids["image/png"], err)
	}
	if ev := f.lastEvent(t); ev.Action != "smtp.test_send" || ev.TargetID != "super" || !strings.Contains(ev.Meta, `"sent":true`) {
		t.Errorf("audit = %+v", ev)
	}

	// En inglés para quien tiene la wiki en inglés; el error del servidor llega entero.
	en := repo.User{ID: "super", Email: "boss@acme.cl", Locale: "en"}
	srv.rejectTo = "boss@acme.cl"
	err := f.svc.SendTest(ctx, en)
	var se *SendError
	if !errors.As(err, &se) || !strings.Contains(se.Detail, "550") || !strings.Contains(se.Detail, "mailbox unavailable") {
		t.Fatalf("rejected: %v", err)
	}
	if ev := f.lastEvent(t); !strings.Contains(ev.Meta, `"sent":false`) {
		t.Errorf("a failed test is audited too: %s", ev.Meta)
	}
	srv.rejectTo = ""
	if err := f.svc.SendTest(ctx, en); err != nil {
		t.Fatal(err)
	}
	msg, _, _ = parts(t, srv.messages()[1].data)
	if subject, _ := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject")); subject != "Test email from Wiki ACME" {
		t.Errorf("english subject = %q", subject)
	}
}

// STARTTLS: la contraseña viaja después del cifrado, y sin STARTTLS no se manda nada.
func TestStartTLS(t *testing.T) {
	f := newFixture(t, Patch{})
	ctx := context.Background()
	srv, roots := startFakeSMTP(t, true)
	f.svc.roots = roots
	host, port := hostPort(t, srv.addr)
	if _, err := f.svc.Update(ctx, f.super.ID, Patch{Host: new(host), Port: new(port), Username: new("wiki"), Password: new("clave"),
		From: new("wiki@acme.cl")}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SendTest(ctx, f.super); err != nil {
		t.Fatal(err)
	}
	if srv.authUser != "wiki" || srv.authPass != "clave" || !srv.authTLS || len(srv.messages()) != 1 {
		t.Errorf("auth %q/%q over TLS %v, %d messages", srv.authUser, srv.authPass, srv.authTLS, len(srv.messages()))
	}

	plain, _ := startFakeSMTP(t, false)
	host, port = hostPort(t, plain.addr)
	f.svc.Update(ctx, f.super.ID, Patch{Host: new(host), Port: new(port)})
	var se *SendError
	if err := f.svc.SendTest(ctx, f.super); !errors.As(err, &se) || !strings.Contains(se.Detail, "STARTTLS") {
		t.Errorf("without STARTTLS: %v", err)
	}
	if plain.authUser != "" || len(plain.messages()) != 0 {
		t.Error("credentials or mail went over a plain connection")
	}
}

// Un asunto o destinatario con saltos de línea no agrega encabezados.
func TestHeaderInjection(t *testing.T) {
	from := mail.Address{Name: "Wiki", Address: "wiki@acme.cl"}
	if _, err := mimeMessage(from, "x@acme.cl\r\nBcc: y@evil.cl", "s", "t", nil, nil, "d"); !errors.Is(err, ErrInvalid) {
		t.Errorf("to: %v", err)
	}
	if _, err := mimeMessage(from, "x@acme.cl", "s\r\nBcc: y@evil.cl", "t", nil, nil, "d"); !errors.Is(err, ErrInvalid) {
		t.Errorf("subject: %v", err)
	}
	// Un título con comillas o acentos queda bien codificado en el remitente.
	msg, err := mimeMessage(mail.Address{Name: `Wiki "Ñuñoa"`, Address: "wiki@acme.cl"}, "x@acme.cl", "Ñ", "t", []byte("<p>h</p>"), nil, "d")
	if err != nil {
		t.Fatal(err)
	}
	m, err := mail.ReadMessage(bytes.NewReader(msg))
	if err != nil {
		t.Fatal(err)
	}
	if a, err := m.Header.AddressList("From"); err != nil || a[0].Name != `Wiki "Ñuñoa"` {
		t.Errorf("From = %v, %v", a, err)
	}
}
