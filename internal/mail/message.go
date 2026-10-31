package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"html/template"
	"image/png"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"slices"
	"strconv"
	"strings"

	"github.com/athomo001/kudawiki/internal/repo"
)

// languages son los idiomas de la interfaz; un correo sale en el de quien lo recibe.
var languages = []string{"es", "en"}

// templates es la lista cerrada de correos (§12.1): cuántos párrafos tiene cada uno. Los textos
// son email.<nombre>.subject, .title, .p1…pN y .action en los archivos de i18n.
var templates = map[string]int{
	"test":                      2,
	"invite":                    2, // §8.5.3
	"password_reset":            3, // §8.5.1
	"password_changed":          3, // §8.5.1, §8.5.2: aviso de seguridad, no desactivable
	"superadmin_password_reset": 2, // §8.5.3: a los demás superadmins
	// Avisos (§5.3, §12.1): nunca llevan contenido de la página, solo su título.
	"proposal_pending":    2,
	"proposal_reminder":   2,
	"proposal_approved":   2,
	"proposal_rejected":   2,
	"proposal_rebase":     2,
	"page_changed":        2,
	"page_deleted":        2,
	"review_due":          2, // §4.8
	"review_reminder":     2,
	"read_request":        2, // §4.14
	"read_reminder":       2,
	"quiz_assigned":       2, // §4.16
	"quiz_reminder":       2,
	"quiz_extra_attempts": 1,
	"quiz_failed_out":     2,
	"digest":              1,
}

// lineTemplates son los renglones del resumen diario: email.line.<tipo>, uno por aviso.
var lineTemplates = map[string]bool{
	"proposal_pending": true, "proposal_reminder": true, "proposal_approved": true, "proposal_rejected": true,
	"proposal_rebase": true, "page_changed": true, "page_deleted": true, "review_due": true, "review_reminder": true,
	"read_request": true, "read_reminder": true, "quiz_assigned": true, "quiz_reminder": true, "quiz_extra_attempts": true, "quiz_failed_out": true,
}

// content es la descripción de un correo: de ella salen el HTML y el texto plano, así nunca
// dicen cosas distintas (§12.1).
type content struct {
	Lang, Subject, Title string
	Paragraphs           []string
	Lines                []string // la lista del resumen diario
	Action, URL          string
	// Unsubscribe es la baja en un clic (RFC 8058): va en los encabezados y en el pie.
	Unsubscribe, UnsubscribeText string
}

func (s *Service) text(lang, key string, vars map[string]string) string {
	t := s.texts[lang][key]
	for k, v := range vars {
		t = strings.ReplaceAll(t, "{"+k+"}", v)
	}
	return t
}

func (s *Service) compose(m Message, lang string, vars map[string]string) content {
	name := m.Template
	c := content{Lang: lang, URL: m.URL, Unsubscribe: m.Unsubscribe}
	c.Subject = s.text(lang, "email."+name+".subject", vars)
	c.Title = s.text(lang, "email."+name+".title", vars)
	for i := 1; i <= templates[name]; i++ {
		c.Paragraphs = append(c.Paragraphs, s.text(lang, "email."+name+".p"+strconv.Itoa(i), vars))
	}
	for _, l := range m.Lines {
		lv := map[string]string{}
		for k, v := range vars {
			lv[k] = v
		}
		for k, v := range l.Vars {
			lv[k] = v
		}
		c.Lines = append(c.Lines, s.text(lang, "email.line."+l.Template, lv))
	}
	if m.URL != "" {
		c.Action = s.text(lang, "email."+name+".action", vars)
	}
	if m.Unsubscribe != "" {
		c.UnsubscribeText = s.text(lang, "email.unsubscribe", vars)
	}
	return c
}

// frame es el marco común con la marca de la empresa, a prueba de Outlook (§12): tablas,
// estilos en línea y 600 px de ancho. html/template escapa todo lo que viene de afuera.
var frame = template.Must(template.New("mail").Parse(`<!doctype html>
<html lang="{{.Lang}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>{{.Subject}}</title></head>
<body style="margin:0;padding:0;background:#f4f5f7">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="background:#f4f5f7"><tr><td align="center" style="padding:24px 12px">
<table role="presentation" width="600" cellpadding="0" cellspacing="0" border="0" style="width:100%;max-width:600px;background:#ffffff;border:1px solid #dfe1e6">
<tr><td style="padding:20px 32px;border-bottom:1px solid #dfe1e6">{{if .LogoW}}<img src="cid:logo" alt="{{.Site}}" width="{{.LogoW}}" height="{{.LogoH}}" style="display:block;border:0">{{else}}<span style="font:bold 20px Arial,Helvetica,sans-serif;color:#15171c">{{.Site}}</span>{{end}}</td></tr>
<tr><td style="padding:32px;font:16px/1.5 Arial,Helvetica,sans-serif;color:#15171c">
<h1 style="margin:0 0 16px;font-size:22px;line-height:1.3">{{.Title}}</h1>
{{range .Paragraphs}}<p style="margin:0 0 16px">{{.}}</p>
{{end}}{{if .Lines}}<ul style="margin:0 0 16px;padding-left:20px">{{range .Lines}}<li style="margin:0 0 8px">{{.}}</li>{{end}}</ul>
{{end}}{{if .URL}}<table role="presentation" cellpadding="0" cellspacing="0" border="0" style="margin:24px 0"><tr><td style="background:{{.Button}};border-radius:4px">
<a href="{{.URL}}" style="display:inline-block;padding:12px 24px;font:bold 16px Arial,Helvetica,sans-serif;color:{{.OnButton}};text-decoration:none">{{.Action}}</a>
</td></tr></table>
<p style="margin:0;font-size:13px;color:#5f6368">{{.LinkHint}}<br><a href="{{.URL}}" style="color:{{.Link}};word-break:break-all">{{.URL}}</a></p>
{{end}}</td></tr>
<tr><td style="padding:16px 32px;border-top:1px solid #dfe1e6;font:12px/1.5 Arial,Helvetica,sans-serif;color:#5f6368">{{.Footer}}{{if .Unsubscribe}}<br><a href="{{.Unsubscribe}}" style="color:#5f6368">{{.UnsubscribeText}}</a>{{end}}</td></tr>
</table></td></tr></table>
</body></html>
`))

type frameData struct {
	content
	Site, LinkHint, Footer string
	Button, OnButton, Link template.CSS // colores ya validados por la marca (#rrggbb)
	LogoW, LogoH           int
}

// build arma el mensaje completo (encabezados y MIME) listo para entregar.
func (s *Service) build(ctx context.Context, c config, m Message) ([]byte, error) {
	lang := m.Lang
	if !slices.Contains(languages, lang) {
		lang = languages[0]
	}
	site, err := s.brand.Site(ctx)
	if err != nil {
		return nil, err
	}
	logo, err := s.brand.EmailLogo(ctx)
	if err != nil && !errors.Is(err, repo.ErrNotFound) {
		return nil, err
	}
	all := map[string]string{"site": site.Title, "url": s.baseURL}
	for k, v := range m.Vars {
		all[k] = v
	}
	body := s.compose(m, lang, all)
	footer := s.text(lang, "email.sent_by", all)
	if site.Company != "" {
		footer = site.Company + " · " + footer
	}
	colors := site.EmailColors()
	data := frameData{content: body, Site: site.Title, LinkHint: s.text(lang, "email.link_hint", nil), Footer: footer,
		Button: template.CSS(colors.Button), OnButton: template.CSS(colors.OnButton), Link: template.CSS(colors.Link)}
	if logo != nil {
		// El PNG está al doble: se muestra a la mitad para que se vea nítido en pantallas 2x.
		if cfg, err := png.DecodeConfig(bytes.NewReader(logo)); err == nil {
			data.LogoW, data.LogoH = max(1, cfg.Width/2), max(1, cfg.Height/2)
		} else {
			logo = nil
		}
	}
	var html bytes.Buffer
	if err := frame.Execute(&html, data); err != nil {
		return nil, err
	}
	return mimeMessage(mail.Address{Name: site.Title, Address: c.From}, m.To, body.Subject, plainText(body, footer, data.LinkHint), html.Bytes(), logo,
		s.now().Format(timeLayout), m.Unsubscribe)
}

const timeLayout = "Mon, 02 Jan 2006 15:04:05 -0700" // RFC 5322

// plainText es la versión de texto que todo correo lleva (§12).
func plainText(c content, footer, linkHint string) string {
	var b strings.Builder
	b.WriteString(c.Title + "\n\n")
	for _, p := range c.Paragraphs {
		b.WriteString(p + "\n\n")
	}
	for _, l := range c.Lines {
		b.WriteString("- " + l + "\n")
	}
	if len(c.Lines) > 0 {
		b.WriteString("\n")
	}
	if c.URL != "" {
		b.WriteString(c.Action + ":\n" + c.URL + "\n\n")
	}
	b.WriteString("-- \n" + footer + "\n")
	if c.Unsubscribe != "" {
		b.WriteString(c.UnsubscribeText + ":\n" + c.Unsubscribe + "\n")
	}
	return b.String()
}

// mimeMessage arma multipart/related (alternative de texto y HTML, más el logo inline) o solo
// alternative si no hay logo. Asunto, destinatario y enlace de baja no pueden traer saltos de
// línea: serían encabezados nuevos.
func mimeMessage(from mail.Address, to, subject, text string, html, logo []byte, date, unsubscribe string) ([]byte, error) {
	if strings.ContainsAny(subject+to+unsubscribe, "\r\n") {
		return nil, ErrInvalid
	}
	rcpt, err := mail.ParseAddress(to)
	if err != nil {
		return nil, ErrInvalid
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	domain := from.Address[strings.LastIndexByte(from.Address, '@')+1:]

	var alt bytes.Buffer
	aw := multipart.NewWriter(&alt)
	for _, part := range []struct{ typ, body string }{{"text/plain", text}, {"text/html", string(html)}} {
		w, err := aw.CreatePart(textproto.MIMEHeader{"Content-Type": {part.typ + "; charset=utf-8"}, "Content-Transfer-Encoding": {"quoted-printable"}})
		if err != nil {
			return nil, err
		}
		qp := quotedprintable.NewWriter(w)
		if _, err := qp.Write([]byte(strings.ReplaceAll(part.body, "\n", "\r\n"))); err != nil {
			return nil, err
		}
		qp.Close()
	}
	aw.Close()

	var msg bytes.Buffer
	header := func(k, v string) { msg.WriteString(k + ": " + v + "\r\n") }
	header("From", from.String())
	header("To", rcpt.String())
	header("Subject", mime.QEncoding.Encode("utf-8", subject))
	header("Date", date)
	header("Message-ID", "<"+hex.EncodeToString(id)+"@"+domain+">")
	header("MIME-Version", "1.0")
	header("Auto-Submitted", "auto-generated") // RFC 3834: que nadie le responda con un fuera de oficina
	if unsubscribe != "" {
		// Baja en un clic (RFC 8058): el cliente de correo hace el POST, sin abrir la wiki.
		header("List-Unsubscribe", "<"+unsubscribe+">")
		header("List-Unsubscribe-Post", "List-Unsubscribe=One-Click")
	}
	if logo == nil {
		header("Content-Type", `multipart/alternative; boundary="`+aw.Boundary()+`"`)
		msg.WriteString("\r\n")
		msg.Write(alt.Bytes())
		return msg.Bytes(), nil
	}
	var rel bytes.Buffer
	rw := multipart.NewWriter(&rel)
	w, err := rw.CreatePart(textproto.MIMEHeader{"Content-Type": {`multipart/alternative; boundary="` + aw.Boundary() + `"`}})
	if err != nil {
		return nil, err
	}
	w.Write(alt.Bytes())
	w, err = rw.CreatePart(textproto.MIMEHeader{"Content-Type": {"image/png"}, "Content-Transfer-Encoding": {"base64"},
		"Content-Id": {"<logo>"}, "Content-Disposition": {`inline; filename="logo.png"`}})
	if err != nil {
		return nil, err
	}
	enc := base64.StdEncoding.EncodeToString(logo)
	for len(enc) > 76 {
		w.Write([]byte(enc[:76] + "\r\n"))
		enc = enc[76:]
	}
	w.Write([]byte(enc + "\r\n"))
	rw.Close()
	header("Content-Type", `multipart/related; type="multipart/alternative"; boundary="`+rw.Boundary()+`"`)
	msg.WriteString("\r\n")
	msg.Write(rel.Bytes())
	return msg.Bytes(), nil
}
