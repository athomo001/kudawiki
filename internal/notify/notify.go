// Package notify son los avisos (SPEC §5.3, §12.1): reparte los eventos del outbox a quien le
// toca, agrupa, resuelve el correo con su ventana y el resumen diario, y la baja en un clic.
// También es la puerta de lo que pide cada persona: su bandeja, sus preferencias y seguir páginas.
package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/athomo001/kudawiki/internal/mail"
	"github.com/athomo001/kudawiki/internal/proposals"
	"github.com/athomo001/kudawiki/internal/repo"
)

const (
	pollEvery  = 10 * time.Second
	batchSize  = 100
	digestHour = 8 // el resumen diario sale a las 8, hora local del servidor
	admin      = 3
)

// ErrInvalidPrefs: una categoría o un valor de correo que no existe.
var ErrInvalidPrefs = errors.New("invalid_preferences")

type store interface {
	NotificationEvents(ctx context.Context, limit int) ([]repo.NotificationEvent, error)
	EventPage(ctx context.Context, wsID, pageID string) (repo.EventPage, error)
	PageWatchers(ctx context.Context, e repo.NotificationEvent, p repo.EventPage, unit bool) ([]string, error)
	AreaAdmins(ctx context.Context, wsID string) ([]string, error)
	AutoWatch(ctx context.Context, pageID, userID string) error
	ApplyEvent(ctx context.Context, e repo.NotificationEvent, ns []repo.NewNotification) error
	DueReminders(ctx context.Context, before time.Time) ([]repo.DueReminder, error)
	ApplyReminder(ctx context.Context, d repo.DueReminder, ns []repo.NewNotification) error
	DueNotificationEmails(ctx context.Context, before time.Time, daily bool, limit int) ([]repo.NotificationEmail, error)
	MarkEmailed(ctx context.Context, ids []string, at time.Time) error
	ProposalByID(ctx context.Context, s repo.Scope, id string) (repo.Proposal, error)
	Proposals(ctx context.Context, s repo.Scope, f repo.ProposalFilter) ([]repo.Proposal, error)
	MemberRole(ctx context.Context, s repo.Scope, userID string) (string, error)
	UserByID(ctx context.Context, id string) (repo.User, error)
	// Lo de cada persona.
	Notifications(ctx context.Context, userID string, limit int) ([]repo.Notification, int, error)
	ReadNotifications(ctx context.Context, userID string, ids []string) error
	ReadPageNotifications(ctx context.Context, userID, pageID string) error
	ReadProposalNotifications(ctx context.Context, userID, proposalID string) error
	ReadPendingNotifications(ctx context.Context, s repo.Scope, userID string) error
	NotificationPrefs(ctx context.Context, userID string) (repo.NotificationPrefs, error)
	SetNotificationPrefs(ctx context.Context, userID string, p repo.NotificationPrefs) error
	SetEmailPref(ctx context.Context, userID, category, email string) error
	PageBySlug(ctx context.Context, s repo.Scope, slug string) (repo.Page, error)
	PageWatch(ctx context.Context, s repo.Scope, userID string, p repo.Page) (repo.PageWatch, error)
	Watch(ctx context.Context, s repo.Scope, userID, pageID string, subtree bool) error
	Unwatch(ctx context.Context, s repo.Scope, userID, pageID string) error
}

type mailer interface {
	Enabled(ctx context.Context) (bool, error)
	Enqueue(ctx context.Context, m mail.Message) error
}

// reviewer dice si una propuesta choca con la versión actual de su página (proposals.Service).
type reviewer interface {
	Review(ctx context.Context, sc repo.Scope, userID, id string) (proposals.Detail, error)
}

type Service struct {
	store       store
	mail        mailer
	proposals   reviewer
	baseURL     string
	emailDelay  time.Duration
	remindAfter time.Duration
	unsubKey    []byte
	now         func() time.Time
	lastDigest  time.Time
}

// NewService deriva de la MASTER_KEY la llave de los enlaces de baja: es propia (no la de las
// sesiones) y no se guarda en ningún lado, así un respaldo no sirve para fabricarlos (§12.1).
func NewService(s store, m mailer, p reviewer, masterKey []byte, baseURL string, emailDelay, remindAfter time.Duration, now func() time.Time) *Service {
	mac := hmac.New(sha256.New, masterKey)
	mac.Write([]byte("kudawiki unsubscribe v1"))
	return &Service{store: s, mail: m, proposals: p, baseURL: strings.TrimSuffix(baseURL, "/"), emailDelay: emailDelay,
		remindAfter: remindAfter, unsubKey: mac.Sum(nil), now: now, lastDigest: now()}
}

// Run es el worker: una pasada cada 10 s hasta que ctx termine. Ninguna acción espera por él.
func (s *Service) Run(ctx context.Context, log *slog.Logger) {
	tick := time.NewTicker(pollEvery)
	defer tick.Stop()
	for {
		if err := s.Pass(ctx); err != nil && ctx.Err() == nil {
			log.Error("notifications", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// Pass reparte lo pendiente, arma los recordatorios y resuelve los correos que tocan.
func (s *Service) Pass(ctx context.Context) error {
	if err := s.fanOut(ctx); err != nil {
		return err
	}
	if err := s.remind(ctx); err != nil {
		return err
	}
	if err := s.email(ctx); err != nil {
		return err
	}
	return s.digest(ctx)
}

type payload struct {
	Title string `json:"title"`
	Actor string `json:"actor,omitempty"`
	Days  int    `json:"days,omitempty"`
}

func (p payload) json() string {
	b, _ := json.Marshal(p)
	return string(b)
}

func (s *Service) name(ctx context.Context, userID string) string {
	if u, err := s.store.UserByID(ctx, userID); err == nil {
		return u.FullName
	}
	return ""
}

func (s *Service) fanOut(ctx context.Context) error {
	events, err := s.store.NotificationEvents(ctx, batchSize)
	if err != nil {
		return err
	}
	for _, e := range events {
		ns, err := s.recipients(ctx, e)
		if err != nil {
			return err
		}
		if err := s.store.ApplyEvent(ctx, e, ns); err != nil {
			return err
		}
	}
	return nil
}

// recipients decide a quién le toca un evento. Lo que ya no existe (una página purgada, una
// propuesta de un área borrada) no avisa a nadie. Nadie recibe aviso de lo que hizo él mismo.
func (s *Service) recipients(ctx context.Context, e repo.NotificationEvent) ([]repo.NewNotification, error) {
	full := repo.Scope{WsID: e.WsID, MaxRank: admin} // el worker lee como admin; el acceso de cada uno se mira aparte
	var ns []repo.NewNotification
	switch e.Kind {
	case repo.EventPageSaved, repo.EventPageDeleted:
		page, err := s.store.EventPage(ctx, e.WsID, e.PageID)
		if errors.Is(err, repo.ErrNotFound) {
			return nil, nil
		} else if err != nil {
			return nil, err
		}
		deleted := e.Kind == repo.EventPageDeleted
		skip := []string{e.ActorID}
		if !deleted && e.ActorID != "" {
			// El autor sigue lo que crea, y quien edita lo que edita, si no lo desactivó (§12.1).
			if err := s.store.AutoWatch(ctx, page.ID, e.ActorID); err != nil {
				return nil, err
			}
		}
		if e.ProposalID != "" {
			// Quien aprobó ya sabe que la página cambió.
			if p, err := s.store.ProposalByID(ctx, full, e.ProposalID); err == nil {
				skip = append(skip, p.ReviewerID)
			}
		}
		watchers, err := s.store.PageWatchers(ctx, e, page, deleted)
		if err != nil {
			return nil, err
		}
		kind, group := "page_changed", "page_changed:"+page.ID
		if deleted {
			kind, group = "page_deleted", ""
		}
		body := payload{Title: page.Title, Actor: s.name(ctx, e.ActorID)}.json()
		for _, u := range watchers {
			if !slices.Contains(skip, u) {
				ns = append(ns, repo.NewNotification{UserID: u, Kind: kind, Category: "watched", PageID: page.ID, GroupKey: group, Payload: body})
			}
		}
		if !deleted {
			rebase, err := s.rebase(ctx, e, page)
			if err != nil {
				return nil, err
			}
			ns = append(ns, rebase...)
		}
	case repo.EventProposalSubmitted:
		p, err := s.store.ProposalByID(ctx, full, e.ProposalID)
		if errors.Is(err, repo.ErrNotFound) || (err == nil && p.Status != "pending") {
			return nil, nil
		} else if err != nil {
			return nil, err
		}
		admins, err := s.store.AreaAdmins(ctx, e.WsID)
		if err != nil {
			return nil, err
		}
		// Un solo aviso por área mientras no lo lea: "hay N propuestas por revisar".
		body := payload{Title: p.Title, Actor: p.Author}.json()
		for _, u := range admins {
			if u != p.AuthorID {
				ns = append(ns, repo.NewNotification{UserID: u, Kind: "proposal_pending", Category: "proposals", MinRank: admin,
					GroupKey: "proposal_pending:" + e.WsID, Payload: body})
			}
		}
	case repo.EventProposalResolved:
		p, err := s.store.ProposalByID(ctx, full, e.ProposalID)
		if errors.Is(err, repo.ErrNotFound) {
			return nil, nil
		} else if err != nil {
			return nil, err
		}
		kind := map[string]string{"merged": "proposal_approved", "rejected": "proposal_rejected"}[p.Status]
		if kind == "" || p.AuthorID == e.ActorID {
			return nil, nil
		}
		ns = append(ns, s.forAuthor(p, kind, payload{Title: p.Title, Actor: s.name(ctx, e.ActorID)}))
	}
	return ns, nil
}

// forAuthor es un aviso al autor de una propuesta, uno por propuesta: el último estado reemplaza
// al anterior. El nivel lo pone la página; una página nueva todavía no existe y manda el de la
// propuesta.
func (s *Service) forAuthor(p repo.Proposal, kind string, body payload) repo.NewNotification {
	n := repo.NewNotification{UserID: p.AuthorID, Kind: kind, Category: "my_proposals", PageID: p.PageID, ProposalID: p.ID,
		GroupKey: "proposal:" + p.ID, Payload: body.json(), MinRank: 1}
	if p.PageID == "" {
		n.MinRank = p.MinRank
	}
	return n
}

// rebase avisa a los autores de las propuestas pendientes de la página que, con lo que se acaba
// de publicar, ya no se mezclan solas y hay que rehacerlas (§5.3). Se mira con los permisos del
// autor: si ya no ve la página, no hay aviso.
func (s *Service) rebase(ctx context.Context, e repo.NotificationEvent, page repo.EventPage) ([]repo.NewNotification, error) {
	pending, err := s.store.Proposals(ctx, repo.Scope{WsID: e.WsID, MaxRank: admin}, repo.ProposalFilter{Status: []string{"pending"}, Limit: 200})
	if err != nil {
		return nil, err
	}
	var ns []repo.NewNotification
	for _, p := range pending {
		if p.PageID != page.ID || p.AuthorID == e.ActorID || p.ID == e.ProposalID {
			continue
		}
		role, err := s.store.MemberRole(ctx, repo.Scope{WsID: e.WsID}, p.AuthorID)
		if err != nil {
			return nil, err
		}
		if role == "" {
			continue
		}
		d, err := s.proposals.Review(ctx, repo.Scope{WsID: e.WsID, MaxRank: repo.RoleRank(role)}, p.AuthorID, p.ID)
		if errors.Is(err, repo.ErrNotFound) {
			continue
		} else if err != nil {
			return nil, err
		}
		if d.Conflict {
			ns = append(ns, s.forAuthor(p, "proposal_rebase", payload{Title: page.Title}))
		}
	}
	return ns, nil
}

// remind avisa a los admins de las propuestas que llevan más de approval.remind_after_days
// esperando, una vez por envío (§5.3).
func (s *Service) remind(ctx context.Context) error {
	now := s.now()
	due, err := s.store.DueReminders(ctx, now.Add(-s.remindAfter))
	if err != nil {
		return err
	}
	for _, d := range due {
		admins, err := s.store.AreaAdmins(ctx, d.WsID)
		if err != nil {
			return err
		}
		body := payload{Title: d.Title, Days: int(now.Sub(d.CreatedAt).Hours() / 24)}.json()
		var ns []repo.NewNotification
		for _, u := range admins {
			if u != d.AuthorID {
				ns = append(ns, repo.NewNotification{UserID: u, Kind: "proposal_reminder", Category: "proposals", MinRank: admin,
					GroupKey: "proposal_reminder:" + d.WsID, Payload: body})
			}
		}
		if err := s.store.ApplyReminder(ctx, d, ns); err != nil {
			return err
		}
	}
	return nil
}

// email resuelve los avisos que siguen sin leer pasada la ventana (notify.email_delay): salen
// por correo, o no, según la preferencia. Sin correo configurado solo hay bandeja (§12).
func (s *Service) email(ctx context.Context) error {
	now := s.now()
	due, err := s.store.DueNotificationEmails(ctx, now.Add(-s.emailDelay), false, batchSize)
	if err != nil || len(due) == 0 {
		return err
	}
	enabled, err := s.mail.Enabled(ctx)
	if err != nil {
		return err
	}
	done := make([]string, 0, len(due))
	for _, n := range due {
		if enabled && n.Pref == "immediate" {
			m := mail.Message{To: n.Email, Lang: n.Locale, Template: n.Kind, Vars: s.vars(n), URL: s.link(n.Notification),
				Unsubscribe: s.unsubscribeURL(n.UserID, n.Category)}
			if err := s.mail.Enqueue(ctx, m); err != nil {
				return err
			}
		}
		done = append(done, n.ID)
	}
	return s.store.MarkEmailed(ctx, done, now)
}

// digest manda, una vez al día, un solo correo con los avisos sin leer de quienes eligieron el
// resumen diario.
func (s *Service) digest(ctx context.Context) error {
	now := s.now()
	at := time.Date(now.Year(), now.Month(), now.Day(), digestHour, 0, 0, 0, now.Location())
	if now.Before(at) || !s.lastDigest.Before(at) {
		return nil
	}
	due, err := s.store.DueNotificationEmails(ctx, now, true, 1000)
	if err != nil {
		return err
	}
	enabled, err := s.mail.Enabled(ctx)
	if err != nil {
		return err
	}
	byUser := map[string][]repo.NotificationEmail{}
	var users []string
	for _, n := range due {
		if byUser[n.UserID] == nil {
			users = append(users, n.UserID)
		}
		byUser[n.UserID] = append(byUser[n.UserID], n)
	}
	for _, u := range users {
		list := byUser[u]
		ids := make([]string, 0, len(list))
		lines := make([]mail.Line, 0, len(list))
		for _, n := range list {
			ids = append(ids, n.ID)
			lines = append(lines, mail.Line{Template: n.Kind, Vars: s.vars(n)})
		}
		if enabled {
			first := list[0]
			m := mail.Message{To: first.Email, Lang: first.Locale, Template: "digest", Lines: lines, URL: s.baseURL + "/",
				Vars: map[string]string{"name": first.FullName, "count": strconv.Itoa(len(list))}, Unsubscribe: s.unsubscribeURL(u, "all")}
			if err := s.mail.Enqueue(ctx, m); err != nil {
				return err
			}
		}
		if err := s.store.MarkEmailed(ctx, ids, now); err != nil {
			return err
		}
	}
	s.lastDigest = now
	return nil
}

func (s *Service) vars(n repo.NotificationEmail) map[string]string {
	var p payload
	_ = json.Unmarshal([]byte(n.Payload), &p)
	return map[string]string{"name": n.FullName, "title": p.Title, "actor": p.Actor, "area": n.AreaName,
		"days": strconv.Itoa(p.Days), "count": strconv.Itoa(n.Count)}
}

// link es a dónde lleva un aviso: la página, la propuesta o la bandeja del área. Las mismas
// rutas que arma la interfaz.
func (s *Service) link(n repo.Notification) string {
	area := s.baseURL + "/w/" + n.AreaSlug
	switch n.Kind {
	case "proposal_pending", "proposal_reminder":
		return area + "/proposals"
	case "proposal_approved", "proposal_rejected", "proposal_rebase":
		return area + "/proposals/" + n.ProposalID
	}
	if n.PageSlug != "" {
		return area + "/p/" + n.PageSlug
	}
	return area
}

// unsubscribeURL es el enlace de baja de una categoría ("all": el resumen, todas). Lleva el id
// de la persona, la categoría y una firma: no hay que iniciar sesión para usarlo (RFC 8058).
func (s *Service) unsubscribeURL(userID, category string) string {
	return s.baseURL + "/unsubscribe?t=" + userID + "." + category + "." + s.sign(userID, category)
}

func (s *Service) sign(userID, category string) string {
	mac := hmac.New(sha256.New, s.unsubKey)
	mac.Write([]byte(userID + "." + category))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:16])
}

// Unsubscribe apaga el correo de la categoría del enlace. Un enlace adulterado es el 404
// uniforme; usarlo dos veces no es un error.
func (s *Service) Unsubscribe(ctx context.Context, token string) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || !hmac.Equal([]byte(parts[2]), []byte(s.sign(parts[0], parts[1]))) {
		return repo.ErrNotFound
	}
	categories := []string{parts[1]}
	if parts[1] == "all" {
		categories = repo.NotificationCategories
	} else if !slices.Contains(repo.NotificationCategories, parts[1]) {
		return repo.ErrNotFound
	}
	for _, c := range categories {
		if err := s.store.SetEmailPref(ctx, parts[0], c, "off"); err != nil {
			return err
		}
	}
	return nil
}

// List es la bandeja: los últimos avisos y cuántos sin leer.
func (s *Service) List(ctx context.Context, userID string) ([]repo.Notification, int, error) {
	return s.store.Notifications(ctx, userID, 30)
}

// Read marca como leídos esos avisos, o todos.
func (s *Service) Read(ctx context.Context, userID string, ids []string) error {
	return s.store.ReadNotifications(ctx, userID, ids)
}

// PageOpened, ProposalOpened y PendingOpened: ver lo avisado lo marca como leído, y si el correo
// todavía no salió, ya no sale (§12.1).
func (s *Service) PageOpened(ctx context.Context, userID, pageID string) error {
	return s.store.ReadPageNotifications(ctx, userID, pageID)
}

func (s *Service) ProposalOpened(ctx context.Context, userID, proposalID string) error {
	return s.store.ReadProposalNotifications(ctx, userID, proposalID)
}

func (s *Service) PendingOpened(ctx context.Context, sc repo.Scope, userID string) error {
	return s.store.ReadPendingNotifications(ctx, sc, userID)
}

func (s *Service) Prefs(ctx context.Context, userID string) (repo.NotificationPrefs, error) {
	return s.store.NotificationPrefs(ctx, userID)
}

func (s *Service) SetPrefs(ctx context.Context, userID string, p repo.NotificationPrefs) error {
	for c, e := range p.Email {
		if !slices.Contains(repo.NotificationCategories, c) || (e != "immediate" && e != "daily" && e != "off") {
			return ErrInvalidPrefs
		}
	}
	return s.store.SetNotificationPrefs(ctx, userID, p)
}

// WatchOf dice cómo sigue la persona una página que puede ver.
func (s *Service) WatchOf(ctx context.Context, sc repo.Scope, userID string, p repo.Page) (repo.PageWatch, error) {
	return s.store.PageWatch(ctx, sc, userID, p)
}

// Watch sigue una página ("page"), con sus subpáginas ("subtree"), o la deja de seguir ("").
// Una página que la persona no ve es el 404 uniforme.
func (s *Service) Watch(ctx context.Context, sc repo.Scope, userID, slug, mode string) error {
	if mode != "" && mode != "page" && mode != "subtree" {
		return ErrInvalidPrefs
	}
	p, err := s.store.PageBySlug(ctx, sc, slug)
	if err != nil {
		return err
	}
	if mode == "" {
		return s.store.Unwatch(ctx, sc, userID, p.ID)
	}
	return s.store.Watch(ctx, sc, userID, p.ID, mode == "subtree")
}
