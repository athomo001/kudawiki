package ldap

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	ber "github.com/go-asn1-ber/asn1-ber"
	goldap "github.com/go-ldap/ldap/v3"

	"github.com/athomo001/kudawiki/internal/repo"
)

// Grupos del directorio (§8.3.1, §8.6.1). Cada flavor llega al mismo resultado por su camino: AD
// con tokenGroups y la *matching rule in chain*, que ya resuelven el anidamiento; los demás
// recorriendo los grupos de los grupos en Kudawiki.

var ErrGroupNotFound = errors.New("ldap_group_not_found")

// Group es un grupo del directorio. GUID es su identidad estable: objectGUID en AD; en los demás,
// attrs.guid o, si el directorio no tiene uno, el DN. SID solo existe en AD: con él se cruzan
// los tokenGroups de una persona.
type Group struct {
	GUID string `json:"guid"`
	SID  string `json:"sid,omitempty"`
	DN   string `json:"dn"`
	Name string `json:"name"`
}

const (
	inChainRule = "1.2.840.113556.1.4.1941" // LDAP_MATCHING_RULE_IN_CHAIN de AD
	// maxDepth corta el anidamiento fuera de AD (§8.6.1): más niveles son un error de diseño del
	// directorio o un ciclo que se escapó.
	maxDepth = 10
	// maxVisits acota lo que recorre la vista previa fuera de AD, donde cada miembro es una consulta.
	maxVisits = 5000
	// orChunk: cuántas condiciones van en un (|…): los servidores limitan el largo del filtro.
	orChunk = 50
)

// identity es la identidad estable de una entrada; el DN si no tiene el atributo.
func identity(c config, e *goldap.Entry) string {
	if c.Attrs.GUID != "" {
		if id := formatGUID(c.Attrs.GUID, rawValue(e, c.Attrs.GUID)); id != "" {
			return id
		}
	}
	return e.DN
}

func groupAttrs(c config) []string {
	if c.Flavor == "ad" {
		return []string{"cn", "objectGUID", "objectSid"}
	}
	if c.Attrs.GUID != "" {
		return []string{"cn", c.Attrs.GUID}
	}
	return []string{"cn"}
}

func parseGroup(c config, e *goldap.Entry) Group {
	g := Group{DN: e.DN, Name: first(values(e, "cn"))}
	if c.Flavor == "ad" {
		g.GUID = formatGUID("objectGUID", rawValue(e, "objectGUID"))
		g.SID = formatSID(rawValue(e, "objectSid"))
	} else {
		g.GUID = identity(c, e)
	}
	if g.GUID == "" {
		g.GUID = e.DN
	}
	if g.Name == "" {
		g.Name = e.DN
	}
	return g
}

func byName(a, b Group) int {
	return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
}

// formatSID escribe un SID binario como S-1-5-21-…: revisión, cuántas subautoridades, la
// autoridad en 6 bytes big-endian y las subautoridades en little-endian (MS-DTYP §2.4.2.2).
func formatSID(b []byte) string {
	if len(b) < 8 || len(b) != 8+4*int(b[1]) {
		return ""
	}
	var auth uint64
	for _, x := range b[2:8] {
		auth = auth<<8 | uint64(x)
	}
	s := fmt.Sprintf("S-%d-%d", b[0], auth)
	for i := 8; i < len(b); i += 4 {
		s += "-" + strconv.FormatUint(uint64(binary.LittleEndian.Uint32(b[i:])), 10)
	}
	return s
}

// sidBytes es el inverso de formatSID, para buscar un grupo por su SID.
func sidBytes(s string) ([]byte, bool) {
	parts := strings.Split(s, "-")
	if len(parts) < 3 || parts[0] != "S" {
		return nil, false
	}
	rev, err1 := strconv.ParseUint(parts[1], 10, 8)
	auth, err2 := strconv.ParseUint(parts[2], 10, 48)
	if err1 != nil || err2 != nil || len(parts)-3 > 15 {
		return nil, false
	}
	b := []byte{byte(rev), byte(len(parts) - 3)}
	for i := 5; i >= 0; i-- {
		b = append(b, byte(auth>>(8*i)))
	}
	for _, p := range parts[3:] {
		n, err := strconv.ParseUint(p, 10, 32)
		if err != nil {
			return nil, false
		}
		b = binary.LittleEndian.AppendUint32(b, uint32(n))
	}
	return b, true
}

// guidBytes es el inverso de formatGUID para objectGUID: los tres primeros grupos van en
// little-endian.
func guidBytes(s string) ([]byte, bool) {
	h, err := hex.DecodeString(strings.ReplaceAll(s, "-", ""))
	if err != nil || len(h) != 16 || strings.Count(s, "-") != 4 {
		return nil, false
	}
	return append([]byte{h[3], h[2], h[1], h[0], h[5], h[4], h[7], h[6]}, h[8:]...), true
}

// escapeBytes escribe cada byte como \xx: es la única forma de poner un valor binario en un
// filtro (RFC 4515).
func escapeBytes(b []byte) string {
	var sb strings.Builder
	for _, x := range b {
		fmt.Fprintf(&sb, `\%02x`, x)
	}
	return sb.String()
}

// memberFilter pone en group_member_filter los valores ya escapados.
func memberFilter(f, dn, uid string) string {
	return strings.NewReplacer("{dn}", dn, "{uid}", uid).Replace(f)
}

// memberTerms es memberFilter como términos sueltos de un OR: si el filtro ya es un OR, sus
// hijos. Así la búsqueda queda (&<grupos>(|t1 t2 …)) sin un OR dentro de otro: 389 DS 3.2 (el
// de FreeIPA en EL10) no devuelve nada con ORs anidados bajo un AND cuando la búsqueda pasa por
// las ACI, aunque el filtro es válido.
func memberTerms(f, dn, uid string) []string {
	mf := memberFilter(f, dn, uid)
	p, err := goldap.CompileFilter(mf)
	if err != nil || p.Tag != goldap.FilterOr {
		return []string{mf}
	}
	terms := make([]string, 0, len(p.Children))
	for _, ch := range p.Children {
		t, err := goldap.DecompileFilter(ch)
		if err != nil {
			return []string{mf}
		}
		terms = append(terms, t)
	}
	return terms
}

// notDisabled es la parte del filtro que deja afuera las cuentas deshabilitadas, si hay cómo.
func notDisabled(c config) string {
	if c.DisabledFilter == "" {
		return ""
	}
	return "(!" + c.DisabledFilter + ")"
}

// searchGroups busca grupos bajo group_base_dn cuyo nombre contenga q; con q vacío, los primeros.
// truncated dice que hay más de los que se muestran.
func searchGroups(conn *goldap.Conn, c config, q string, limit int) (groups []Group, truncated bool, err error) {
	filter := "(&" + c.groupFilter() + "(cn=*" + goldap.EscapeFilter(q) + "*))"
	if q == "" {
		filter = "(&" + c.groupFilter() + "(cn=*))"
	}
	res, err := conn.Search(goldap.NewSearchRequest(c.GroupBaseDN, goldap.ScopeWholeSubtree, goldap.NeverDerefAliases,
		limit, 0, false, filter, groupAttrs(c), nil))
	if goldap.IsErrorWithCode(err, goldap.LDAPResultSizeLimitExceeded) {
		truncated, err = true, nil
	}
	if err != nil {
		return nil, false, err
	}
	for _, e := range res.Entries {
		groups = append(groups, parseGroup(c, e))
	}
	slices.SortFunc(groups, byName)
	return groups, truncated, nil
}

// findGroup busca un grupo por su identidad, siempre bajo group_base_dn: no se le cree al
// navegador el DN ni el nombre que manda.
func findGroup(conn *goldap.Conn, c config, guid string) (Group, error) {
	base, scope := c.GroupBaseDN, goldap.ScopeWholeSubtree
	var cond string
	switch {
	case c.Flavor == "ad":
		b, ok := guidBytes(guid)
		if !ok {
			return Group{}, ErrGroupNotFound
		}
		cond = "(objectGUID=" + escapeBytes(b) + ")"
	case c.Attrs.GUID != "" && !isDN(guid):
		cond = "(" + c.Attrs.GUID + "=" + goldap.EscapeFilter(guid) + ")"
	default:
		// Identidad por DN: el grupo es esa entrada, si está bajo group_base_dn.
		dn, err := goldap.ParseDN(guid)
		root, _ := goldap.ParseDN(c.GroupBaseDN)
		if err != nil || root == nil || !root.AncestorOfFold(dn) {
			return Group{}, ErrGroupNotFound
		}
		base, scope = guid, goldap.ScopeBaseObject
	}
	res, err := conn.Search(goldap.NewSearchRequest(base, scope, goldap.NeverDerefAliases, 2, 0, false,
		"(&"+c.groupFilter()+cond+")", groupAttrs(c), nil))
	if goldap.IsErrorWithCode(err, goldap.LDAPResultNoSuchObject) {
		return Group{}, ErrGroupNotFound
	}
	if err != nil && !goldap.IsErrorWithCode(err, goldap.LDAPResultSizeLimitExceeded) {
		return Group{}, err
	}
	// Dos grupos con la misma identidad: no es una identidad.
	if len(res.Entries) != 1 || err != nil {
		return Group{}, ErrGroupNotFound
	}
	return parseGroup(c, res.Entries[0]), nil
}

func isDN(s string) bool {
	dn, err := goldap.ParseDN(s)
	return err == nil && len(dn.RDNs) > 0
}

// groupFilter es el filtro que reconoce un grupo en el flavor.
func (c config) groupFilter() string {
	return Flavors[c.Flavor].GroupFilter
}

// memberships devuelve los grupos de una persona, también los heredados por anidamiento (§8.3.1).
// En AD salen de tokenGroups, en una consulta y solo con el SID; en los demás, buscando los
// grupos que la tienen como miembro y después los grupos de esos grupos, hasta maxDepth.
func memberships(conn *goldap.Conn, c config, e *goldap.Entry) ([]Group, error) {
	if c.Flavor == "ad" {
		// tokenGroups es un atributo construido: AD solo lo calcula en una búsqueda de alcance base.
		res, err := conn.Search(goldap.NewSearchRequest(e.DN, goldap.ScopeBaseObject, goldap.NeverDerefAliases, 1, 0, false,
			"(objectClass=*)", []string{"tokenGroups"}, nil))
		if err != nil {
			return nil, err
		}
		var groups []Group
		for _, en := range res.Entries {
			for _, a := range en.Attributes {
				if !strings.EqualFold(a.Name, "tokenGroups") {
					continue
				}
				for _, v := range a.ByteValues {
					if sid := formatSID(v); sid != "" {
						groups = append(groups, Group{SID: sid})
					}
				}
			}
		}
		return groups, nil
	}
	var groups []Group
	seen := map[string]bool{}
	uid := goldap.EscapeFilter(first(values(e, "uid")))
	conds := memberTerms(c.GroupMemberFilter, goldap.EscapeFilter(e.DN), uid)
	for depth := 0; depth < maxDepth && len(conds) > 0; depth++ {
		var next []string
		for chunk := range slices.Chunk(conds, orChunk) {
			res, err := conn.SearchWithPaging(goldap.NewSearchRequest(c.GroupBaseDN, goldap.ScopeWholeSubtree, goldap.NeverDerefAliases,
				0, 0, false, "(&"+c.groupFilter()+"(|"+strings.Join(chunk, "")+"))", groupAttrs(c), nil), 500)
			if err != nil {
				return nil, err
			}
			for _, ge := range res.Entries {
				key := strings.ToLower(ge.DN)
				if seen[key] {
					continue // ciclo o el mismo grupo por dos caminos
				}
				seen[key] = true
				groups = append(groups, parseGroup(c, ge))
				// Un grupo no tiene uid: {uid} lleva su DN, que ningún memberUid trae.
				dn := goldap.EscapeFilter(ge.DN)
				next = append(next, memberTerms(c.GroupMemberFilter, dn, dn)...)
			}
		}
		conds = next
	}
	return groups, nil
}

// describe completa con el nombre y el DN los grupos que en AD llegan solo con el SID. Quedan
// solo los que están bajo group_base_dn: los demás (Domain Users y otros) no se pueden mapear.
func describe(conn *goldap.Conn, c config, groups []Group) ([]Group, error) {
	if c.Flavor != "ad" {
		slices.SortFunc(groups, byName)
		return groups, nil
	}
	var conds []string
	for _, g := range groups {
		if b, ok := sidBytes(g.SID); ok {
			conds = append(conds, "(objectSid="+escapeBytes(b)+")")
		}
	}
	out := []Group{}
	for chunk := range slices.Chunk(conds, orChunk) {
		res, err := conn.SearchWithPaging(goldap.NewSearchRequest(c.GroupBaseDN, goldap.ScopeWholeSubtree, goldap.NeverDerefAliases,
			0, 0, false, "(&"+c.groupFilter()+"(|"+strings.Join(chunk, "")+"))", groupAttrs(c), nil), 500)
		if err != nil {
			return nil, err
		}
		for _, e := range res.Entries {
			out = append(out, parseGroup(c, e))
		}
	}
	slices.SortFunc(out, byName)
	return out, nil
}

// Grant es lo que las reglas le dan a una persona en un área: el rol más alto entre sus grupos
// mapeados (§8.3.1). MappingID es la regla que lo da, para decir "Gestionado por AD: <grupo>";
// Via, todos los grupos que dan acceso al área.
type Grant struct {
	WsID      string   `json:"workspace_id"`
	WsSlug    string   `json:"workspace_slug"`
	WsName    string   `json:"workspace_name"`
	Role      string   `json:"role"`
	MappingID string   `json:"mapping_id"`
	Via       []string `json:"via"`
}

// grants cruza los grupos de una persona con las reglas: en AD por SID, en los demás por
// identidad. Varias reglas para la misma área: gana el rol más alto.
func grants(c config, groups []Group, mappings []repo.LDAPMapping) []Grant {
	has := func(m repo.LDAPMapping) bool {
		return slices.ContainsFunc(groups, func(g Group) bool {
			if c.Flavor == "ad" {
				return m.GroupSID != "" && strings.EqualFold(g.SID, m.GroupSID)
			}
			return strings.EqualFold(g.GUID, m.GroupGUID)
		})
	}
	byWs := map[string]*Grant{}
	var out []*Grant
	for _, m := range mappings {
		if !has(m) {
			continue
		}
		g, ok := byWs[m.WsID]
		if !ok {
			g = &Grant{WsID: m.WsID, WsSlug: m.WsSlug, WsName: m.WsName}
			byWs[m.WsID] = g
			out = append(out, g)
		}
		if repo.RoleRank(m.Role) > repo.RoleRank(g.Role) {
			g.Role, g.MappingID = m.Role, m.ID
		}
		if !slices.Contains(g.Via, m.GroupName) {
			g.Via = append(g.Via, m.GroupName)
		}
	}
	res := make([]Grant, 0, len(out))
	for _, g := range out {
		res = append(res, *g)
	}
	slices.SortFunc(res, func(a, b Grant) int { return strings.Compare(strings.ToLower(a.WsName), strings.ToLower(b.WsName)) })
	return res
}

// idAttrs pide solo la identidad: lo que necesita la vista previa.
func idAttrs(c config) []string {
	if c.Attrs.GUID != "" {
		return []string{c.Attrs.GUID}
	}
	return []string{"1.1"}
}

// members devuelve las personas habilitadas que están en el grupo, directa o indirectamente, con
// los atributos attrs, una vez cada una. En AD, una búsqueda paginada con la *matching rule in
// chain* (§8.3.3); en los demás, se recorre el grupo hacia abajo y truncated avisa si se cortó en
// limit entradas visitadas.
func members(conn *goldap.Conn, c config, g Group, attrs []string, limit int) (out []*goldap.Entry, truncated bool, err error) {
	seenID := map[string]bool{}
	users := func(base string, scope int, cond string) (int, error) {
		res, err := conn.SearchWithPaging(goldap.NewSearchRequest(base, scope, goldap.NeverDerefAliases, 0, 0, false,
			"(&"+c.UserFilter+notDisabled(c)+cond+")", attrs, nil), 500)
		if goldap.IsErrorWithCode(err, goldap.LDAPResultNoSuchObject) {
			return 0, nil
		}
		if err != nil {
			return 0, err
		}
		for _, e := range res.Entries {
			if id := identity(c, e); !seenID[id] {
				seenID[id] = true
				out = append(out, e)
			}
		}
		return len(res.Entries), nil
	}
	if c.Flavor == "ad" {
		_, err := users(c.UserBaseDN, goldap.ScopeWholeSubtree, "(memberOf:"+inChainRule+":="+goldap.EscapeFilter(g.DN)+")")
		return out, false, err
	}
	dnAttrs, uidAttrs := memberAttrs(c.GroupMemberFilter)
	userRoot, _ := goldap.ParseDN(c.UserBaseDN)
	groupRoot, _ := goldap.ParseDN(c.GroupBaseDN)
	seen := map[string]bool{strings.ToLower(g.DN): true}
	queue := []string{g.DN}
	visits := 0
	for depth := 0; depth < maxDepth && len(queue) > 0; depth++ {
		var next, uids []string
		for _, gdn := range queue {
			res, err := conn.Search(goldap.NewSearchRequest(gdn, goldap.ScopeBaseObject, goldap.NeverDerefAliases, 1, 0, false,
				c.groupFilter(), append(slices.Clone(dnAttrs), uidAttrs...), nil))
			if goldap.IsErrorWithCode(err, goldap.LDAPResultNoSuchObject) {
				continue
			}
			if err != nil {
				return nil, false, err
			}
			for _, e := range res.Entries {
				for _, a := range uidAttrs {
					uids = append(uids, values(e, a)...)
				}
				for _, a := range dnAttrs {
					for _, mdn := range values(e, a) {
						key := strings.ToLower(mdn)
						if seen[key] {
							continue
						}
						seen[key] = true
						if visits++; visits > limit {
							return out, true, nil
						}
						dn, err := goldap.ParseDN(mdn)
						if err != nil {
							continue
						}
						// Cada miembro es una persona o un grupo: se prueba como persona y, si no, como grupo.
						// Una persona deshabilitada no se encuentra como persona, y tampoco es un grupo.
						found := 0
						if userRoot != nil && userRoot.AncestorOfFold(dn) {
							if found, err = users(mdn, goldap.ScopeBaseObject, ""); err != nil {
								return nil, false, err
							}
						}
						if found == 0 && groupRoot != nil && groupRoot.AncestorOfFold(dn) {
							next = append(next, mdn)
						}
					}
				}
			}
		}
		for chunk := range slices.Chunk(uids, orChunk) {
			var conds strings.Builder
			for _, u := range chunk {
				conds.WriteString("(uid=" + goldap.EscapeFilter(u) + ")")
			}
			if _, err := users(c.UserBaseDN, goldap.ScopeWholeSubtree, "(|"+conds.String()+")"); err != nil {
				return nil, false, err
			}
		}
		queue = next
	}
	return out, false, nil
}

// memberAttrs lee de group_member_filter en qué atributos del grupo están los miembros: los que
// se comparan con {dn} guardan DN y los que se comparan con {uid}, uids.
func memberAttrs(f string) (dnAttrs, uidAttrs []string) {
	const dnMark, uidMark = "kudawiki-dn", "kudawiki-uid"
	p, err := goldap.CompileFilter(memberFilter(f, dnMark, uidMark))
	if err != nil {
		return nil, nil
	}
	var walk func(*ber.Packet)
	walk = func(p *ber.Packet) {
		if p.Tag == goldap.FilterEqualityMatch && len(p.Children) == 2 {
			attr, v := p.Children[0].Data.String(), p.Children[1].Data.String()
			switch v {
			case dnMark:
				dnAttrs = append(dnAttrs, attr)
			case uidMark:
				uidAttrs = append(uidAttrs, attr)
			}
			return
		}
		for _, ch := range p.Children {
			walk(ch)
		}
	}
	walk(p)
	return dnAttrs, uidAttrs
}
