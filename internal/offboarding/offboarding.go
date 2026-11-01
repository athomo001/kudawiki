// Package offboarding es la baja de usuarios (SPEC §8.4): desactivar a una persona, pasar lo
// suyo de cada área al admin que lo recibe y borrar lo que nunca se publicó. También da y quita
// el superadmin desde el panel (B15) y, pasado un año de la baja, seudonimiza a la persona en
// la auditoría (B14, §13.10).
package offboarding

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/repo"
)

var (
	// ErrSelf: nadie se da de baja ni se quita el superadmin a sí mismo; así siempre queda uno.
	ErrSelf               = errors.New("cannot_change_self")
	ErrAlreadyOffboarded  = errors.New("already_offboarded")
	ErrOffboardingPending = errors.New("offboarding_pending")
	ErrAlreadyActive      = errors.New("already_active")
	ErrUserInactive       = errors.New("user_inactive")
	ErrInvalidReceiver    = errors.New("invalid_receiver")
	// ErrNeedsAdmin: quien se va es el último admin de un área; antes de completar la baja hay
	// que nombrar otro (§8.4).
	ErrNeedsAdmin = errors.New("area_needs_admin")
)

const system = "offboarding" // meta.system de lo que hace el job

type store interface {
	UserByID(ctx context.Context, id string) (repo.User, error)
	OffboardingPlan(ctx context.Context, userID string) (repo.OffboardingPlan, error)
	StartOffboarding(ctx context.Context, o repo.Offboarding) (int64, error)
	OffboardingOf(ctx context.Context, userID string) (repo.Offboarding, error)
	DueOffboardings(ctx context.Context, now time.Time) ([]string, error)
	CompleteOffboarding(ctx context.Context, userID string, receivers map[string]string, at time.Time) (repo.OffboardingResult, error)
	Reactivate(ctx context.Context, userID string) error
	SetGlobalAdmin(ctx context.Context, userID string, on bool) error
	AddNotifications(ctx context.Context, wsID string, ns []repo.NewNotification) error
	DuePseudonyms(ctx context.Context, cutoff time.Time) ([]string, error)
	Pseudonymize(ctx context.Context, userID, pseudonym string) (int64, error)
}

type files interface {
	Remove(wsID, id string) error
}

type Service struct {
	store             store
	files             files
	audit             *audit.Recorder
	grace             time.Duration // offboarding.grace_days
	pseudonymizeAfter time.Duration // audit.pseudonymize_after_days
	key               []byte
	now               func() time.Time
	log               *slog.Logger
}

// Config son los plazos de la baja (§8.4, §13.10).
type Config struct {
	Grace, PseudonymizeAfter time.Duration
}

// NewService deriva de la MASTER_KEY la llave de los seudónimos: sin ella, nadie puede calcular
// el de un correo conocido para ver qué hizo esa persona; con ella, una investigación sí (§13.10).
func NewService(s store, f files, rec *audit.Recorder, masterKey []byte, c Config, now func() time.Time, log *slog.Logger) *Service {
	mac := hmac.New(sha256.New, masterKey)
	mac.Write([]byte("kudawiki audit pseudonym v1"))
	return &Service{store: s, files: f, audit: rec, grace: c.Grace, pseudonymizeAfter: c.PseudonymizeAfter, key: mac.Sum(nil), now: now, log: log}
}

// Area es lo que la persona tiene en un área y quién lo recibe por defecto: el admin más
// antiguo; si el área no tiene admin, quien hace la baja (ToActor). NeedsAdmin: era su último
// admin.
type Area struct {
	repo.OffboardingArea
	Receiver   string
	ToActor    bool
	NeedsAdmin bool
}

// Plan es el resumen previo a la baja (§8.4).
type Plan struct {
	Areas         []Area
	PersonalPages int
	PersonalMedia int
	Drafts        int
	Proposals     int
	Grace         time.Duration
	Pending       *repo.Offboarding // la baja en curso, si hay
}

// Plan arma el resumen para quien hace la baja (actorID, el superadmin).
func (s *Service) Plan(ctx context.Context, actorID, userID string) (Plan, error) {
	u, err := s.store.UserByID(ctx, userID)
	if err != nil {
		return Plan{}, err
	}
	p, err := s.store.OffboardingPlan(ctx, u.ID)
	if err != nil {
		return Plan{}, err
	}
	out := Plan{PersonalPages: p.PersonalPages, PersonalMedia: p.PersonalMedia, Drafts: p.Drafts, Proposals: p.Proposals, Grace: s.grace}
	for _, a := range p.Areas {
		out.Areas = append(out.Areas, defaults(a, actorID))
	}
	if o, err := s.store.OffboardingOf(ctx, u.ID); err == nil {
		out.Pending = &o
	} else if !errors.Is(err, repo.ErrNotFound) {
		return Plan{}, err
	}
	return out, nil
}

func defaults(a repo.OffboardingArea, fallback string) Area {
	out := Area{OffboardingArea: a}
	switch {
	case len(a.Admins) > 0:
		out.Receiver = a.Admins[0].ID
	case a.Role == "admin":
		out.NeedsAdmin = true
	default:
		out.Receiver, out.ToActor = fallback, true
	}
	return out
}

// receivers decide quién recibe lo de cada área con algo que transferir. chosen es lo elegido en
// la pantalla de baja; strict rechaza una elección que no es admin del área (el pedido del panel)
// en lugar de volver al valor por defecto (el job, si el elegido dejó de ser admin durante la
// gracia). Devuelve también las áreas que se quedaron sin admin.
func receivers(p repo.OffboardingPlan, chosen map[string]string, fallback string, strict bool) (map[string]string, []string, error) {
	out := map[string]string{}
	var needs []string
	for _, a := range p.Areas {
		d := defaults(a, fallback)
		if d.NeedsAdmin {
			needs = append(needs, a.Slug)
		}
		if a.Pages+a.Quizzes+a.ReadRequests == 0 {
			continue
		}
		pick, ok := chosen[a.WsID]
		switch {
		case !ok || pick == "":
			pick = d.Receiver
		case pick == d.Receiver:
		case !isAdmin(a.Admins, pick):
			if strict {
				return nil, nil, ErrInvalidReceiver
			}
			pick = d.Receiver
		}
		if pick != "" {
			out[a.WsID] = pick
		}
	}
	return out, needs, nil
}

func isAdmin(admins []repo.AreaAdmin, id string) bool {
	for _, a := range admins {
		if a.ID == id {
			return true
		}
	}
	return false
}

// Outcome dice en qué quedó el pedido: la baja empezó (Until) o se completó.
type Outcome struct {
	Completed bool
	Until     time.Time
	Result    repo.OffboardingResult
}

// Start inicia la baja: la cuenta queda desactivada ya, con las sesiones cerradas, y lo demás
// se hace al terminar la gracia. Con now la completa en el acto (la ruta pide reautenticarse);
// con una baja ya en curso, now es la forma de adelantarla.
func (s *Service) Start(ctx context.Context, actorID, userID string, chosen map[string]string, now bool) (Outcome, error) {
	if actorID == userID {
		return Outcome{}, ErrSelf
	}
	u, err := s.store.UserByID(ctx, userID)
	if err != nil {
		return Outcome{}, err
	}
	if !u.OffboardedAt.IsZero() {
		return Outcome{}, ErrAlreadyOffboarded
	}
	pending := !u.OffboardingAt.IsZero()
	if pending && !now {
		return Outcome{}, ErrOffboardingPending
	}
	p, err := s.store.OffboardingPlan(ctx, u.ID)
	if err != nil {
		return Outcome{}, err
	}
	strict := true
	if pending && len(chosen) == 0 {
		// Adelantar una baja en curso sin elegir de nuevo: vale lo elegido al iniciarla.
		o, err := s.store.OffboardingOf(ctx, u.ID)
		if err != nil {
			return Outcome{}, err
		}
		chosen, strict = o.Receivers, false
	}
	to, needs, err := receivers(p, chosen, actorID, strict)
	if err != nil {
		return Outcome{}, err
	}
	if now && len(needs) > 0 {
		return Outcome{}, ErrNeedsAdmin
	}
	if !pending {
		until := s.now().Add(s.grace)
		closed, err := s.store.StartOffboarding(ctx, repo.Offboarding{UserID: u.ID, Until: until, Receivers: to, By: actorID})
		if err != nil {
			return Outcome{}, err
		}
		s.audit.Record(ctx, audit.Event{Action: audit.UserOffboardingStart, TargetType: "user", TargetID: u.ID,
			Meta: map[string]any{"until": until.UTC().Format(time.DateOnly), "sessions_closed": closed, "now": now}})
		if !now {
			return Outcome{Until: until}, nil
		}
	}
	res, err := s.complete(ctx, u, to, actorID, "")
	return Outcome{Completed: true, Result: res}, err
}

// complete hace la baja y registra cada parte. actorID no recibe aviso de lo que se le pasó a
// él mismo: lo acaba de pedir.
func (s *Service) complete(ctx context.Context, u repo.User, to map[string]string, actorID, sys string) (repo.OffboardingResult, error) {
	res, err := s.store.CompleteOffboarding(ctx, u.ID, to, s.now())
	if err != nil {
		return res, err
	}
	var pages, quizzes, reads int
	for _, t := range res.Transfers {
		pages, quizzes, reads = pages+len(t.PageIDs), quizzes+t.Quizzes, reads+t.ReadRequests
		for _, id := range t.PageIDs {
			s.audit.Record(ctx, audit.Event{Action: audit.PageOwnerTransfer, WsID: t.WsID, TargetType: "page", TargetID: id, System: sys,
				Meta: map[string]any{"from": u.ID, "to": t.To}})
		}
		if t.To == actorID {
			continue
		}
		if err := s.store.AddNotifications(ctx, t.WsID, []repo.NewNotification{t.Notice(u.FullName)}); err != nil {
			// La baja ya está hecha y auditada: sin el aviso, quien la recibió igual es dueño.
			s.log.Error("ownership notice failed", "workspace", t.WsID, "err", err)
		}
	}
	if res.PersonalWsID != "" {
		s.audit.Record(ctx, audit.Event{Action: audit.PersonalPurge, WsID: res.PersonalWsID, TargetType: "user", TargetID: u.ID, System: sys,
			Meta: map[string]any{"pages": res.PersonalPages, "media": len(res.PersonalMedia)}})
	}
	s.audit.Record(ctx, audit.Event{Action: audit.UserOffboardingComplete, TargetType: "user", TargetID: u.ID, System: sys,
		Meta: map[string]any{"pages": pages, "quizzes": quizzes, "read_requests": reads, "personal_pages": res.PersonalPages,
			"personal_media": len(res.PersonalMedia), "drafts": res.Drafts, "proposals": res.Proposals}})
	// Un archivo que no se pudo borrar ya no tiene fila: lo encuentra la salud del contenido (§13.7).
	for _, f := range res.PersonalMedia {
		if err := s.files.Remove(f.WsID, f.ID); err != nil {
			s.log.Error("personal media remove failed", "workspace", f.WsID, "media", f.ID, "err", err)
		}
	}
	return res, nil
}

// Reactivate vuelve a activar una cuenta (B15). Durante la gracia anula la baja sin perder nada.
func (s *Service) Reactivate(ctx context.Context, actorID, userID string) error {
	if actorID == userID {
		return ErrSelf
	}
	u, err := s.store.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	if err := s.store.Reactivate(ctx, u.ID); err != nil {
		if errors.Is(err, repo.ErrConflict) {
			return ErrAlreadyActive
		}
		return err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.UserReactivate, TargetType: "user", TargetID: u.ID,
		Meta: map[string]any{"cancelled_offboarding": !u.OffboardingAt.IsZero(), "after_offboarding": !u.OffboardedAt.IsZero()}})
	return nil
}

// SetSuperadmin da o quita el superadmin (B15). Solo a cuentas activas: una desactivada no entra,
// y al completar su baja lo pierde igual.
func (s *Service) SetSuperadmin(ctx context.Context, actorID, userID string, on bool) error {
	if actorID == userID {
		return ErrSelf
	}
	u, err := s.store.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	if !u.IsActive {
		return ErrUserInactive
	}
	if u.IsGlobalAdmin == on {
		return nil
	}
	if err := s.store.SetGlobalAdmin(ctx, u.ID, on); err != nil {
		return err
	}
	action := audit.UserSuperadminRevoke
	if on {
		action = audit.UserSuperadminGrant
	}
	s.audit.Record(ctx, audit.Event{Action: action, TargetType: "user", TargetID: u.ID})
	return nil
}

// Run completa las bajas cuya gracia venció y seudonimiza a quienes se fueron hace más de
// audit.pseudonymize_after_days. Una baja que no se puede completar queda para la próxima pasada.
func (s *Service) Run(ctx context.Context) error {
	now := s.now()
	due, err := s.store.DueOffboardings(ctx, now)
	if err != nil {
		return err
	}
	var errs error
	for _, id := range due {
		errs = errors.Join(errs, s.completeDue(ctx, id))
	}
	ids, err := s.store.DuePseudonyms(ctx, now.Add(-s.pseudonymizeAfter))
	if err != nil {
		return errors.Join(errs, err)
	}
	for _, id := range ids {
		u, err := s.store.UserByID(ctx, id)
		if err != nil {
			errs = errors.Join(errs, err)
			continue
		}
		n, err := s.store.Pseudonymize(ctx, u.ID, s.pseudonym(u.Email))
		if err != nil {
			errs = errors.Join(errs, err)
			continue
		}
		s.audit.Record(ctx, audit.Event{Action: audit.UserPseudonymize, TargetType: "user", TargetID: u.ID, System: system,
			Meta: map[string]any{"events": n}})
	}
	return errs
}

func (s *Service) completeDue(ctx context.Context, userID string) error {
	o, err := s.store.OffboardingOf(ctx, userID)
	if err != nil {
		return err
	}
	u, err := s.store.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	p, err := s.store.OffboardingPlan(ctx, userID)
	if err != nil {
		return err
	}
	to, needs, _ := receivers(p, o.Receivers, o.By, false)
	if len(needs) > 0 {
		// Sigue desactivada y nada se pierde: el panel muestra qué área necesita un admin.
		s.log.Warn("offboarding waits for an area admin", "user", userID, "areas", strings.Join(needs, ","))
		return nil
	}
	_, err = s.complete(ctx, u, to, "", system)
	return err
}

// pseudonym es el mismo para la misma persona en todos sus eventos: la auditoría sigue
// mostrando qué hizo cada quien, sin decir quién era.
func (s *Service) pseudonym(email string) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(strings.ToLower(email)))
	return hex.EncodeToString(mac.Sum(nil))[:16]
}

// Daily corre Run al arrancar y después cada 24 horas, hasta que ctx termine.
func (s *Service) Daily(ctx context.Context) {
	tick := time.NewTicker(24 * time.Hour)
	defer tick.Stop()
	for {
		if err := s.Run(ctx); err != nil && ctx.Err() == nil {
			s.log.Error("offboarding job failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
