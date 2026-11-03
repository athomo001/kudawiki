package ldap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/crypto"
	"github.com/athomo001/kudawiki/internal/database"
	"github.com/athomo001/kudawiki/internal/database/dbtest"
	"github.com/athomo001/kudawiki/internal/ldap/ldaptest"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/athomo001/kudawiki/internal/workspace"
)

var key = crypto.MasterKey{Key: bytes.Repeat([]byte{7}, 32), Version: 1}

const (
	svcDN    = "CN=svc-kudawiki,OU=Servicios,DC=empresa,DC=local"
	svcPass  = "clave-de-servicio"
	usersDN  = "OU=Usuarios,DC=empresa,DC=local"
	groupsDN = "OU=Grupos,DC=empresa,DC=local"
)

type fixture struct {
	svc  *Service
	repo *repo.Repo
	db   *database.DB
}

func newFixture(t *testing.T, fromConfig Patch) fixture {
	t.Helper()
	db := dbtest.Open(t)
	r := repo.New(db)
	if err := r.CreateUser(context.Background(), repo.User{ID: "super", Email: "super@acme.cl", FullName: "Super", IsGlobalAdmin: true, IsActive: true, Locale: "es"}); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.DiscardHandler)
	rec := audit.NewRecorder(r, time.Now, log)
	svc, err := NewService(r, workspace.NewService(r, rec, log), rec, key, fromConfig, time.Now, log)
	if err != nil {
		t.Fatal(err)
	}
	return fixture{svc: svc, repo: r, db: db}
}

func (f fixture) lastEvent(t *testing.T) repo.AuditEntry {
	t.Helper()
	ev, err := f.repo.AuditEvents(context.Background(), repo.AuditFilter{Limit: 1})
	if err != nil || len(ev) == 0 {
		t.Fatalf("audit: %v", err)
	}
	return ev[0]
}

// directory es un AD de juguete: la cuenta de servicio, dos personas (una deshabilitada) y las OU.
func directory(t *testing.T, o ldaptest.Options) *ldaptest.Server {
	t.Helper()
	d := ldaptest.New(t, o)
	d.RootDSE = map[string][]string{"supportedCapabilities": {"1.2.840.113556.1.4.800"}, "namingContexts": {"DC=empresa,DC=local"}}
	d.Add(ldaptest.Entry{DN: "DC=empresa,DC=local", Attrs: map[string][]string{"objectClass": {"domain"}}}, "")
	d.Add(ldaptest.Entry{DN: "OU=Servicios,DC=empresa,DC=local", Attrs: map[string][]string{"objectClass": {"organizationalUnit"}}}, "")
	d.Add(ldaptest.Entry{DN: usersDN, Attrs: map[string][]string{"objectClass": {"organizationalUnit"}}}, "")
	d.Add(ldaptest.Entry{DN: groupsDN, Attrs: map[string][]string{"objectClass": {"organizationalUnit"}}}, "")
	d.Add(ldaptest.Entry{DN: svcDN, Attrs: map[string][]string{"objectClass": {"user"}, "sAMAccountName": {"svc-kudawiki"}}}, svcPass)
	d.Add(person("ana", "Ana Pérez", "512", string([]byte{0x78, 0x56, 0x34, 0x12, 0xbc, 0x9a, 0xf0, 0xde, 1, 2, 3, 4, 5, 6, 7, 8})), "")
	d.Add(person("beto", "Beto Soto", "514", "0123456789abcdef"), "") // 514 = 512 + bit 0x2, deshabilitada
	return d
}

func person(login, name, uac, guid string) ldaptest.Entry {
	return ldaptest.Entry{DN: "CN=" + name + "," + usersDN, Attrs: map[string][]string{
		"objectClass": {"top", "person", "user"}, "objectCategory": {"person"}, "sAMAccountName": {login},
		"userPrincipalName": {login + "@empresa.local"}, "mail": {login + "@acme.cl"}, "displayName": {name},
		"userAccountControl": {uac}, "objectGUID": {guid}, "telephoneNumber": {"+56 9 1234 5678"},
	}}
}

// form es lo que mandaría el panel para conectarse a d.
func form(d *ldaptest.Server, caFile string) Patch {
	p := Patch{URLs: &[]string{d.URL}, BindDN: new(svcDN), BindPassword: new(svcPass), UserBaseDN: new(usersDN),
		GroupBaseDN: new(groupsDN), DomainNetbios: new("empresa")}
	if strings.HasPrefix(d.URL, "ldap://") {
		p.StartTLS = new(true)
	}
	if caFile != "" {
		p.CAFile = new(caFile)
	}
	return p
}

func steps(s Server) map[string]Step {
	m := map[string]Step{}
	for _, st := range s.Steps {
		m[st.Name] = st
	}
	return m
}

func TestSettingsAndPassword(t *testing.T) {
	f := newFixture(t, Patch{})
	ctx := context.Background()
	s, err := f.svc.Settings(ctx)
	if err != nil || s.Enabled || s.Flavor != "ad" || s.Timeout != "10s" || s.BindPassword != "" || len(s.Locked) != 0 {
		t.Fatalf("defaults = %+v, %v", s, err)
	}
	s, _, err = f.svc.Update(ctx, "super", Patch{URLs: &[]string{" ldaps://dc01.empresa.local:636/ ", "ldaps://dc01.empresa.local:636"},
		BindDN: new(svcDN), BindPassword: new(svcPass), UserBaseDN: new(usersDN), LoginAttrs: &[]string{"sAMAccountName"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.URLs, []string{"ldaps://dc01.empresa.local:636"}) || s.BindPassword != Mask || s.UserFilter != "" {
		t.Errorf("after update = %+v", s)
	}
	ev := f.lastEvent(t)
	if ev.Action != "ldap.config_update" || !strings.Contains(ev.Meta, `"keys":["bind_dn","bind_password","login_attrs","urls","user_base_dn"]`) ||
		strings.Contains(ev.Meta, svcPass) || strings.Contains(ev.Meta, "dc01") {
		t.Errorf("audit = %s %s", ev.Action, ev.Meta)
	}

	// La contraseña está cifrada en la base y la máscara de vuelta no la cambia.
	var enc []byte
	if err := f.db.Read.QueryRow(`SELECT secret_enc FROM site_settings WHERE key = 'auth.ldap.bind_password' AND value_json IS NULL`).Scan(&enc); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(enc, []byte(svcPass)) {
		t.Error("the bind password is stored in plaintext")
	}
	if _, _, err := f.svc.Update(ctx, "super", Patch{BindPassword: new(Mask)}, false); err != nil {
		t.Fatal(err)
	}
	c, _ := f.svc.settings(ctx, nil, true)
	if c.password != svcPass {
		t.Errorf("the mask changed the password: %q", c.password)
	}
}

func TestValidation(t *testing.T) {
	f := newFixture(t, Patch{})
	ctx := context.Background()
	for name, tc := range map[string]struct {
		p     Patch
		field string
		err   error
	}{
		"url con ruta":          {Patch{URLs: &[]string{"ldaps://dc01/ou=x"}}, "urls", ErrInvalid},
		"url http":              {Patch{URLs: &[]string{"https://dc01"}}, "urls", ErrInvalid},
		"ldap sin starttls":     {Patch{URLs: &[]string{"ldap://dc01"}}, "start_tls", ErrInvalid},
		"filtro roto":           {Patch{UserFilter: new("(objectClass=user")}, "user_filter", ErrInvalid},
		"dn roto":               {Patch{UserBaseDN: new("OU=Usuarios,DC")}, "user_base_dn", ErrInvalid},
		"atributo con espacios": {Patch{LoginAttrs: &[]string{"sAMAccountName", "mail)(x"}}, "login_attrs", ErrInvalid},
		"ca relativa":           {Patch{CAFile: new("ca.pem")}, "ca_file", ErrInvalid},
		"timeout largo":         {Patch{Timeout: new("5m")}, "timeout", ErrInvalid},
		"flavor desconocido":    {Patch{Flavor: new("novell")}, "flavor", ErrInvalid},
		"activar sin datos":     {Patch{Enabled: new(true), URLs: &[]string{"ldaps://dc01"}}, "bind_dn", ErrIncomplete},
		"ayuda sin https":       {Patch{PasswordHelpURL: new("javascript:alert(1)")}, "password_help_url", ErrInvalid},
		"ayuda con usuario":     {Patch{PasswordHelpURL: new("https://x:y@cuenta.acme.cl")}, "password_help_url", ErrInvalid},
		"sincronizar seguido":   {Patch{SyncInterval: new("1m")}, "sync.interval", ErrInvalid},
		"tope mayor que 1":      {Patch{SyncMaxRemovalRatio: new(1.5)}, "sync.max_removal_ratio", ErrInvalid},
		"foto rota":             {Patch{Attrs: &AttrsPatch{Photo: new("jpeg photo")}}, "attrs.photo", ErrInvalid},
	} {
		_, _, err := f.svc.Update(ctx, "super", tc.p, false)
		var fe *FieldError
		if !errors.Is(err, tc.err) || !errors.As(err, &fe) || fe.Field != tc.field {
			t.Errorf("%s: err = %v (%+v)", name, err, fe)
		}
	}
	// StartTLS con ldap:// sí; con ldaps:// no aplica, y no es error.
	if _, _, err := f.svc.Update(ctx, "super", Patch{URLs: &[]string{"ldap://dc01", "ldaps://dc02"}, StartTLS: new(true)}, false); err != nil {
		t.Error(err)
	}
}

func TestFixedFromFile(t *testing.T) {
	f := newFixture(t, Patch{URLs: &[]string{"ldaps://dc01.empresa.local"}, BindPassword: new(svcPass)})
	ctx := context.Background()
	s, _ := f.svc.Settings(ctx)
	if !slices.Equal(s.Locked, []string{"bind_password", "urls"}) || s.BindPassword != Mask || s.URLs[0] != "ldaps://dc01.empresa.local" {
		t.Errorf("settings = %+v", s)
	}
	for _, p := range []Patch{{URLs: &[]string{"ldaps://otro"}}, {BindPassword: new("otra")}} {
		if _, _, err := f.svc.Update(ctx, "super", p, false); !errors.Is(err, ErrLocked) {
			t.Errorf("update %+v = %v", p, err)
		}
	}
	if _, err := NewService(f.repo, nil, nil, key, Patch{URLs: &[]string{"http://x"}}, time.Now, slog.New(slog.DiscardHandler)); err == nil ||
		!strings.Contains(err.Error(), "auth.ldap.urls") {
		t.Errorf("invalid file config = %v", err)
	}
}

// Cambiar adónde se conecta exige volver a escribir la contraseña: la guardada no viaja a un
// servidor nuevo, ni al probar ni al guardar.
func TestPasswordStaysWithDestination(t *testing.T) {
	d := directory(t, ldaptest.Options{LDAPS: true})
	f := newFixture(t, Patch{})
	ctx := context.Background()
	if _, _, err := f.svc.Update(ctx, "super", form(d, d.CAFile(t)), false); err != nil {
		t.Fatal(err)
	}
	evil := ldaptest.New(t, ldaptest.Options{LDAPS: true})
	for _, p := range []Patch{
		{URLs: &[]string{evil.URL}},
		{BindDN: new("CN=otro,DC=empresa,DC=local")},
		{InsecureSkipVerify: new(true)},
		{URLs: &[]string{evil.URL}, BindPassword: new(Mask)},
	} {
		if _, err := f.svc.TestConnection(ctx, p); !errors.Is(err, ErrPasswordRequired) {
			t.Errorf("test %+v = %v", p, err)
		}
		if _, _, err := f.svc.Update(ctx, "super", p, false); !errors.Is(err, ErrPasswordRequired) {
			t.Errorf("update %+v = %v", p, err)
		}
	}
	if evil.Binds() != 0 {
		t.Errorf("the stored password reached another server (%d binds)", evil.Binds())
	}
	// Lo que no cambia el destino no la pide.
	if _, err := f.svc.TestConnection(ctx, Patch{UserFilter: new("(objectClass=user)")}); err != nil {
		t.Error(err)
	}
}

func TestConnection(t *testing.T) {
	ctx := context.Background()
	t.Run("ldaps", func(t *testing.T) {
		d := directory(t, ldaptest.Options{LDAPS: true})
		f := newFixture(t, Patch{})
		r, err := f.svc.TestConnection(ctx, form(d, d.CAFile(t)))
		if err != nil {
			t.Fatal(err)
		}
		st := steps(r.Servers[0])
		if !r.OK || len(r.Servers) != 1 || st["tcp"].Status != "ok" || st["tls"].Status != "ok" || st["bind"].Status != "ok" ||
			st["user_base"].Status != "ok" || st["group_base"].Status != "ok" || *r.Servers[0].Users != 2 {
			t.Errorf("report = %+v", r)
		}
		if c := r.Servers[0].Cert; c == nil || c.Issuer != "CN=Kudawiki Test CA" || c.DaysLeft < 360 || c.Version != "TLS 1.3" {
			t.Errorf("cert = %+v", c)
		}
		if r.Suggestion == nil || r.Suggestion.Flavor != "ad" || r.Suggestion.GUID != "objectGUID" {
			t.Errorf("suggestion = %+v", r.Suggestion)
		}
		ev := f.lastEvent(t)
		if ev.Action != "ldap.test" || !strings.Contains(ev.Meta, `"kind":"connection"`) || !strings.Contains(ev.Meta, `"ok":true`) {
			t.Errorf("audit = %s %s", ev.Action, ev.Meta)
		}
	})
	t.Run("starttls", func(t *testing.T) {
		d := directory(t, ldaptest.Options{})
		f := newFixture(t, Patch{})
		r, err := f.svc.TestConnection(ctx, form(d, d.CAFile(t)))
		if err != nil || !r.OK || r.Servers[0].Cert == nil {
			t.Errorf("report = %+v, %v", r, err)
		}
	})
	fails := map[string]struct {
		opts  ldaptest.Options
		patch func(*Patch, *ldaptest.Server)
		step  string
		code  string
	}{
		"ca desconocida": {ldaptest.Options{LDAPS: true}, func(p *Patch, _ *ldaptest.Server) { p.CAFile = nil }, "tls", "tls_unknown_ca"},
		"otro nombre":    {ldaptest.Options{LDAPS: true, Names: []string{"dc09.empresa.local"}}, nil, "tls", "tls_hostname"},
		"vencido":        {ldaptest.Options{LDAPS: true, NotAfter: time.Now().Add(-time.Hour)}, nil, "tls", "tls_expired"},
		"contraseña":     {ldaptest.Options{LDAPS: true}, func(p *Patch, _ *ldaptest.Server) { p.BindPassword = new("otra") }, "bind", "bind_invalid_credentials"},
		"base":           {ldaptest.Options{LDAPS: true}, func(p *Patch, _ *ldaptest.Server) { p.UserBaseDN = new("OU=Gente,DC=empresa,DC=local") }, "user_base", "base_not_found"},
		"apagado": {ldaptest.Options{LDAPS: true}, func(p *Patch, _ *ldaptest.Server) {
			p.URLs = &[]string{"ldaps://localhost:1"}
		}, "tcp", "unreachable"},
	}
	for name, tc := range fails {
		t.Run(name, func(t *testing.T) {
			d := directory(t, tc.opts)
			f := newFixture(t, Patch{})
			p := form(d, d.CAFile(t))
			if tc.patch != nil {
				tc.patch(&p, d)
			}
			r, err := f.svc.TestConnection(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			st := steps(r.Servers[0])[tc.step]
			if r.OK || st.Status != "fail" || st.Code != tc.code {
				t.Errorf("report = %+v", r)
			}
		})
	}
	t.Run("por vencer", func(t *testing.T) {
		d := directory(t, ldaptest.Options{LDAPS: true, NotAfter: time.Now().AddDate(0, 0, 10)})
		f := newFixture(t, Patch{})
		r, _ := f.svc.TestConnection(ctx, form(d, d.CAFile(t)))
		if st := steps(r.Servers[0])["tls"]; !r.OK || st.Status != "warn" || st.Code != "cert_expiring" {
			t.Errorf("report = %+v", r)
		}
	})
	// Un segundo controlador que no responde hace fallar la prueba, aunque el primero pase.
	t.Run("dos servidores", func(t *testing.T) {
		d := directory(t, ldaptest.Options{LDAPS: true})
		f := newFixture(t, Patch{})
		p := form(d, d.CAFile(t))
		p.URLs = &[]string{d.URL, "ldaps://localhost:1"}
		r, _ := f.svc.TestConnection(ctx, p)
		if r.OK || len(r.Servers) != 2 || steps(r.Servers[0])["users"].Status != "ok" || steps(r.Servers[1])["tcp"].Status != "fail" {
			t.Errorf("report = %+v", r)
		}
	})
}

// No se guarda una configuración activa que no conecta, salvo "Guardar igual", que queda auditado.
func TestSaveRequiresPassingTest(t *testing.T) {
	d := directory(t, ldaptest.Options{LDAPS: true})
	f := newFixture(t, Patch{})
	ctx := context.Background()
	p := form(d, d.CAFile(t))
	p.Enabled = new(true)
	p.BindPassword = new("otra")
	_, report, err := f.svc.Update(ctx, "super", p, false)
	if !errors.Is(err, ErrTestFailed) || report == nil || report.OK {
		t.Fatalf("update = %v, %+v", err, report)
	}
	if s, _ := f.svc.Settings(ctx); s.Enabled || len(s.URLs) != 0 {
		t.Errorf("a failing configuration was saved: %+v", s)
	}
	if _, _, err := f.svc.Update(ctx, "super", p, true); err != nil {
		t.Fatal(err)
	}
	if s, _ := f.svc.Settings(ctx); !s.Enabled {
		t.Error("force did not save")
	}
	if ev := f.lastEvent(t); ev.Action != "ldap.config_update" || !strings.Contains(ev.Meta, `"forced":true`) {
		t.Errorf("audit = %s %s", ev.Action, ev.Meta)
	}
	// Con la contraseña correcta, guarda sin forzar.
	p.BindPassword = new(svcPass)
	if _, _, err := f.svc.Update(ctx, "super", p, false); err != nil {
		t.Fatal(err)
	}
	if ev := f.lastEvent(t); strings.Contains(ev.Meta, "forced") {
		t.Errorf("audit = %s", ev.Meta)
	}
}

func TestUser(t *testing.T) {
	d := directory(t, ldaptest.Options{LDAPS: true})
	f := newFixture(t, Patch{})
	ctx := context.Background()
	p := form(d, d.CAFile(t))

	for _, login := range []string{"ana", `EMPRESA\ana`, "ana@empresa.local", "ANA@acme.cl"} {
		r, err := f.svc.TestUser(ctx, p, login)
		if err != nil {
			t.Fatal(err)
		}
		if r.Status != "found" || r.DN != "CN=Ana Pérez,"+usersDN || !r.Enabled || r.Email != "ana@acme.cl" || r.Name != "Ana Pérez" ||
			r.GUID != "12345678-9abc-def0-0102-030405060708" || len(r.Missing) != 0 {
			t.Errorf("%s: report = %+v", login, r)
		}
		// De los demás atributos, el nombre sí; el valor nunca.
		raw, _ := json.Marshal(r)
		if !slices.Contains(r.Attributes, "telephoneNumber") || strings.Contains(string(raw), "1234 5678") {
			t.Errorf("%s: attributes = %v / %s", login, r.Attributes, raw)
		}
	}
	if ev := f.lastEvent(t); ev.Action != "ldap.test" || !strings.Contains(ev.Meta, `"kind":"user"`) || !strings.Contains(ev.Meta, `"login":"ANA@acme.cl"`) {
		t.Errorf("audit = %s %s", ev.Action, ev.Meta)
	}

	r, _ := f.svc.TestUser(ctx, p, "beto")
	if r.Status != "found" || r.Enabled {
		t.Errorf("disabled account = %+v", r)
	}
	// Lo escrito va escapado (RFC 4515): un filtro inyectado no encuentra a nadie.
	for _, login := range []string{"*", "*)(sAMAccountName=*", `OTRO\ana`} {
		r, err := f.svc.TestUser(ctx, p, login)
		if err != nil || r.Status != "not_found" || (login == "*" && !strings.Contains(r.Filter, `(sAMAccountName=\2a)`)) {
			t.Errorf("%q: report = %+v, %v", login, r, err)
		}
	}
	// Dos entradas para lo mismo no identifican a una persona.
	twin := person("ana", "Ana Otra", "512", "x")
	twin.DN = "CN=Ana Otra,OU=Servicios,DC=empresa,DC=local"
	d.Add(twin, "")
	p.UserBaseDN = new("DC=empresa,DC=local")
	if r, _ := f.svc.TestUser(ctx, p, "ana"); r.Status != "ambiguous" || len(r.DNs) != 2 {
		t.Errorf("ambiguous = %+v", r)
	}
	if _, err := f.svc.TestUser(ctx, p, "  "); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty login = %v", err)
	}
}

// Sin atributo de identidad (generic), se avisa que la cuenta depende del DN.
func TestUserWithoutGUID(t *testing.T) {
	d := directory(t, ldaptest.Options{LDAPS: true})
	d.Add(ldaptest.Entry{DN: "uid=carla," + usersDN, Attrs: map[string][]string{"objectClass": {"person"}, "uid": {"carla"}, "cn": {"Carla"}}}, "")
	f := newFixture(t, Patch{})
	p := form(d, d.CAFile(t))
	p.Flavor = new("generic")
	r, err := f.svc.TestUser(context.Background(), p, "carla")
	if err != nil || r.Status != "found" || !slices.Equal(r.Warnings, []string{"guid_from_dn", "no_mapped_group"}) || !slices.Equal(r.Missing, []string{"mail"}) || r.Name != "Carla" {
		t.Errorf("report = %+v, %v", r, err)
	}
}

// Con varios controladores, se usa el que responde y se recuerda (§8.6.2).
func TestFailover(t *testing.T) {
	d := directory(t, ldaptest.Options{LDAPS: true})
	f := newFixture(t, Patch{})
	p := form(d, d.CAFile(t))
	p.URLs = &[]string{"ldaps://localhost:1", d.URL}
	r, err := f.svc.TestUser(context.Background(), p, "ana")
	if err != nil || r.URL != d.URL {
		t.Fatalf("report = %+v, %v", r, err)
	}
	if f.svc.last != d.URL {
		t.Errorf("last = %q", f.svc.last)
	}
	p.URLs = &[]string{"ldaps://localhost:1"}
	if _, err := f.svc.TestUser(context.Background(), p, "ana"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("all down = %v", err)
	}
}
