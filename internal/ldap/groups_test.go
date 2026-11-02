package ldap

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/athomo001/kudawiki/internal/ldap/ldaptest"
	"github.com/athomo001/kudawiki/internal/repo"
	goldap "github.com/go-ldap/ldap/v3"
)

const (
	anaDN      = "CN=Ana Pérez," + usersDN
	betoDN     = "CN=Beto Soto," + usersDN
	editoresDN = "CN=GG-Wiki-TI-Editores," + groupsDN
	redesDN    = "CN=TI-Redes," + groupsDN
	finanzasDN = "CN=GG-Finanzas," + groupsDN
	anaGUID    = "12345678-9abc-def0-0102-030405060708"
)

func sid(rid string) string {
	b, _ := sidBytes("S-1-5-21-1-2-3-" + rid)
	return string(b)
}

// adGroups suma al AD de juguete: TI-Redes dentro de GG-Wiki-TI-Editores, Ana en TI-Redes y Beto
// (deshabilitado) en GG-Finanzas. tokenGroups es lo que AD calcularía para Ana, con Domain Users,
// que está fuera de group_base_dn.
func adGroups(d *ldaptest.Server) {
	group := func(dn, name, guid, rid string, memberOf ...string) {
		d.Add(ldaptest.Entry{DN: dn, Attrs: map[string][]string{"objectClass": {"top", "group"}, "cn": {name},
			"objectGUID": {guid}, "objectSid": {sid(rid)}, "memberOf": memberOf}}, "")
	}
	group(editoresDN, "GG-Wiki-TI-Editores", "editores-guid-01", "1101")
	group(redesDN, "TI-Redes", "redes-guid-00002", "1102", editoresDN)
	group(finanzasDN, "GG-Finanzas", "finanzas-guid-03", "1103")
	d.Operational = []string{"tokenGroups"}
	d.Set(anaDN, "memberOf", redesDN)
	d.Set(anaDN, "tokenGroups", sid("1102"), sid("1101"), sid("513"))
	d.Set(betoDN, "memberOf", finanzasDN)
	d.Set(betoDN, "tokenGroups", sid("1103"), sid("513"))
}

func guidOf(raw string) string { return formatGUID("objectGUID", []byte(raw)) }

func names(groups []Group) []string {
	var out []string
	for _, g := range groups {
		out = append(out, g.Name)
	}
	return out
}

func TestMappingsAD(t *testing.T) {
	d := directory(t, ldaptest.Options{LDAPS: true})
	adGroups(d)
	f := newFixture(t, Patch{})
	ctx := context.Background()
	p := form(d, d.CAFile(t))
	if _, _, err := f.svc.Groups(ctx, "wiki"); !errors.Is(err, ErrIncomplete) {
		t.Errorf("groups without a saved connection = %v", err)
	}
	if _, _, err := f.svc.Update(ctx, "super", p, false); err != nil {
		t.Fatal(err)
	}
	for _, ws := range []repo.Workspace{{ID: "ti", Slug: "ti", Name: "TI"}, {ID: "fin", Slug: "finanzas", Name: "Finanzas"}} {
		if err := f.repo.CreateWorkspace(ctx, ws); err != nil {
			t.Fatal(err)
		}
	}
	personal, err := f.repo.EnsurePersonal(ctx, "super")
	if err != nil {
		t.Fatal(err)
	}

	groups, truncated, err := f.svc.Groups(ctx, "WIKI")
	if err != nil || truncated || len(groups) != 1 {
		t.Fatalf("groups = %+v, %v, %v", groups, truncated, err)
	}
	editores := groups[0]
	if editores != (Group{GUID: guidOf("editores-guid-01"), SID: "S-1-5-21-1-2-3-1101", DN: editoresDN, Name: "GG-Wiki-TI-Editores"}) {
		t.Errorf("group = %+v", editores)
	}
	// Lo escrito va escapado: un * no lista todo.
	if groups, _, err := f.svc.Groups(ctx, "*"); err != nil || len(groups) != 0 {
		t.Errorf("escaped search = %+v, %v", groups, err)
	}
	if groups, _, _ := f.svc.Groups(ctx, ""); !slices.Equal(names(groups), []string{"GG-Finanzas", "GG-Wiki-TI-Editores", "TI-Redes"}) {
		t.Errorf("all groups = %v", names(groups))
	}

	// La regla guarda el grupo como está en el directorio, no como lo manda el navegador.
	m, err := f.svc.CreateMapping(ctx, "super", MappingInput{GroupGUID: editores.GUID, WsID: "ti", Role: "editor"})
	if err != nil {
		t.Fatal(err)
	}
	if m.GroupDN != editoresDN || m.GroupName != "GG-Wiki-TI-Editores" || m.GroupSID != editores.SID || m.WsSlug != "ti" || m.Role != "editor" {
		t.Errorf("mapping = %+v", m)
	}
	if ev := f.lastEvent(t); ev.Action != "ldap.mapping_create" || ev.WsID != "ti" || !strings.Contains(ev.Meta, `"group":"GG-Wiki-TI-Editores"`) {
		t.Errorf("audit = %+v", ev)
	}
	for name, tc := range map[string]struct {
		in   MappingInput
		want error
	}{
		"duplicate":       {MappingInput{GroupGUID: editores.GUID, WsID: "ti", Role: "admin"}, ErrMappingExists},
		"personal space":  {MappingInput{GroupGUID: editores.GUID, WsID: personal.ID, Role: "viewer"}, ErrInvalid},
		"unknown area":    {MappingInput{GroupGUID: editores.GUID, WsID: "nope", Role: "viewer"}, ErrInvalid},
		"superadmin role": {MappingInput{GroupGUID: editores.GUID, WsID: "fin", Role: "superadmin"}, ErrInvalid},
		"unknown group":   {MappingInput{GroupGUID: guidOf("no-existe-guid-0"), WsID: "fin", Role: "viewer"}, ErrGroupNotFound},
		"not a guid":      {MappingInput{GroupGUID: editoresDN, WsID: "fin", Role: "viewer"}, ErrGroupNotFound},
	} {
		if _, err := f.svc.CreateMapping(ctx, "super", tc.in); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v", name, err)
		}
	}

	// Probar un usuario: los grupos anidados cuentan y Domain Users, fuera de group_base_dn, no se muestra.
	r, err := f.svc.TestUser(ctx, p, "ana")
	if err != nil || !slices.Equal(names(r.Groups), []string{"GG-Wiki-TI-Editores", "TI-Redes"}) || len(r.Access) != 1 ||
		r.Access[0].WsSlug != "ti" || r.Access[0].Role != "editor" || r.Access[0].MappingID != m.ID || slices.Contains(r.Warnings, "no_mapped_group") {
		t.Errorf("ana = %+v, %v", r, err)
	}
	// Varios grupos para la misma área: gana el rol más alto.
	redes, err := f.svc.CreateMapping(ctx, "super", MappingInput{GroupGUID: guidOf("redes-guid-00002"), WsID: "ti", Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	r, _ = f.svc.TestUser(ctx, p, "ana")
	if len(r.Access) != 1 || r.Access[0].Role != "admin" || r.Access[0].MappingID != redes.ID || !slices.Equal(r.Access[0].Via, []string{"GG-Wiki-TI-Editores", "TI-Redes"}) {
		t.Errorf("highest role = %+v", r.Access)
	}
	if r, _ := f.svc.TestUser(ctx, p, "beto"); len(r.Access) != 0 || !slices.Contains(r.Warnings, "no_mapped_group") {
		t.Errorf("beto = %+v", r)
	}

	// Vista previa: Ana está en Editores por TI-Redes; Beto también, pero deshabilitado no cuenta.
	d.Set(betoDN, "memberOf", finanzasDN, redesDN)
	in := MappingInput{GroupGUID: editores.GUID, WsID: "fin", Role: "editor"}
	if pv, err := f.svc.Preview(ctx, in); err != nil || pv.Members != 1 || pv.Accounts != 0 || pv.Group.Name != "GG-Wiki-TI-Editores" {
		t.Errorf("preview = %+v, %v", pv, err)
	}
	if err := f.repo.CreateUser(ctx, repo.User{ID: "ana", Email: "ana@acme.cl", FullName: "Ana Pérez", IsActive: true, Locale: "es"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Write.Exec(`UPDATE users SET auth_source = 'ldap', ldap_guid = ? WHERE id = 'ana'`, anaGUID); err != nil {
		t.Fatal(err)
	}
	if pv, _ := f.svc.Preview(ctx, in); pv.Accounts != 1 || pv.Added != 1 || pv.Raised != 0 {
		t.Errorf("preview with an account = %+v", pv)
	}
	if err := f.repo.SetMember(ctx, repo.Scope{WsID: "fin"}, "ana", "viewer"); err != nil {
		t.Fatal(err)
	}
	if pv, _ := f.svc.Preview(ctx, in); pv.Added != 0 || pv.Raised != 1 {
		t.Errorf("preview with a lower role = %+v", pv)
	}
	in.Role = "viewer"
	if pv, _ := f.svc.Preview(ctx, in); pv.Added != 0 || pv.Raised != 0 {
		t.Errorf("preview with the same role = %+v", pv)
	}

	// Cambiar el rol y borrar quedan auditados; lo que no existe es el 404 de siempre.
	if m, err := f.svc.UpdateMapping(ctx, m.ID, "viewer"); err != nil || m.Role != "viewer" {
		t.Errorf("update = %+v, %v", m, err)
	}
	if ev := f.lastEvent(t); ev.Action != "ldap.mapping_update" || !strings.Contains(ev.Meta, `"from":"editor"`) || !strings.Contains(ev.Meta, `"to":"viewer"`) {
		t.Errorf("audit = %+v", ev)
	}
	if _, err := f.svc.UpdateMapping(ctx, "nope", "viewer"); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("update unknown = %v", err)
	}
	if err := f.svc.DeleteMapping(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if ev := f.lastEvent(t); ev.Action != "ldap.mapping_delete" || ev.WsID != "ti" {
		t.Errorf("audit = %+v", ev)
	}
	if list, _ := f.svc.Mappings(ctx); len(list) != 1 || list[0].ID != redes.ID {
		t.Errorf("mappings = %+v", list)
	}
	if err := f.svc.DeleteMapping(ctx, m.ID); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("delete twice = %v", err)
	}
}

// Fuera de AD, Kudawiki resuelve el anidamiento: grupos de grupos, ciclos y memberUid (§8.6.1).
func TestMappingsNested(t *testing.T) {
	d := directory(t, ldaptest.Options{LDAPS: true})
	d.Operational = []string{"entryUUID"}
	anaUID := "uid=ana," + usersDN
	// Como en un directorio real, la entrada lista también las clases de las que hereda.
	person := []string{"top", "person", "organizationalPerson", "inetOrgPerson"}
	d.Add(ldaptest.Entry{DN: anaUID, Attrs: map[string][]string{"objectClass": person, "uid": {"ana"},
		"cn": {"Ana"}, "mail": {"ana@acme.cl"}, "entryUUID": {"uuid-ana"}}}, "")
	d.Add(ldaptest.Entry{DN: "uid=eva," + usersDN, Attrs: map[string][]string{"objectClass": person, "uid": {"eva"},
		"cn": {"Eva"}, "nsAccountLock": {"TRUE"}, "entryUUID": {"uuid-eva"}}}, "")
	group := func(name, class string, attrs map[string][]string) string {
		dn := "cn=" + name + "," + groupsDN
		attrs["objectClass"], attrs["cn"], attrs["entryUUID"] = []string{class}, []string{name}, []string{"uuid-" + name}
		d.Add(ldaptest.Entry{DN: dn, Attrs: attrs}, "")
		return dn
	}
	redes := group("redes", "groupOfNames", map[string][]string{"member": {anaUID, "uid=eva," + usersDN}})
	group("editores", "groupOfNames", map[string][]string{"member": {redes}})
	group("ciclo-a", "groupOfNames", map[string][]string{"member": {"cn=ciclo-b," + groupsDN}})
	group("ciclo-b", "groupOfNames", map[string][]string{"member": {"cn=ciclo-a," + groupsDN, anaUID}})
	group("posix", "posixGroup", map[string][]string{"memberUid": {"ana", "eva"}})
	group("otros", "groupOfNames", map[string][]string{"member": {"uid=nadie," + usersDN}})

	f := newFixture(t, Patch{})
	ctx := context.Background()
	p := form(d, d.CAFile(t))
	p.Flavor, p.GroupMemberFilter = new("openldap"), new("(|(member={dn})(memberUid={uid}))")
	if _, _, err := f.svc.Update(ctx, "super", p, false); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.CreateWorkspace(ctx, repo.Workspace{ID: "ti", Slug: "ti", Name: "TI"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CreateMapping(ctx, "super", MappingInput{GroupGUID: "uuid-editores", WsID: "ti", Role: "editor"}); err != nil {
		t.Fatal(err)
	}

	r, err := f.svc.TestUser(ctx, p, "ana")
	if err != nil || !slices.Equal(names(r.Groups), []string{"ciclo-a", "ciclo-b", "editores", "posix", "redes"}) ||
		len(r.Access) != 1 || r.Access[0].Role != "editor" {
		t.Errorf("ana = %+v, %v", r, err)
	}
	// Eva está en los mismos grupos, pero deshabilitada: la vista previa no la cuenta.
	for name, want := range map[string]int{"uuid-editores": 1, "uuid-posix": 1, "uuid-ciclo-a": 1, "uuid-otros": 0} {
		pv, err := f.svc.Preview(ctx, MappingInput{GroupGUID: name, WsID: "ti", Role: "viewer"})
		if err != nil || pv.Members != want || pv.Truncated {
			t.Errorf("%s: preview = %+v, %v", name, pv, err)
		}
	}

	// Sin atributo de identidad (generic), el grupo se reconoce por su DN, siempre bajo group_base_dn.
	if _, _, err := f.svc.Update(ctx, "super", Patch{Flavor: new("generic")}, false); err != nil {
		t.Fatal(err)
	}
	groups, _, err := f.svc.Groups(ctx, "editores")
	if err != nil || len(groups) != 1 || groups[0].GUID != "cn=editores,"+groupsDN {
		t.Fatalf("generic groups = %+v, %v", groups, err)
	}
	// generic no trae filtro de cuentas deshabilitadas: Eva cuenta.
	if pv, err := f.svc.Preview(ctx, MappingInput{GroupGUID: groups[0].GUID, WsID: "ti", Role: "viewer"}); err != nil || pv.Members != 2 {
		t.Errorf("generic preview = %+v, %v", pv, err)
	}
	if _, err := f.svc.Preview(ctx, MappingInput{GroupGUID: anaUID, WsID: "ti", Role: "viewer"}); !errors.Is(err, ErrGroupNotFound) {
		t.Errorf("a DN outside group_base_dn = %v", err)
	}
}

func TestGroupMemberFilter(t *testing.T) {
	f := newFixture(t, Patch{})
	for _, v := range []string{"(member=x)", "(member={dn}", "member={dn}"} {
		_, _, err := f.svc.Update(context.Background(), "super", Patch{GroupMemberFilter: new(v)}, false)
		var fe *FieldError
		if !errors.As(err, &fe) || fe.Field != "group_member_filter" {
			t.Errorf("%q: err = %v", v, err)
		}
	}
	if s, _, err := f.svc.Update(context.Background(), "super", Patch{GroupMemberFilter: new("(uniqueMember={dn})")}, false); err != nil || s.GroupMemberFilter != "(uniqueMember={dn})" {
		t.Errorf("valid filter = %+v, %v", s, err)
	}
}

func TestSID(t *testing.T) {
	for _, s := range []string{"S-1-5-21-3623811015-3361044348-30300820-1013", "S-1-5-32-544", "S-1-1-0"} {
		b, ok := sidBytes(s)
		if !ok || formatSID(b) != s {
			t.Errorf("%s: %x → %q", s, b, formatSID(b))
		}
	}
	for _, s := range []string{"", "S-1", "X-1-5", "S-1-5-x", "S-1-5-1-2-3-4-5-6-7-8-9-10-11-12-13-14-15-16"} {
		if _, ok := sidBytes(s); ok {
			t.Errorf("%q should not parse", s)
		}
	}
	if formatSID([]byte{1, 2, 0, 0, 0, 0, 0, 5, 1, 0, 0, 0}) != "" {
		t.Error("a truncated SID should not format")
	}
	g := "12345678-9abc-def0-0102-030405060708"
	if b, ok := guidBytes(g); !ok || formatGUID("objectGUID", b) != g {
		t.Errorf("guid round trip = %x", b)
	}
}

// Los términos del filtro de miembros van sueltos en el OR de la búsqueda: 389 DS 3.2 (FreeIPA
// en EL10) no devuelve nada con un OR dentro de otro bajo un AND, si la búsqueda pasa por las ACI.
func TestMemberTerms(t *testing.T) {
	dn := goldap.EscapeFilter(`cn=Ana \2c Pérez (TI),ou=Personas,dc=x`)
	for _, c := range []struct {
		filter string
		want   []string
	}{
		{"(|(member={dn})(uniqueMember={dn}))", []string{"(member=" + dn + ")", "(uniqueMember=" + dn + ")"}},
		{"(member={dn})", []string{"(member=" + dn + ")"}},
		{"(&(objectClass=posixGroup)(memberUid={uid}))", []string{"(&(objectClass=posixGroup)(memberUid=ana))"}},
	} {
		if got := memberTerms(c.filter, dn, "ana"); !slices.Equal(got, c.want) {
			t.Errorf("%s: %q, want %q", c.filter, got, c.want)
		}
	}
}
