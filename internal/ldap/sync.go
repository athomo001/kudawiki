package ldap

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	goldap "github.com/go-ldap/ldap/v3"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/auth"
	"github.com/athomo001/kudawiki/internal/mail"
	"github.com/athomo001/kudawiki/internal/offboarding"
	"github.com/athomo001/kudawiki/internal/repo"
)

// Sincronización con el directorio (§8.3.3, §8.3.4). El login pone al día a quien entra; la
// sincronización cubre a quien no entra: da de alta de antemano a quien está en un grupo con
// regla y, sobre todo, detecta las bajas. Primero arma el plan completo leyendo el directorio y
// después lo aplica: si algo falla al leer, o el plan quita demasiado, no se aplica nada.

var (
	// ErrSyncDisabled: la conexión está apagada; no hay nada que sincronizar.
	ErrSyncDisabled = errors.New("ldap_disabled")
	// ErrSyncRunning: ya hay una sincronización en curso en este proceso.
	ErrSyncRunning = errors.New("ldap_sync_running")
)

// syncVisits es hasta cuántas entradas recorre la sincronización en un grupo fuera de AD. Un
// grupo más grande se aborta siempre, también forzando: con la lista cortada daría de baja a
// quien quedó afuera.
const syncVisits = 100_000

// leavers inicia la baja de §8.4; lo implementa offboarding.
type leavers interface {
	Start(ctx context.Context, actorID, userID string, chosen map[string]string, now bool) (offboarding.Outcome, error)
}

// mailer avisa a los superadmins de una sincronización abortada; lo implementa mail.
type mailer interface {
	Enabled(ctx context.Context) (bool, error)
	Enqueue(ctx context.Context, m mail.Message) error
}

// SetOffboarding conecta la baja de §8.4: sin ella, la sincronización no da de baja a nadie.
func (s *Service) SetOffboarding(l leavers) { s.leavers = l }

// SetMail conecta el aviso a los superadmins cuando una sincronización se aborta.
func (s *Service) SetMail(m mailer, baseURL string) { s.mail, s.baseURL = m, baseURL }

// SyncOptions dice quién la pidió y cómo. Trigger es periodic, manual (el panel) o cli.
type SyncOptions struct {
	ActorID string
	Trigger string
	DryRun  bool
	Force   bool
}

// SyncReport es lo que hizo (o, con DryRun, lo que haría) la sincronización.
type SyncReport struct {
	DryRun  bool   `json:"dry_run"`
	Forced  bool   `json:"forced"`
	Aborted string `json:"aborted,omitempty"` // removal_ratio | empty_group | truncated
	Group   string `json:"group,omitempty"`   // el grupo vacío o cortado
	Groups  int    `json:"groups"`
	People  int    `json:"people"` // personas habilitadas en grupos con regla
	Created int    `json:"created"`
	Updated int    `json:"updated"` // cuentas a las que les cambió el correo, el nombre o el DN
	Added   int    `json:"added"`
	Changed int    `json:"changed"`
	// Removed son las membresías del directorio que se quitan, también las de quienes se dan de
	// baja (se quitan al completar la baja, §8.4). Con Managed, el total, da Ratio.
	Removed     int      `json:"removed"`
	Managed     int      `json:"managed"`
	Ratio       float64  `json:"ratio"`
	MaxRatio    float64  `json:"max_ratio"`
	Deactivated []Leaver `json:"deactivated"`
	Conflicts   []string `json:"conflicts"` // correos del directorio que ya usa otra cuenta
}

// Leaver es una cuenta del directorio que se da de baja, y por qué.
type Leaver struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Reason string `json:"reason"` // disabled | not_found | outside_base | no_mapped_group
}

// syncPerson es una persona habilitada en algún grupo con regla.
type syncPerson struct {
	profile
	groups []Group
	grants []Grant
	user   *repo.User // su cuenta, si ya tiene
}

// Sync sincroniza una vez. Con DryRun solo arma el informe. Sin Force, se aborta sin aplicar nada
// si quitaría más de sync.max_removal_ratio de las membresías del directorio o si un grupo con
// regla que tenía miembros vuelve vacío (§8.3.4).
func (s *Service) Sync(ctx context.Context, o SyncOptions) (SyncReport, error) {
	if !s.syncing.TryLock() {
		return SyncReport{}, ErrSyncRunning
	}
	defer s.syncing.Unlock()
	r := SyncReport{DryRun: o.DryRun, Forced: o.Force, Deactivated: []Leaver{}, Conflicts: []string{}}
	enabled, err := s.settings(ctx, nil, false)
	if err != nil {
		return r, err
	}
	if !enabled.Enabled {
		return r, ErrSyncDisabled
	}
	mappings, err := s.store.LDAPMappings(ctx)
	if err != nil {
		return r, err
	}
	// Sin reglas no se sincroniza: nadie del directorio puede entrar, y "nadie está en un grupo
	// con regla" no es motivo para dar de baja a todos.
	if len(mappings) == 0 {
		return r, nil
	}
	conn, c, err := s.directory(ctx)
	if err != nil {
		s.failed("sync_unavailable")
		return r, err
	}
	defer conn.Close()
	r.MaxRatio = c.SyncMaxRemovalRatio
	people, err := s.plan(ctx, conn, c, mappings, o, &r)
	if err != nil {
		s.failed("sync_unavailable")
		return r, err
	}
	if o.DryRun {
		return r, nil
	}
	if r.Aborted != "" {
		s.aborted(ctx, o, r)
		return r, nil
	}
	s.audit.Record(ctx, audit.Event{Action: audit.LDAPSyncStart, TargetType: "site", System: system(o),
		Meta: map[string]any{"trigger": o.Trigger, "forced": o.Force}})
	if err := s.apply(ctx, people, &r); err != nil {
		s.failed("sync_failed")
		return r, err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.LDAPSyncDone, TargetType: "site", System: system(o),
		Meta: map[string]any{"trigger": o.Trigger, "forced": o.Force, "created": r.Created, "updated": r.Updated,
			"added": r.Added, "changed": r.Changed, "removed": r.Removed, "deactivated": len(r.Deactivated), "conflicts": len(r.Conflicts)}})
	s.synced(SyncSummary{At: s.now()})
	return r, nil
}

// system es meta.system de lo que hace la sincronización sin una persona detrás.
func system(o SyncOptions) string {
	if o.ActorID == "" {
		return "ldap"
	}
	return ""
}

// plan lee el directorio y arma lo que hay que hacer, sin tocar nada.
func (s *Service) plan(ctx context.Context, conn *goldap.Conn, c config, mappings []repo.LDAPMapping, o SyncOptions, r *SyncReport) ([]*syncPerson, error) {
	rows, err := s.store.LDAPMemberships(ctx)
	if err != nil {
		return nil, err
	}
	had := map[string]int{}
	current := map[string]map[string]repo.LDAPMembership{}
	for _, m := range rows {
		had[m.MappingID]++
		if current[m.UserID] == nil {
			current[m.UserID] = map[string]repo.LDAPMembership{}
		}
		current[m.UserID][m.WsID] = m
	}
	r.Groups, r.Managed = len(mappings), len(rows)

	people := map[string]*syncPerson{}
	var order []string
	for _, m := range mappings {
		var entries []*goldap.Entry
		g, err := findGroup(conn, c, m.GroupGUID)
		switch {
		case errors.Is(err, ErrGroupNotFound):
			// El grupo ya no está bajo group_base_dn: para la regla, está vacío.
		case err != nil:
			return nil, err
		default:
			var truncated bool
			if entries, truncated, err = members(conn, c, g, profileAttrs(c), syncVisits); err != nil {
				return nil, err
			}
			if truncated {
				r.Aborted, r.Group = "truncated", m.GroupName
				return nil, nil
			}
		}
		s.debug("sync group", "group", m.GroupName, "members", len(entries))
		if len(entries) == 0 && had[m.ID] > 0 && !o.Force && r.Aborted == "" {
			r.Aborted, r.Group = "empty_group", m.GroupName
		}
		for _, e := range entries {
			p, err := readProfile(c, e)
			if err != nil {
				s.debug("sync skips entry", "dn", e.DN, "reason", err.Error())
				continue
			}
			sp, ok := people[p.GUID]
			if !ok {
				sp = &syncPerson{profile: p}
				people[p.GUID] = sp
				order = append(order, p.GUID)
			}
			sp.groups = append(sp.groups, Group{GUID: m.GroupGUID, SID: m.GroupSID})
		}
	}
	r.People = len(people)

	accounts, err := s.store.LDAPUsers(ctx)
	if err != nil {
		return nil, err
	}
	var out []*syncPerson
	for _, guid := range order {
		sp := people[guid]
		sp.grants = grants(c, sp.groups, mappings)
		if i := slices.IndexFunc(accounts, func(u repo.User) bool { return u.LDAPGUID == guid }); i >= 0 {
			u := accounts[i]
			if !u.IsActive {
				continue // una baja en la wiki no se deshace sola (§8.4)
			}
			sp.user = &u
			if u.Email != sp.Email || u.FullName != sp.Name || u.LDAPDN != sp.DN {
				r.Updated++
			}
			have := current[u.ID]
			for _, g := range sp.grants {
				switch cur, ok := have[g.WsID]; {
				case !ok:
					r.Added++
				case cur.Role != g.Role:
					r.Changed++
				}
			}
			for ws := range have {
				if !slices.ContainsFunc(sp.grants, func(g Grant) bool { return g.WsID == ws }) {
					r.Removed++
				}
			}
			out = append(out, sp)
			continue
		}
		if !c.PrecreateUsers {
			continue
		}
		if _, err := s.store.UserByEmail(ctx, sp.Email); err == nil {
			r.Conflicts = append(r.Conflicts, sp.Email)
			continue
		} else if !errors.Is(err, repo.ErrNotFound) {
			return nil, err
		}
		r.Created++
		r.Added += len(sp.grants)
		out = append(out, sp)
	}

	// Quien tiene cuenta del directorio y ya no está en ningún grupo con regla se da de baja:
	// deshabilitado, borrado, fuera de user_base_dn o fuera de los grupos (§8.3.3).
	for _, u := range accounts {
		if !u.IsActive || people[u.LDAPGUID] != nil {
			continue
		}
		reason := "no_mapped_group"
		if _, err := s.findByIdentity(conn, c, u.LDAPGUID); err != nil {
			var rej errRejected
			if !errors.As(err, &rej) {
				return nil, err
			}
			switch rej {
			case "disabled":
				reason = "disabled"
			case "outside user_base_dn":
				reason = "outside_base"
			case "ambiguous":
				// Dos entradas con la misma identidad: un error del directorio, no una baja.
				s.log.Warn("ldap sync: ambiguous identity, account left as is", "user", u.ID)
				continue
			default:
				reason = "not_found"
			}
		}
		r.Deactivated = append(r.Deactivated, Leaver{UserID: u.ID, Name: u.FullName, Email: u.Email, Reason: reason})
		r.Removed += len(current[u.ID])
	}
	if r.Managed > 0 {
		r.Ratio = float64(r.Removed) / float64(r.Managed)
	}
	if !o.Force && r.Aborted == "" && r.Ratio > c.SyncMaxRemovalRatio {
		r.Aborted = "removal_ratio"
	}
	return out, nil
}

// apply hace lo que armó plan.
func (s *Service) apply(ctx context.Context, people []*syncPerson, r *SyncReport) error {
	for _, sp := range people {
		var id string
		if sp.user == nil {
			u, err := s.create(ctx, sp.profile)
			if errors.Is(err, auth.ErrAccountConflict) {
				r.Created--
				r.Conflicts = append(r.Conflicts, sp.Email)
				continue
			}
			if err != nil {
				return err
			}
			id = u.ID
		} else {
			id = sp.user.ID
			if err := s.refresh(ctx, *sp.user, sp.profile); errors.Is(err, auth.ErrAccountConflict) {
				r.Conflicts = append(r.Conflicts, sp.Email)
			} else if err != nil {
				return err
			}
		}
		s.photo(ctx, id, sp.Photo)
		if err := s.members.SyncDirectory(ctx, id, directoryGrants(sp.grants)); err != nil {
			return err
		}
	}
	for _, l := range r.Deactivated {
		if s.leavers == nil {
			break
		}
		// La baja de §8.4 sin receptores elegidos: el admin más antiguo de cada área. Desactiva ya
		// y transfiere al terminar la gracia; si se reactiva antes, no se pierde nada.
		_, err := s.leavers.Start(ctx, "", l.UserID, nil, false)
		if errors.Is(err, offboarding.ErrOffboardingPending) || errors.Is(err, offboarding.ErrAlreadyOffboarded) {
			continue
		}
		if err != nil {
			return err
		}
		s.audit.Record(ctx, audit.Event{Action: audit.UserDeactivatedByLDAP, TargetType: "user", TargetID: l.UserID, System: "ldap",
			Meta: map[string]any{"reason": l.Reason}})
	}
	return nil
}

// aborted deja la sincronización abortada en la auditoría y avisa a los superadmins: alguien
// tiene que mirar el directorio y, si el cambio es correcto, forzarla desde el panel.
func (s *Service) aborted(ctx context.Context, o SyncOptions, r SyncReport) {
	s.audit.Record(ctx, audit.Event{Action: audit.LDAPSyncAborted, TargetType: "site", System: system(o),
		Meta: map[string]any{"trigger": o.Trigger, "reason": r.Aborted, "group": r.Group, "removed": r.Removed,
			"managed": r.Managed, "deactivated": len(r.Deactivated)}})
	s.synced(SyncSummary{At: s.now(), Aborted: r.Aborted})
	s.log.Warn("ldap sync aborted", "reason", r.Aborted, "group", r.Group, "removed", r.Removed, "managed", r.Managed)
	if s.mail == nil {
		return
	}
	if on, err := s.mail.Enabled(ctx); err != nil || !on {
		return
	}
	users, err := s.store.ListUsers(ctx)
	if err != nil {
		s.log.Error("ldap sync aborted notice", "err", err)
		return
	}
	for _, u := range users {
		if !u.IsGlobalAdmin || !u.IsActive {
			continue
		}
		err := s.mail.Enqueue(ctx, mail.Message{To: u.Email, Lang: u.Locale, Template: "ldap_sync_aborted",
			URL: strings.TrimSuffix(s.baseURL, "/") + "/admin/ldap", Vars: map[string]string{"name": u.FullName}})
		if err != nil {
			s.log.Error("ldap sync aborted notice", "err", err)
		}
	}
}

// Run sincroniza cada sync.interval mientras ctx viva. La primera vez, un minuto después de
// arrancar: un reinicio no espera una hora para ver las bajas, ni compite con el arranque.
func (s *Service) Run(ctx context.Context) {
	wait := time.Minute
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = defaultSyncInterval
		if c, err := s.settings(ctx, nil, false); err == nil {
			wait = c.syncInterval()
		}
		_, err := s.Sync(ctx, SyncOptions{Trigger: "periodic"})
		if err != nil && !errors.Is(err, ErrSyncDisabled) && !errors.Is(err, ErrSyncRunning) && ctx.Err() == nil {
			s.log.Error("ldap sync failed", "err", err)
		}
	}
}
