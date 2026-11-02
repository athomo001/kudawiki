package ldap

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/athomo001/kudawiki/internal/auth"
	"github.com/athomo001/kudawiki/internal/ldap/ldaptest"
	"github.com/athomo001/kudawiki/internal/repo"
)

// loginFixture es el AD de juguete con grupos, la conexión guardada y activa, y dos reglas:
// GG-Wiki-TI-Editores → TI editor y TI-Redes → Finanzas lector. Ana está en las dos (Editores
// por TI-Redes).
func loginFixture(t *testing.T) (fixture, *ldaptest.Server) {
	t.Helper()
	d := directory(t, ldaptest.Options{LDAPS: true})
	adGroups(d)
	d.SetPassword(anaDN, "clave-ana")
	d.SetPassword(betoDN, "clave-beto")
	f := newFixture(t, Patch{})
	ctx := context.Background()
	p := form(d, d.CAFile(t))
	p.Enabled = new(true)
	if _, _, err := f.svc.Update(ctx, "super", p, false); err != nil {
		t.Fatal(err)
	}
	for _, ws := range []repo.Workspace{{ID: "ti", Slug: "ti", Name: "TI"}, {ID: "fin", Slug: "finanzas", Name: "Finanzas"}} {
		if err := f.repo.CreateWorkspace(ctx, ws); err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range []MappingInput{{GroupGUID: guidOf("editores-guid-01"), WsID: "ti", Role: "editor"}, {GroupGUID: guidOf("redes-guid-00002"), WsID: "fin", Role: "viewer"}} {
		if _, err := f.svc.CreateMapping(ctx, "super", m); err != nil {
			t.Fatal(err)
		}
	}
	return f, d
}

func (f fixture) source(t *testing.T, ws, userID string) repo.MemberSource {
	t.Helper()
	m, err := f.repo.MemberSourceOf(context.Background(), repo.Scope{WsID: ws}, userID)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestLogin(t *testing.T) {
	f, d := loginFixture(t)
	ctx := context.Background()

	// Contraseña vacía: ni siquiera llega al directorio (sería un bind anónimo exitoso).
	binds := d.Binds()
	if _, err := f.svc.Login(ctx, "ana", ""); !errors.Is(err, auth.ErrInvalidCredentials) || d.Binds() != binds {
		t.Errorf("empty password = %v, binds %d → %d", err, binds, d.Binds())
	}
	for name, tc := range map[string][2]string{
		"wrong password":   {"ana", "otra"},
		"unknown login":    {"nadie", "clave-ana"},
		"injected filter":  {"*", "clave-ana"},
		"disabled account": {"beto", "clave-beto"},
	} {
		if _, err := f.svc.Login(ctx, tc[0], tc[1]); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Errorf("%s: err = %v", name, err)
		}
	}

	// Primer ingreso: la cuenta se crea con lo del directorio y recibe sus áreas por los grupos.
	u, err := f.svc.Login(ctx, `EMPRESA\ana`, "clave-ana")
	if err != nil {
		t.Fatal(err)
	}
	if u.AuthSource != "ldap" || u.LDAPGUID != anaGUID || u.Email != "ana@acme.cl" || u.FullName != "Ana Pérez" || u.PasswordHash != "" || !u.IsActive {
		t.Errorf("user = %+v", u)
	}
	if m := f.source(t, "ti", u.ID); m.Role != "editor" || m.LDAPRole != "editor" || m.ManualRole != "" || m.MappingID == "" {
		t.Errorf("ti = %+v", m)
	}
	if m := f.source(t, "fin", u.ID); m.Role != "viewer" {
		t.Errorf("fin = %+v", m)
	}
	if ev := f.lastEvent(t); ev.Action != "ws.member_add" || !strings.Contains(ev.Meta, `"source":"ldap_group"`) || !strings.Contains(ev.Meta, `"system":"ldap"`) {
		t.Errorf("audit = %+v", ev)
	}

	// La misma persona por otro login es la misma cuenta; el correo y el nombre se traen del
	// directorio en cada ingreso.
	d.Set(anaDN, "mail", "ana.perez@acme.cl")
	d.Set(anaDN, "displayName", "Ana Pérez Soto")
	again, err := f.svc.Login(ctx, "ana@empresa.local", "clave-ana")
	if err != nil || again.ID != u.ID || again.Email != "ana.perez@acme.cl" || again.FullName != "Ana Pérez Soto" {
		t.Errorf("second login = %+v, %v", again, err)
	}

	// Un rol a mano en Finanzas se conserva; el efectivo es el más alto.
	if err := f.repo.SetMember(ctx, repo.Scope{WsID: "fin"}, u.ID, "editor"); err != nil {
		t.Fatal(err)
	}
	// Sale de TI-Redes en el directorio: Finanzas queda con su rol manual y TI sigue (Editores lo
	// tenía solo por TI-Redes, pero el token de AD es el que manda).
	d.Set(anaDN, "tokenGroups", sid("1101"), sid("513"))
	if _, err := f.svc.Login(ctx, "ana", "clave-ana"); err != nil {
		t.Fatal(err)
	}
	if m := f.source(t, "fin", u.ID); m.Role != "editor" || m.ManualRole != "editor" || m.LDAPRole != "" {
		t.Errorf("fin after leaving the group = %+v", m)
	}
	// Ahora sale de Editores y vuelve a TI-Redes: TI, que solo tenía por el grupo, se va.
	d.Set(anaDN, "tokenGroups", sid("1102"))
	if _, err := f.svc.Login(ctx, "ana", "clave-ana"); err != nil {
		t.Fatal(err)
	}
	if m := f.source(t, "ti", u.ID); m.Role != "" {
		t.Errorf("ti after leaving = %+v", m)
	}
	if ev := f.lastEvent(t); ev.Action != "ws.member_remove" || ev.WsID != "ti" {
		t.Errorf("audit = %+v", ev)
	}
	if m := f.source(t, "fin", u.ID); m.Role != "editor" || m.LDAPRole != "viewer" {
		t.Errorf("fin back in the group = %+v", m)
	}

	// Sin ningún grupo con regla no entra, aunque tenga un rol manual (§8.6.3).
	d.Set(anaDN, "tokenGroups", sid("513"))
	if _, err := f.svc.Login(ctx, "ana", "clave-ana"); !errors.Is(err, auth.ErrNoDirectoryAccess) {
		t.Errorf("no mapped group = %v", err)
	}
	d.Set(anaDN, "tokenGroups", sid("1101"))

	// La reautenticación es un bind con la identidad guardada.
	if err := f.svc.VerifyPassword(ctx, again, "clave-ana"); err != nil {
		t.Errorf("reauth = %v", err)
	}
	if err := f.svc.VerifyPassword(ctx, again, "otra"); !errors.Is(err, auth.ErrWrongPassword) {
		t.Errorf("reauth with a wrong password = %v", err)
	}

	// Una cuenta dada de baja en la wiki no vuelve a entrar sola.
	if _, err := f.db.Write.Exec(`UPDATE users SET is_active = 0 WHERE id = ?`, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Login(ctx, "ana", "clave-ana"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("inactive account = %v", err)
	}
}

// Otra cuenta con el mismo correo no se fusiona sola (§8.6.3).
func TestLoginAccountConflict(t *testing.T) {
	f, d := loginFixture(t)
	ctx := context.Background()
	carla := person("carla", "Carla Ruiz", "512", "carla-guid-00003")
	d.Add(carla, "clave-carla")
	d.Set(carla.DN, "tokenGroups", sid("1101"))
	if err := f.repo.CreateUser(ctx, repo.User{ID: "local-carla", Email: "carla@acme.cl", FullName: "Carla", PasswordHash: "x", IsActive: true, Locale: "es"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Login(ctx, "carla", "clave-carla"); !errors.Is(err, auth.ErrAccountConflict) {
		t.Errorf("conflict = %v", err)
	}
	if _, err := f.repo.UserByLDAPGUID(ctx, guidOf("carla-guid-00003")); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("an account was created: %v", err)
	}
}

// AD distingue en el código data por qué falló el bind; solo 532 y 773 se le dicen a la persona.
func TestLoginADCodes(t *testing.T) {
	f, d := loginFixture(t)
	ctx := context.Background()
	for code, want := range map[string]error{"532": auth.ErrPasswordExpired, "773": auth.ErrPasswordExpired, "52e": auth.ErrInvalidCredentials,
		"775": auth.ErrInvalidCredentials, "533": auth.ErrInvalidCredentials} {
		d.BindDiagnostic = "80090308: LdapErr: DSID-0C09042A, comment: AcceptSecurityContext error, data " + code + ", v3839"
		if _, err := f.svc.Login(ctx, "ana", "otra"); !errors.Is(err, want) {
			t.Errorf("data %s = %v, want %v", code, err, want)
		}
	}
}

func TestLoginUnavailable(t *testing.T) {
	f, d := loginFixture(t)
	ctx := context.Background()
	// Con la conexión desactivada, el directorio no se consulta.
	if _, _, err := f.svc.Update(ctx, "super", Patch{Enabled: new(false)}, false); err != nil {
		t.Fatal(err)
	}
	binds := d.Binds()
	if _, err := f.svc.Login(ctx, "ana", "clave-ana"); !errors.Is(err, auth.ErrInvalidCredentials) || d.Binds() != binds {
		t.Errorf("disabled = %v", err)
	}
	// Un directorio que no responde no es "contraseña incorrecta".
	if _, _, err := f.svc.Update(ctx, "super", Patch{Enabled: new(true), URLs: &[]string{"ldaps://localhost:1"}, BindPassword: new(svcPass)}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Login(ctx, "ana", "clave-ana"); !errors.Is(err, auth.ErrDirectoryUnavailable) {
		t.Errorf("unreachable = %v", err)
	}
}
