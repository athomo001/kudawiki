// Package reading es el acuse de lectura (SPEC §4.14): pedir que se lea una página, confirmar
// que se leyó y ver quién lo hizo.
package reading

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/oklog/ulid/v2"
)

var (
	// ErrInvalidAudience: la solicitud apunta a quien no puede recibirla (un editor pidiéndosela a
	// otro editor, alguien sin el nivel de la página) o no le llegaría a nadie.
	ErrInvalidAudience = errors.New("invalid_audience")
	// ErrInvalidRequest: el texto es demasiado largo o el plazo ya pasó.
	ErrInvalidRequest = errors.New("invalid_read_request")
	// ErrPageChanged: la página cambió desde que la persona la abrió; tiene que confirmar lo que
	// hay ahora, no lo que leyó antes.
	ErrPageChanged = errors.New("page_changed")
	// ErrQuizRequired: la solicitud se cumple aprobando su cuestionario, no con el botón.
	ErrQuizRequired = errors.New("quiz_required")
)

const (
	editor, admin = 2, 3
	maxStatement  = 500
	maxUsers      = 500
)

type store interface {
	PageBySlug(ctx context.Context, s repo.Scope, slug string) (repo.Page, error)
	Members(ctx context.Context, s repo.Scope) ([]repo.Member, error)
	CreateReadRequest(ctx context.Context, s repo.Scope, rr repo.ReadRequest) error
	ReadRequestByID(ctx context.Context, s repo.Scope, id string) (repo.ReadRequest, error)
	ReadRequests(ctx context.Context, s repo.Scope, createdBy string) ([]repo.ReadRequest, error)
	Recipients(ctx context.Context, s repo.Scope, requestID string) ([]repo.Recipient, error)
	AudienceSize(ctx context.Context, s repo.Scope, pageID, createdBy string, a repo.Audience) (int, error)
	CloseReadRequest(ctx context.Context, s repo.Scope, id string) error
	MyReads(ctx context.Context, s repo.Scope, userID, pageID string) ([]repo.MyRead, error)
	RecordReadViews(ctx context.Context, userID string, rev int, requestIDs []string, at time.Time) error
	Acknowledge(ctx context.Context, requestID, userID string, rev int) error
}

// quizzes es lo que el acuse necesita de los cuestionarios (§4.16): si uno se puede pedir junto
// con la lectura y cómo va cada destinatario.
type quizzes interface {
	Assignable(ctx context.Context, sc repo.Scope, pageID, quizID string) error
	Progress(ctx context.Context, sc repo.Scope, quizID, requestID string) (map[string]repo.QuizProgress, int, error)
}

type Service struct {
	store   store
	quizzes quizzes
	audit   *audit.Recorder
	now     func() time.Time
}

func NewService(s store, q quizzes, rec *audit.Recorder, now func() time.Time) *Service {
	return &Service{store: s, quizzes: q, audit: rec, now: now}
}

func (s *Service) record(ctx context.Context, sc repo.Scope, a audit.Action, id string, meta map[string]any) {
	s.audit.Record(ctx, audit.Event{Action: a, WsID: sc.WsID, TargetType: "read_request", TargetID: id, Meta: meta})
}

// Input es lo que se pide. Audience.All de un editor quiere decir todos los lectores. QuizID: el
// cuestionario de la página que hay que aprobar (§4.16); vacío, basta con confirmar.
type Input struct {
	Slug, Statement, QuizID string
	DueAt                   time.Time // cero = sin plazo
	Audience                repo.Audience
}

// Create guarda una solicitud de lectura. Siempre de arriba hacia abajo (§4.14, §19.25): el
// admin del área a todo el área, a unos roles o a unas personas; un editor solo a lectores y solo
// sobre páginas N1 (las que un lector puede leer). Un lector no pide: para él la ruta no existe.
func (s *Service) Create(ctx context.Context, sc repo.Scope, actorID string, in Input) (string, error) {
	if sc.MaxRank < editor {
		return "", repo.ErrNotFound
	}
	page, err := s.store.PageBySlug(ctx, sc, in.Slug)
	if err != nil {
		return "", err
	}
	in.Statement = strings.TrimSpace(in.Statement)
	if utf8.RuneCountInString(in.Statement) > maxStatement || (!in.DueAt.IsZero() && !in.DueAt.After(s.now())) {
		return "", ErrInvalidRequest
	}
	if in.QuizID != "" {
		if err := s.quizzes.Assignable(ctx, sc, page.ID, in.QuizID); errors.Is(err, repo.ErrNotFound) {
			return "", ErrInvalidRequest
		} else if err != nil {
			return "", err
		}
	}
	aud, err := s.audience(ctx, sc, page, in.Audience)
	if err != nil {
		return "", err
	}
	if n, err := s.store.AudienceSize(ctx, sc, page.ID, actorID, aud); err != nil {
		return "", err
	} else if n == 0 {
		return "", ErrInvalidAudience
	}
	rr := repo.ReadRequest{ID: ulid.Make().String(), PageID: page.ID, Revision: page.Revision, Statement: in.Statement,
		Audience: aud, DueAt: in.DueAt, CreatedBy: actorID, QuizID: in.QuizID}
	if err := s.store.CreateReadRequest(ctx, sc, rr); err != nil {
		return "", err
	}
	s.record(ctx, sc, audit.ReadRequestCreate, rr.ID, map[string]any{"page_id": page.ID, "revision": page.Revision, "due": !in.DueAt.IsZero(),
		"quiz_id": in.QuizID})
	return rr.ID, nil
}

// audience normaliza y valida a quién va la solicitud según quién la pide.
func (s *Service) audience(ctx context.Context, sc repo.Scope, page repo.Page, a repo.Audience) (repo.Audience, error) {
	kinds := 0
	for _, set := range []bool{a.All, len(a.Roles) > 0, len(a.Users) > 0} {
		if set {
			kinds++
		}
	}
	if kinds != 1 || len(a.Users) > maxUsers {
		return repo.Audience{}, ErrInvalidAudience
	}
	if sc.MaxRank == editor && page.MinRank > 1 {
		return repo.Audience{}, ErrInvalidAudience // ningún lector puede leerla
	}
	switch {
	case a.All && sc.MaxRank == editor:
		return repo.Audience{Roles: []string{"viewer"}}, nil
	case a.All:
		return repo.Audience{All: true}, nil
	case len(a.Roles) > 0:
		roles := slices.Compact(slices.Sorted(slices.Values(a.Roles)))
		for _, r := range roles {
			if repo.RoleRank(r) == 0 || (sc.MaxRank == editor && r != "viewer") {
				return repo.Audience{}, ErrInvalidAudience
			}
		}
		return repo.Audience{Roles: roles}, nil
	}
	members, err := s.store.Members(ctx, sc)
	if err != nil {
		return repo.Audience{}, err
	}
	role := make(map[string]string, len(members))
	for _, m := range members {
		role[m.UserID] = m.Role
	}
	users := slices.Compact(slices.Sorted(slices.Values(a.Users)))
	for _, u := range users {
		r, ok := role[u]
		if !ok || repo.RoleRank(r) < page.MinRank || (sc.MaxRank == editor && r != "viewer") {
			return repo.Audience{}, ErrInvalidAudience
		}
	}
	return repo.Audience{Users: users}, nil
}

// Candidates son las personas a las que actorID puede pedirle el acuse sobre la página (para
// elegirlas una a una): las que pueden leerla y, si pide un editor, solo los lectores. Un editor
// no tiene la lista de miembros del área: ve solo esto.
func (s *Service) Candidates(ctx context.Context, sc repo.Scope, actorID, slug string) ([]repo.Member, error) {
	if sc.MaxRank < editor {
		return nil, repo.ErrNotFound
	}
	page, err := s.store.PageBySlug(ctx, sc, slug)
	if err != nil {
		return nil, err
	}
	members, err := s.store.Members(ctx, sc)
	if err != nil {
		return nil, err
	}
	out := []repo.Member{}
	for _, m := range members {
		if m.UserID != actorID && repo.RoleRank(m.Role) >= page.MinRank && (sc.MaxRank == admin || m.Role == "viewer") {
			out = append(out, m)
		}
	}
	return out, nil
}

// manage: la solicitud la administra quien la pidió o un admin del área; para cualquier otro no
// existe (§4.14: un editor ve solo las suyas).
func (s *Service) manage(ctx context.Context, sc repo.Scope, actorID, id string) (repo.ReadRequest, error) {
	if sc.MaxRank < editor {
		return repo.ReadRequest{}, repo.ErrNotFound
	}
	rr, err := s.store.ReadRequestByID(ctx, sc, id)
	if err == nil && sc.MaxRank < admin && rr.CreatedBy != actorID {
		err = repo.ErrNotFound
	}
	return rr, err
}

// Summary es una solicitud con sus cuentas, para el listado.
type Summary struct {
	repo.ReadRequest
	Recipients, Opened, Confirmed int
}

// List son las solicitudes del área para el admin, o las propias para un editor.
func (s *Service) List(ctx context.Context, sc repo.Scope, actorID string) ([]Summary, error) {
	if sc.MaxRank < editor {
		return nil, repo.ErrNotFound
	}
	by := actorID
	if sc.MaxRank == admin {
		by = ""
	}
	list, err := s.store.ReadRequests(ctx, sc, by)
	if err != nil {
		return nil, err
	}
	out := make([]Summary, len(list))
	for i, rr := range list {
		rs, err := s.store.Recipients(ctx, sc, rr.ID)
		if err != nil {
			return nil, err
		}
		out[i] = Summary{ReadRequest: rr, Recipients: len(rs)}
		for _, r := range rs {
			if !r.FirstOpen.IsZero() {
				out[i].Opened++
			}
			if !r.AckAt.IsZero() {
				out[i].Confirmed++
			}
		}
	}
	return out, nil
}

// Status de un destinatario (§4.14). Con cuestionario, la confirmación es aprobarlo y se suman
// en curso y reprobado sin intentos.
const (
	StatusUnopened   = "unopened"
	StatusOpened     = "opened"
	StatusConfirmed  = "confirmed"
	StatusOverdue    = "overdue"
	StatusInProgress = "in_progress"
	StatusPassed     = "passed"
	StatusFailedOut  = "failed_out"
)

// Person es un destinatario con su estado y, si la solicitud lleva cuestionario, cómo le va.
type Person struct {
	repo.Recipient
	Status string
	Quiz   repo.QuizProgress
}

type Detail struct {
	repo.ReadRequest
	People []Person
}

// Get es el panel de una solicitud: el estado de cada destinatario.
func (s *Service) Get(ctx context.Context, sc repo.Scope, actorID, id string) (Detail, error) {
	rr, err := s.manage(ctx, sc, actorID, id)
	if err != nil {
		return Detail{}, err
	}
	rs, err := s.store.Recipients(ctx, sc, id)
	if err != nil {
		return Detail{}, err
	}
	progress, maxAttempts := map[string]repo.QuizProgress{}, 0
	if rr.QuizID != "" {
		if progress, maxAttempts, err = s.quizzes.Progress(ctx, sc, rr.QuizID, rr.ID); err != nil {
			return Detail{}, err
		}
	}
	d := Detail{ReadRequest: rr, People: make([]Person, len(rs))}
	now := s.now()
	for i, r := range rs {
		p := progress[r.UserID]
		st := StatusUnopened
		switch {
		case !r.AckAt.IsZero() && rr.QuizID != "":
			st = StatusPassed
		case !r.AckAt.IsZero():
			st = StatusConfirmed
		case p.InProgress:
			st = StatusInProgress
		case rr.QuizID != "" && maxAttempts > 0 && p.Used >= maxAttempts+p.Extra:
			st = StatusFailedOut
		case !rr.DueAt.IsZero() && now.After(rr.DueAt):
			st = StatusOverdue
		case !r.FirstOpen.IsZero() || p.Used > 0:
			st = StatusOpened
		}
		d.People[i] = Person{Recipient: r, Status: st, Quiz: p}
	}
	return d, nil
}

// Close cierra la solicitud: deja de pedirse y de contar aperturas.
func (s *Service) Close(ctx context.Context, sc repo.Scope, actorID, id string) error {
	if _, err := s.manage(ctx, sc, actorID, id); err != nil {
		return err
	}
	if err := s.store.CloseReadRequest(ctx, sc, id); err != nil {
		return err
	}
	s.record(ctx, sc, audit.ReadRequestClose, id, nil)
	return nil
}

// Opened es lo que pasa cuando un destinatario abre la página: se anota la apertura en cada
// solicitud abierta que tiene sobre ella y se le devuelven, para mostrarle el aviso.
func (s *Service) Opened(ctx context.Context, sc repo.Scope, userID string, page repo.Page) ([]repo.MyRead, error) {
	reads, err := s.store.MyReads(ctx, sc, userID, page.ID)
	if err != nil || len(reads) == 0 {
		return reads, err
	}
	// Después de confirmar, la solicitud ya cumplió con esa persona: volver a abrir la página (o la
	// recarga que sigue al botón) no cuenta como apertura.
	var ids []string
	for _, r := range reads {
		if r.AckAt.IsZero() {
			ids = append(ids, r.ID)
		}
	}
	if len(ids) == 0 {
		return reads, nil
	}
	return reads, s.store.RecordReadViews(ctx, userID, page.Revision, ids, s.now())
}

// Ack es "Confirmo que lo leí": vale para la revisión que la persona tenía en pantalla, que
// tiene que ser la actual (§4.14). Solo un destinatario de una solicitud abierta.
func (s *Service) Ack(ctx context.Context, sc repo.Scope, userID, id string, revision int) error {
	rr, err := s.store.ReadRequestByID(ctx, sc, id)
	if err != nil {
		return err
	}
	page, err := s.store.PageBySlug(ctx, sc, rr.PageSlug)
	if err != nil {
		return err
	}
	reads, err := s.store.MyReads(ctx, sc, userID, page.ID)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(reads, func(r repo.MyRead) bool { return r.ID == id })
	if i < 0 {
		return repo.ErrNotFound
	}
	if rr.QuizID != "" {
		return ErrQuizRequired // solo un destinatario se entera de que lleva cuestionario
	}
	if revision != page.Revision {
		return ErrPageChanged
	}
	if !reads[i].AckAt.IsZero() {
		return nil
	}
	if err := s.store.Acknowledge(ctx, id, userID, revision); err != nil {
		return err
	}
	s.record(ctx, sc, audit.ReadAck, id, map[string]any{"page_id": page.ID, "revision": revision})
	return nil
}
