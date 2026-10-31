package mail

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io/fs"
	"slices"
	"strings"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/branding"
	"github.com/athomo001/kudawiki/internal/repo"
)

// brand es la marca de la empresa que lleva cada correo (§12).
type brand interface {
	Site(ctx context.Context) (branding.Site, error)
	EmailLogo(ctx context.Context) ([]byte, error)
}

type Service struct {
	store         store
	brand         brand
	audit         *audit.Recorder
	key           []byte             // MASTER_KEY: cifra la contraseña guardada
	fixed         map[string]*string // claves del archivo o de las variables, ya validadas
	fixedPassword *string            // la del archivo: nunca va a la base
	locked        []string
	texts         map[string]map[string]string // idioma → clave → texto
	baseURL       string
	now           func() time.Time
	wake          chan struct{}  // Enqueue despierta al worker
	roots         *x509.CertPool // nil: los certificados del sistema; los tests ponen el de su servidor
}

// NewService valida lo que viene del archivo de configuración: un puerto o un remitente mal
// escritos ahí impiden arrancar, en vez de quedar ignorados. i18n es la carpeta con es.json y
// en.json, de donde salen los textos del correo (§12.1).
func NewService(s store, b brand, rec *audit.Recorder, masterKey []byte, fromConfig Patch, i18n fs.FS, baseURL string, now func() time.Time) (*Service, error) {
	fixed, password, hasPassword, err := fromConfig.values()
	if err != nil {
		return nil, errors.New("smtp in the configuration file: port 1-65535, security starttls, tls or none, and from as a plain address")
	}
	// locked nunca es nil: la interfaz espera una lista, aunque esté vacía.
	svc := &Service{store: s, brand: b, audit: rec, key: masterKey, fixed: fixed, locked: []string{}, texts: map[string]map[string]string{},
		baseURL: strings.TrimSuffix(baseURL, "/"), now: now, wake: make(chan struct{}, 1)}
	if hasPassword {
		svc.fixedPassword = &password
		svc.locked = append(svc.locked, "password")
	}
	for k := range fixed {
		svc.locked = append(svc.locked, strings.TrimPrefix(k, "smtp."))
	}
	slices.Sort(svc.locked)
	for _, lang := range languages {
		raw, err := fs.ReadFile(i18n, lang+".json")
		if err != nil {
			return nil, err
		}
		all := map[string]string{}
		if err := json.Unmarshal(raw, &all); err != nil {
			return nil, err
		}
		svc.texts[lang] = map[string]string{}
		for k, v := range all {
			if strings.HasPrefix(k, "email.") {
				svc.texts[lang][k] = v
			}
		}
	}
	return svc, nil
}

// SendTest manda el correo de prueba en el momento, sin cola, a quien lo pide: así el error del
// servidor SMTP se ve en el panel (§12.1). Queda en la auditoría, haya salido o no.
func (s *Service) SendTest(ctx context.Context, to repo.User) error {
	c, err := s.settings(ctx, true)
	if err != nil {
		return err
	}
	if !c.Enabled {
		return ErrNotConfigured
	}
	msg, err := s.build(ctx, c, Message{To: to.Email, Lang: to.Locale, Template: "test", URL: s.baseURL + "/"})
	if err == nil {
		err = s.deliver(ctx, c, to.Email, msg)
	}
	var sendErr *SendError
	if err == nil || errors.As(err, &sendErr) {
		s.audit.Record(ctx, audit.Event{Action: audit.SMTPTestSend, TargetType: "user", TargetID: to.ID, Meta: map[string]any{"sent": err == nil}})
	}
	return err
}
