// Package accessreview es la revisión periódica de accesos (SPEC §21.2.7, ISO 27001 5.18): cada
// cierto tiempo (access_review.interval_days, 90 por omisión) el admin de cada área confirma la
// lista de miembros y roles, y el superadmin la de admins y superadmins. Queda quién confirmó,
// cuándo y qué vio; al vencer se avisa.
package accessreview

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/mail"
	"github.com/athomo001/kudawiki/internal/repo"
)

// NoticeJob es la fila de job_state con el vencimiento ya avisado de cada revisión: un aviso
// por vencimiento, no uno por pasada.
const NoticeJob = "access_review_notice"

// global es la clave de la revisión del superadmin en el estado de los avisos.
const global = "_global"

type store interface {
	Members(ctx context.Context, s repo.Scope) ([]repo.Member, error)
	LastAreaAccessReview(ctx context.Context, s repo.Scope) (repo.AccessReview, bool, time.Time, error)
	LastGlobalAccessReview(ctx context.Context) (repo.AccessReview, bool, time.Time, error)
	AddAccessReview(ctx context.Context, wsID, reviewerID, reviewerName, snapshot string, at time.Time) error
	AdminAccessList(ctx context.Context) ([]repo.AdminAccess, error)
	AreaAccessReviews(ctx context.Context) ([]repo.AreaReview, error)
	AreaAdmins(ctx context.Context, wsID string) ([]string, error)
	AddNotifications(ctx context.Context, wsID string, ns []repo.NewNotification) error
	ListUsers(ctx context.Context) ([]repo.User, error)
	JobState(ctx context.Context, name string) (string, error)
	SetJobState(ctx context.Context, name, valueJSON string) error
}

// mailer es el correo a los superadmins, que no tienen un área donde dejar el aviso.
type mailer interface {
	Enabled(ctx context.Context) (bool, error)
	Enqueue(ctx context.Context, m mail.Message) error
}

type Service struct {
	store   store
	audit   *audit.Recorder
	mail    mailer
	every   time.Duration
	baseURL string
	now     func() time.Time
}

func NewService(s store, rec *audit.Recorder, m mailer, every time.Duration, baseURL string, now func() time.Time) *Service {
	return &Service{store: s, audit: rec, mail: m, every: every, baseURL: strings.TrimSuffix(baseURL, "/"), now: now}
}

// Status es el estado de una revisión: la última confirmación (nil si nunca) y cuándo vence.
type Status struct {
	Last    *repo.AccessReview
	DueAt   time.Time
	Overdue bool
}

func (s *Service) status(last repo.AccessReview, ok bool, since time.Time) Status {
	st := Status{DueAt: since.Add(s.every)}
	if ok {
		st.Last = &last
	}
	st.Overdue = !s.now().Before(st.DueAt)
	return st
}

// Area es la revisión del área: su estado y la lista que hay que confirmar.
func (s *Service) Area(ctx context.Context, sc repo.Scope) (Status, []repo.Member, error) {
	last, ok, since, err := s.store.LastAreaAccessReview(ctx, sc)
	if err != nil {
		return Status{}, nil, err
	}
	ms, err := s.store.Members(ctx, sc)
	return s.status(last, ok, since), ms, err
}

// ConfirmArea deja constancia de que el admin revisó la lista tal como está ahora. Lo que haya
// que corregir se corrige antes en Miembros: confirmar es decir "así está bien".
func (s *Service) ConfirmArea(ctx context.Context, sc repo.Scope, u repo.User) error {
	ms, err := s.store.Members(ctx, sc)
	if err != nil {
		return err
	}
	type entry struct {
		UserID string `json:"user_id"`
		Email  string `json:"email"`
		Role   string `json:"role"`
	}
	snap := make([]entry, len(ms))
	for i, m := range ms {
		snap[i] = entry{m.UserID, m.Email, m.Role}
	}
	return s.confirm(ctx, sc.WsID, u, snap, len(ms))
}

// Global es la revisión del superadmin: admins y superadmins, y cómo va la de cada área.
func (s *Service) Global(ctx context.Context) (Status, []repo.AdminAccess, []AreaStatus, error) {
	last, ok, since, err := s.store.LastGlobalAccessReview(ctx)
	if err != nil {
		return Status{}, nil, nil, err
	}
	admins, err := s.store.AdminAccessList(ctx)
	if err != nil {
		return Status{}, nil, nil, err
	}
	areas, err := s.store.AreaAccessReviews(ctx)
	if err != nil {
		return Status{}, nil, nil, err
	}
	out := make([]AreaStatus, len(areas))
	for i, a := range areas {
		out[i] = AreaStatus{Slug: a.Slug, Name: a.Name, Status: s.status(deref(a.Last), a.Last != nil, a.Since)}
	}
	return s.status(last, ok, since), admins, out, nil
}

// AreaStatus es una fila de la tabla de áreas en la vista del superadmin.
type AreaStatus struct {
	Slug, Name string
	Status
}

func deref(a *repo.AccessReview) repo.AccessReview {
	if a == nil {
		return repo.AccessReview{}
	}
	return *a
}

// ConfirmGlobal deja constancia de la revisión de admins y superadmins.
func (s *Service) ConfirmGlobal(ctx context.Context, u repo.User) error {
	admins, err := s.store.AdminAccessList(ctx)
	if err != nil {
		return err
	}
	type entry struct {
		UserID     string   `json:"user_id"`
		Email      string   `json:"email"`
		Superadmin bool     `json:"superadmin"`
		Active     bool     `json:"active"`
		Areas      []string `json:"areas,omitempty"`
	}
	snap := make([]entry, len(admins))
	for i, a := range admins {
		snap[i] = entry{a.UserID, a.Email, a.Superadmin, a.Active, a.Areas}
	}
	return s.confirm(ctx, "", u, snap, len(admins))
}

func (s *Service) confirm(ctx context.Context, wsID string, u repo.User, snap any, n int) error {
	b, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	if err := s.store.AddAccessReview(ctx, wsID, u.ID, u.FullName, string(b), s.now()); err != nil {
		return err
	}
	// La lista entera va en la tabla; a la auditoría, cuántos (el actor sale del contexto de la
	// petición): es evidencia, no un respaldo.
	s.audit.Record(ctx, audit.Event{WsID: wsID, Action: audit.AccessReviewConfirm,
		TargetType: "access_review", Meta: map[string]any{"entries": n}})
	return nil
}

// Run avisa de las revisiones vencidas, una vez por hora, hasta que ctx termine.
func (s *Service) Run(ctx context.Context, log *slog.Logger) {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		if err := s.Notices(ctx); err != nil && ctx.Err() == nil {
			log.Error("access review notices", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// Notices avisa una vez por cada vencimiento: a los admins del área en su bandeja (con correo,
// es un aviso de la cuenta que no se apaga, §12.1) y a los superadmins por correo.
func (s *Service) Notices(ctx context.Context) error {
	noticed := map[string]string{}
	if raw, err := s.store.JobState(ctx, NoticeJob); err != nil {
		return err
	} else if raw != "" {
		_ = json.Unmarshal([]byte(raw), &noticed)
	}
	changed := false
	areas, err := s.store.AreaAccessReviews(ctx)
	if err != nil {
		return err
	}
	for _, a := range areas {
		st := s.status(deref(a.Last), a.Last != nil, a.Since)
		due := st.DueAt.UTC().Format(time.DateOnly)
		if !st.Overdue || noticed[a.ID] == due {
			continue
		}
		admins, err := s.store.AreaAdmins(ctx, a.ID)
		if err != nil {
			return err
		}
		ns := make([]repo.NewNotification, len(admins))
		for i, id := range admins {
			ns[i] = repo.NewNotification{UserID: id, Kind: "access_review_due", Category: "account", MinRank: 3,
				GroupKey: "access_review:" + a.ID}
		}
		if err := s.store.AddNotifications(ctx, a.ID, ns); err != nil {
			return err
		}
		s.audit.Record(ctx, audit.Event{WsID: a.ID, Action: audit.AccessReviewDue, TargetType: "access_review", System: "jobs",
			Meta: map[string]any{"due": due, "notified": len(admins)}})
		noticed[a.ID], changed = due, true
	}
	last, ok, since, err := s.store.LastGlobalAccessReview(ctx)
	if err != nil {
		return err
	}
	if st := s.status(last, ok, since); !since.IsZero() && st.Overdue && noticed[global] != st.DueAt.UTC().Format(time.DateOnly) {
		n, err := s.mailSuperadmins(ctx)
		if err != nil {
			return err
		}
		due := st.DueAt.UTC().Format(time.DateOnly)
		s.audit.Record(ctx, audit.Event{Action: audit.AccessReviewDue, TargetType: "access_review", System: "jobs",
			Meta: map[string]any{"due": due, "notified": n}})
		noticed[global], changed = due, true
	}
	if !changed {
		return nil
	}
	b, _ := json.Marshal(noticed)
	return s.store.SetJobState(ctx, NoticeJob, string(b))
}

func (s *Service) mailSuperadmins(ctx context.Context) (int, error) {
	if on, err := s.mail.Enabled(ctx); err != nil || !on {
		return 0, err
	}
	users, err := s.store.ListUsers(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, u := range users {
		if !u.IsGlobalAdmin || !u.IsActive {
			continue
		}
		if err := s.mail.Enqueue(ctx, mail.Message{To: u.Email, Lang: u.Locale, Template: "access_review_global",
			URL: s.baseURL + "/admin/access-review", Vars: map[string]string{"name": u.FullName}}); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
