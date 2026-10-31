package quiz

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/athomo001/kudawiki/internal/wiki"
	"github.com/oklog/ulid/v2"
)

const (
	editor, admin = 2, 3
	maxReason     = 500
	maxExtra      = 10
)

type store interface {
	WorkspaceByID(ctx context.Context, id string) (repo.Workspace, error)
	PageBySlug(ctx context.Context, s repo.Scope, slug string) (repo.Page, error)
	Members(ctx context.Context, s repo.Scope) ([]repo.Member, error)
	UserByID(ctx context.Context, id string) (repo.User, error)
	CreateQuiz(ctx context.Context, s repo.Scope, q repo.Quiz) error
	QuizByID(ctx context.Context, s repo.Scope, id string) (repo.Quiz, error)
	PageQuizzes(ctx context.Context, s repo.Scope, pageID string) ([]repo.Quiz, error)
	UpdateQuiz(ctx context.Context, s repo.Scope, q repo.Quiz) error
	SetQuizStatus(ctx context.Context, s repo.Scope, id, status string) error
	DeleteQuiz(ctx context.Context, s repo.Scope, id string) error
	PublishQuiz(ctx context.Context, s repo.Scope, id, by string, v repo.QuizVersion) (int, error)
	SavePreviewVersion(ctx context.Context, s repo.Scope, id, by string, v repo.QuizVersion) error
	QuizVersion(ctx context.Context, s repo.Scope, quizID string, version int) (repo.QuizVersion, error)
	InsertAttempt(ctx context.Context, s repo.Scope, a repo.QuizAttempt) error
	AttemptByID(ctx context.Context, s repo.Scope, id string) (repo.QuizAttempt, error)
	OpenAttempt(ctx context.Context, s repo.Scope, quizID, userID string) (repo.QuizAttempt, error)
	DiscardPreview(ctx context.Context, s repo.Scope, quizID, userID string) error
	SaveAnswers(ctx context.Context, s repo.Scope, id, answers string) error
	FinishAttempt(ctx context.Context, s repo.Scope, o repo.AttemptOutcome) error
	AttemptStats(ctx context.Context, s repo.Scope, quizID, userID, requestID string) (repo.AttemptStats, error)
	UserAttempts(ctx context.Context, s repo.Scope, quizID, userID string) ([]repo.QuizAttempt, error)
	QuizAttempts(ctx context.Context, s repo.Scope, quizID string) ([]repo.QuizAttempt, error)
	ExpiredAttempts(ctx context.Context, s repo.Scope, quizID string, now time.Time) ([]repo.QuizAttempt, error)
	GrantExtraAttempts(ctx context.Context, s repo.Scope, quizID, userID, requestID string, extra int, reason, by string, n repo.NewNotification) error
	OpenQuizRequest(ctx context.Context, s repo.Scope, quizID, userID string) (repo.ReadRequest, error)
	AckedQuizRequest(ctx context.Context, s repo.Scope, quizID, userID string) (repo.ReadRequest, error)
	ReadRequestByID(ctx context.Context, s repo.Scope, id string) (repo.ReadRequest, error)
	AssignedQuiz(ctx context.Context, s repo.Scope, quizID, userID string) (bool, error)
	RequestQuizProgress(ctx context.Context, s repo.Scope, requestID string) (map[string]repo.QuizProgress, error)
}

type Service struct {
	store store
	audit *audit.Recorder
	now   func() time.Time
	rand  *rand.Rand
}

func NewService(s store, rec *audit.Recorder, now func() time.Time) *Service {
	return &Service{store: s, audit: rec, now: now, rand: rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))}
}

func (s *Service) record(ctx context.Context, sc repo.Scope, a audit.Action, id string, meta map[string]any) {
	s.audit.Record(ctx, audit.Event{Action: a, WsID: sc.WsID, TargetType: "quiz", TargetID: id, Meta: meta})
}

// area es el área del Scope si tiene los cuestionarios encendidos; si no, para quien pregunta
// no existen (§4.16.7). En un espacio personal no hay cuestionarios (§4.16.1).
func (s *Service) area(ctx context.Context, sc repo.Scope) (repo.Workspace, error) {
	w, err := s.store.WorkspaceByID(ctx, sc.WsID)
	if err == nil && (w.Kind != "area" || !w.QuizzesEnabled) {
		err = repo.ErrNotFound
	}
	return w, err
}

// quiz es el cuestionario si el área los tiene y el Scope ve su página.
func (s *Service) quiz(ctx context.Context, sc repo.Scope, id string) (repo.Workspace, repo.Quiz, error) {
	w, err := s.area(ctx, sc)
	if err != nil {
		return w, repo.Quiz{}, err
	}
	q, err := s.store.QuizByID(ctx, sc, id)
	return w, q, err
}

// manages: lo administra quien lo creó, el dueño de la página o un admin del área (§4.16.1).
func manages(sc repo.Scope, actorID string, q repo.Quiz) bool {
	return sc.MaxRank == admin || sc.MaxRank >= editor && (q.CreatedBy == actorID || q.PageOwnerID == actorID)
}

// seesResults: además de quien lo administra, quien lo asignó (§4.16.5).
func (s *Service) seesResults(ctx context.Context, sc repo.Scope, actorID string, q repo.Quiz) (bool, error) {
	if manages(sc, actorID, q) {
		return true, nil
	}
	if sc.MaxRank < editor {
		return false, nil
	}
	return s.store.AssignedQuiz(ctx, sc, q.ID, actorID)
}

func parseDraft(raw string) (Draft, error) {
	var d Draft
	err := json.Unmarshal([]byte(raw), &d)
	return d, err
}

func (s *Service) version(ctx context.Context, sc repo.Scope, quizID string, n int) (Settings, []Question, repo.QuizVersion, error) {
	v, err := s.store.QuizVersion(ctx, sc, quizID, n)
	if err != nil {
		return Settings{}, nil, v, err
	}
	var st Settings
	var qs []Question
	if err := json.Unmarshal([]byte(v.Settings), &st); err != nil {
		return st, nil, v, err
	}
	return st, qs, v, json.Unmarshal([]byte(v.Questions), &qs)
}

// Create empieza un cuestionario sobre una página que quien lo crea puede editar (§4.16.1).
func (s *Service) Create(ctx context.Context, sc repo.Scope, actorID, slug, title string) (string, error) {
	if _, err := s.area(ctx, sc); err != nil {
		return "", err
	}
	if sc.MaxRank < editor {
		return "", repo.ErrNotFound
	}
	page, err := s.store.PageBySlug(ctx, sc, slug)
	if err != nil {
		return "", err
	}
	title = strings.TrimSpace(title)
	if title == "" || utf8.RuneCountInString(title) > maxTitle {
		return "", ErrInvalidQuiz
	}
	draft, _ := json.Marshal(Draft{Settings: Defaults(), Questions: []Question{}})
	q := repo.Quiz{ID: ulid.Make().String(), PageID: page.ID, Title: title, Draft: string(draft), CreatedBy: actorID}
	if err := s.store.CreateQuiz(ctx, sc, q); err != nil {
		return "", err
	}
	s.record(ctx, sc, audit.QuizCreate, q.ID, map[string]any{"page_id": page.ID})
	return q.ID, nil
}

// Summary es lo que se ve antes de empezar (§4.16.4): "10 preguntas · ~8 min · 2 intentos ·
// aprueba con 70 %", y lo que le queda a quien lo va a hacer.
type Summary struct {
	Questions    int        `json:"questions"`
	EstimatedMin int        `json:"estimated_min"`
	MaxAttempts  int        `json:"max_attempts"`
	PassPercent  int        `json:"pass_percent"`
	TimeLimitS   int        `json:"time_limit_s"`
	Remaining    int        `json:"remaining"` // -1 = sin límite
	NextAt       *time.Time `json:"next_at"`   // la espera entre intentos
	DueAt        *time.Time `json:"due_at"`    // el plazo de la solicitud
	RequestID    string     `json:"request_id"`
	Passed       bool       `json:"passed"`
	InProgress   string     `json:"in_progress"` // el intento abierto, para retomarlo
}

// Listed es un cuestionario en la página: con Manage, quien lo administra; si no, uno que la
// persona puede hacer, con su resumen.
type Listed struct {
	ID, Title, Status string
	Manage            bool
	Summary           *Summary
}

// List son los cuestionarios de la página que la persona ve: todos si los administra; si no, los
// publicados que tiene asignados o que están abiertos a los lectores.
func (s *Service) List(ctx context.Context, sc repo.Scope, actorID, slug string) ([]Listed, error) {
	if _, err := s.area(ctx, sc); err != nil {
		return []Listed{}, nil // apagados: la página no tiene cuestionarios
	}
	page, err := s.store.PageBySlug(ctx, sc, slug)
	if err != nil {
		return nil, err
	}
	qs, err := s.store.PageQuizzes(ctx, sc, page.ID)
	if err != nil {
		return nil, err
	}
	out := []Listed{}
	for _, q := range qs {
		l := Listed{ID: q.ID, Title: q.Title, Status: q.Status, Manage: manages(sc, actorID, q)}
		if q.Status == "published" {
			sum, err := s.summary(ctx, sc, actorID, q)
			switch {
			case err == nil:
				l.Summary = &sum
			case !errors.Is(err, repo.ErrNotFound):
				return nil, err
			case !l.Manage:
				continue
			}
		} else if !l.Manage {
			continue
		}
		out = append(out, l)
	}
	return out, nil
}

// takeContext es por qué la persona hace el cuestionario: la solicitud abierta que tiene, o el
// cuestionario abierto a los lectores (rr vacía). Si ninguna, la última solicitud que ya cumplió
// (done): ve su resultado pero no empieza otro intento. Sin nada de eso, para ella no existe.
func (s *Service) takeContext(ctx context.Context, sc repo.Scope, actorID string, q repo.Quiz) (rr repo.ReadRequest, done bool, err error) {
	rr, err = s.store.OpenQuizRequest(ctx, sc, q.ID, actorID)
	if !errors.Is(err, repo.ErrNotFound) {
		return rr, false, err
	}
	if q.OpenToReaders {
		return repo.ReadRequest{}, false, nil
	}
	rr, err = s.store.AckedQuizRequest(ctx, sc, q.ID, actorID)
	return rr, err == nil, err
}

func (s *Service) summary(ctx context.Context, sc repo.Scope, actorID string, q repo.Quiz) (Summary, error) {
	rr, done, err := s.takeContext(ctx, sc, actorID, q)
	if err != nil {
		return Summary{}, err
	}
	st, qs, _, err := s.version(ctx, sc, q.ID, q.PublishedVersion)
	if err != nil {
		return Summary{}, err
	}
	stats, err := s.store.AttemptStats(ctx, sc, q.ID, actorID, rr.ID)
	if err != nil {
		return Summary{}, err
	}
	sum := Summary{Questions: QuestionCount(st, qs), EstimatedMin: EstimatedMinutes(st, qs), MaxAttempts: st.MaxAttempts,
		PassPercent: st.PassPercent, TimeLimitS: st.TimeLimitS, Remaining: remaining(st, stats), RequestID: rr.ID, Passed: stats.Passed}
	if done {
		sum.Remaining = 0
	}
	if next := nextAt(st, stats); next.After(s.now()) {
		sum.NextAt = &next
	}
	if !rr.DueAt.IsZero() {
		sum.DueAt = &rr.DueAt
	}
	if a, err := s.store.OpenAttempt(ctx, sc, q.ID, actorID); err == nil && !a.Preview {
		sum.InProgress = a.ID
	}
	return sum, nil
}

func remaining(st Settings, stats repo.AttemptStats) int {
	if st.MaxAttempts == 0 {
		return -1
	}
	return max(0, st.MaxAttempts+stats.Extra-stats.Used)
}

func nextAt(st Settings, stats repo.AttemptStats) time.Time {
	if st.CooldownMin == 0 || stats.LastFinished.IsZero() {
		return time.Time{}
	}
	return stats.LastFinished.Add(time.Duration(st.CooldownMin) * time.Minute)
}

// View es el cuestionario para quien lo abre. Draft, Published y PageChanged son solo para quien
// lo administra: el borrador trae las respuestas correctas.
type View struct {
	repo.Quiz
	Manage, Results bool
	Draft           *Draft
	Dirty           bool // el borrador tiene cambios sin publicar
	PageChanged     bool // la página cambió desde la última publicación (§4.16.6)
	Summary         *Summary
}

func (s *Service) Get(ctx context.Context, sc repo.Scope, actorID, id string) (View, error) {
	_, q, err := s.quiz(ctx, sc, id)
	if err != nil {
		return View{}, err
	}
	v := View{Quiz: q, Manage: manages(sc, actorID, q)}
	if v.Results, err = s.seesResults(ctx, sc, actorID, q); err != nil {
		return View{}, err
	}
	if q.Status == "published" {
		sum, err := s.summary(ctx, sc, actorID, q)
		if err == nil {
			v.Summary = &sum
		} else if !errors.Is(err, repo.ErrNotFound) {
			return View{}, err
		}
	}
	if !v.Manage {
		if v.Summary == nil && !v.Results {
			return View{}, repo.ErrNotFound
		}
		return v, nil
	}
	d, err := parseDraft(q.Draft)
	if err != nil {
		return View{}, err
	}
	v.Draft = &d
	v.Dirty = q.PublishedVersion == 0
	if q.PublishedVersion > 0 {
		pv, err := s.store.QuizVersion(ctx, sc, q.ID, q.PublishedVersion)
		if err != nil {
			return View{}, err
		}
		st, _ := json.Marshal(d.Settings)
		qs, _ := json.Marshal(d.Questions)
		v.Dirty = string(st) != pv.Settings || string(qs) != pv.Questions
		v.PageChanged = q.PageRevision > pv.PageRevision
	}
	return v, nil
}

// manage es el cuestionario si actorID lo administra; si no, no existe.
func (s *Service) manage(ctx context.Context, sc repo.Scope, actorID, id string) (repo.Workspace, repo.Quiz, error) {
	w, q, err := s.quiz(ctx, sc, id)
	if err == nil && !manages(sc, actorID, q) {
		err = repo.ErrNotFound
	}
	return w, q, err
}

// Patch es lo que cambia en el borrador; nil = queda como estaba.
type Patch struct {
	Title         *string
	OpenToReaders *bool
	Draft         *Draft
}

func (s *Service) Update(ctx context.Context, sc repo.Scope, actorID, id string, p Patch) (Draft, error) {
	_, q, err := s.manage(ctx, sc, actorID, id)
	if err != nil {
		return Draft{}, err
	}
	d, err := parseDraft(q.Draft)
	if err != nil {
		return Draft{}, err
	}
	if p.Title != nil {
		q.Title = strings.TrimSpace(*p.Title)
		if q.Title == "" || utf8.RuneCountInString(q.Title) > maxTitle {
			return Draft{}, ErrInvalidQuiz
		}
	}
	if p.OpenToReaders != nil {
		q.OpenToReaders = *p.OpenToReaders
	}
	if p.Draft != nil {
		d = *p.Draft
		if err := d.normalize(false); err != nil {
			return Draft{}, err
		}
		raw, _ := json.Marshal(d)
		q.Draft = string(raw)
	}
	if err := s.store.UpdateQuiz(ctx, sc, q); err != nil {
		return Draft{}, err
	}
	s.record(ctx, sc, audit.QuizUpdate, q.ID, map[string]any{"questions": len(d.Questions), "open_to_readers": q.OpenToReaders})
	return d, nil
}

// frozen es el borrador listo para que alguien lo responda: publicado o en una prueba.
func frozen(q repo.Quiz) (repo.QuizVersion, error) {
	d, err := parseDraft(q.Draft)
	if err != nil {
		return repo.QuizVersion{}, err
	}
	if err := d.normalize(true); err != nil {
		return repo.QuizVersion{}, err
	}
	st, _ := json.Marshal(d.Settings)
	qs, _ := json.Marshal(d.Questions)
	return repo.QuizVersion{PageRevision: q.PageRevision, Settings: string(st), Questions: string(qs)}, nil
}

// Publish congela el borrador en una versión nueva: los intentos que vienen la responden; los
// anteriores conservan la suya y su puntaje (§4.16.6). Publicar uno archivado lo vuelve a abrir.
func (s *Service) Publish(ctx context.Context, sc repo.Scope, actorID, id string) (int, error) {
	_, q, err := s.manage(ctx, sc, actorID, id)
	if err != nil {
		return 0, err
	}
	v, err := frozen(q)
	if err != nil {
		return 0, err
	}
	n, err := s.store.PublishQuiz(ctx, sc, q.ID, actorID, v)
	if err != nil {
		return 0, err
	}
	s.record(ctx, sc, audit.QuizPublish, q.ID, map[string]any{"version": n, "page_revision": q.PageRevision})
	return n, nil
}

// Archive lo saca de circulación: nadie más lo empieza ni se asigna; los resultados quedan.
func (s *Service) Archive(ctx context.Context, sc repo.Scope, actorID, id string) error {
	_, q, err := s.manage(ctx, sc, actorID, id)
	if err != nil {
		return err
	}
	if q.Status != "published" {
		return ErrInvalidQuiz
	}
	if err := s.store.SetQuizStatus(ctx, sc, q.ID, "archived"); err != nil {
		return err
	}
	s.record(ctx, sc, audit.QuizArchive, q.ID, nil)
	return nil
}

// Delete borra un cuestionario que nunca se publicó; uno publicado tiene resultados y se archiva.
func (s *Service) Delete(ctx context.Context, sc repo.Scope, actorID, id string) error {
	_, q, err := s.manage(ctx, sc, actorID, id)
	if err != nil {
		return err
	}
	if q.PublishedVersion > 0 {
		return ErrQuizPublished
	}
	if err := s.store.DeleteQuiz(ctx, sc, q.ID); err != nil {
		return err
	}
	s.record(ctx, sc, audit.QuizDelete, q.ID, map[string]any{"page_id": q.PageID})
	return nil
}

// OptionView es una alternativa como la ve quien responde: sin decir si es la correcta.
type OptionView struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// QuestionView es una pregunta del intento. Lo que sigue a Options solo se llena al corregir, y
// según lo que el cuestionario deja ver (§4.16.3).
type QuestionView struct {
	ID             string       `json:"id"`
	Kind           string       `json:"kind"`
	PromptHTML     string       `json:"prompt_html"`
	Points         int          `json:"points"`
	Options        []OptionView `json:"options"`
	Correct        *bool        `json:"correct,omitempty"`
	Earned         *float64     `json:"earned,omitempty"`
	Anchor         string       `json:"anchor,omitempty"`
	CorrectOptions []string     `json:"correct_options,omitempty"`
	Explanation    string       `json:"explanation_html,omitempty"`
}

// Result es la corrección de un intento.
type Result struct {
	Score     float64   `json:"score"`
	Max       float64   `json:"max"`
	Percent   float64   `json:"percent"`
	Passed    bool      `json:"passed"`
	TimedOut  bool      `json:"timed_out"`
	Feedback  string    `json:"feedback"`
	Revealed  bool      `json:"revealed"`
	Submitted time.Time `json:"submitted_at"`
	DurationS int       `json:"duration_s"`
}

// AttemptView es un intento como lo ve quien lo hace: en curso, las preguntas sin respuestas
// correctas; cerrado, además su corrección.
type AttemptView struct {
	ID          string              `json:"id"`
	QuizID      string              `json:"quiz_id"`
	Number      int                 `json:"number"`
	Preview     bool                `json:"preview"`
	Status      string              `json:"status"`
	StartedAt   time.Time           `json:"started_at"`
	DeadlineAt  *time.Time          `json:"deadline_at"`
	PassPercent int                 `json:"pass_percent"`
	Questions   []QuestionView      `json:"questions"`
	Answers     map[string][]string `json:"answers"`
	Result      *Result             `json:"result,omitempty"`
}

// renderer convierte el Markdown de las preguntas como el de la página: mismas reglas, enlaces
// que solo valen si quien lee ve la página de destino (§17).
func (s *Service) renderer(ctx context.Context, sc repo.Scope, w repo.Workspace) func(string) string {
	opts := wiki.Options{Base: "/w/" + w.Slug, Exists: func(slug string) bool {
		_, err := s.store.PageBySlug(ctx, sc, slug)
		return err == nil
	}}
	return func(md string) string {
		if md == "" {
			return ""
		}
		html, _, err := wiki.Render(md, opts)
		if err != nil {
			return ""
		}
		return html
	}
}

// view arma lo que ve quien hizo el intento. reveal: se pueden mostrar las respuestas correctas.
func (s *Service) view(a repo.QuizAttempt, st Settings, qs []Question, render func(string) string, reveal bool) (AttemptView, error) {
	var lay []slot
	if err := json.Unmarshal([]byte(a.Layout), &lay); err != nil {
		return AttemptView{}, err
	}
	answers := map[string][]string{}
	if err := json.Unmarshal([]byte(a.Answers), &answers); err != nil {
		return AttemptView{}, err
	}
	byID := make(map[string]Question, len(qs))
	for _, q := range qs {
		byID[q.ID] = q
	}
	v := AttemptView{ID: a.ID, QuizID: a.QuizID, Number: a.Number, Preview: a.Preview, Status: a.Status, StartedAt: a.StartedAt,
		PassPercent: st.PassPercent, Answers: answers, Questions: make([]QuestionView, 0, len(lay))}
	if !a.DeadlineAt.IsZero() {
		v.DeadlineAt = &a.DeadlineAt
	}
	done := a.Status != "in_progress"
	feedback := st.Feedback
	if a.Preview {
		feedback, reveal = "all", true // quien lo prueba ve todo: para eso lo prueba
	}
	if done {
		end := a.SubmittedAt
		v.Result = &Result{Score: a.Score, Max: a.Max, Percent: a.Percent, Passed: a.Passed, TimedOut: a.Status == "timed_out",
			Feedback: feedback, Revealed: reveal && feedback == "all", Submitted: end, DurationS: int(end.Sub(a.StartedAt).Seconds())}
		if feedback == "score" {
			v.Questions = []QuestionView{}
			return v, nil
		}
	}
	for _, sl := range lay {
		q := byID[sl.Q]
		qv := QuestionView{ID: q.ID, Kind: q.Kind, PromptHTML: render(q.Prompt), Points: q.Points, Options: make([]OptionView, 0, len(sl.O))}
		texts := make(map[string]Option, len(q.Options))
		for _, o := range q.Options {
			texts[o.ID] = o
		}
		for _, id := range sl.O {
			qv.Options = append(qv.Options, OptionView{ID: id, Text: texts[id].Text})
		}
		if done {
			earned, ok := grade(q, answers[q.ID], st.MultipleScoring)
			qv.Correct, qv.Earned = &ok, &earned
			if !ok {
				qv.Anchor = q.Anchor
			}
			if v.Result.Revealed {
				for _, id := range sl.O {
					if texts[id].Correct {
						qv.CorrectOptions = append(qv.CorrectOptions, id)
					}
				}
				qv.Explanation = render(q.Explanation)
			}
		}
		v.Questions = append(v.Questions, qv)
	}
	return v, nil
}

// revealed: las respuestas correctas se muestran según el cuestionario: nunca, siempre, o al
// aprobar o quedarse sin intentos (§4.16.3). Mostrarlas antes regalaría el intento siguiente.
func revealed(st Settings, stats repo.AttemptStats) bool {
	switch st.Reveal {
	case "always":
		return true
	case "after_final":
		return stats.Passed || remaining(st, stats) == 0
	}
	return false
}

// Start empieza un intento, o devuelve el que la persona tiene abierto (un solo intento abierto,
// §4.16.4). Los intentos, la espera y el tiempo los controla el servidor (§4.16.7).
func (s *Service) Start(ctx context.Context, sc repo.Scope, actorID, id string) (AttemptView, error) {
	w, q, err := s.quiz(ctx, sc, id)
	if err != nil {
		return AttemptView{}, err
	}
	if q.Status != "published" {
		return AttemptView{}, repo.ErrNotFound
	}
	rr, done, err := s.takeContext(ctx, sc, actorID, q)
	if err != nil {
		return AttemptView{}, err
	}
	if done {
		return AttemptView{}, ErrNoAttempts
	}
	if err := s.expire(ctx, sc, q); err != nil {
		return AttemptView{}, err
	}
	if open, err := s.store.OpenAttempt(ctx, sc, q.ID, actorID); err == nil {
		if !open.Preview {
			return s.current(ctx, sc, w, open)
		}
		if err := s.store.DiscardPreview(ctx, sc, q.ID, actorID); err != nil {
			return AttemptView{}, err
		}
	} else if !errors.Is(err, repo.ErrNotFound) {
		return AttemptView{}, err
	}
	st, qs, _, err := s.version(ctx, sc, q.ID, q.PublishedVersion)
	if err != nil {
		return AttemptView{}, err
	}
	stats, err := s.store.AttemptStats(ctx, sc, q.ID, actorID, rr.ID)
	if err != nil {
		return AttemptView{}, err
	}
	if remaining(st, stats) == 0 {
		return AttemptView{}, ErrNoAttempts
	}
	if nextAt(st, stats).After(s.now()) {
		return AttemptView{}, ErrCooldown
	}
	a, err := s.insert(ctx, sc, q, actorID, rr.ID, q.PublishedVersion, stats.Used+1, false, st, qs)
	if errors.Is(err, repo.ErrConflict) {
		// Dos "empezar" a la vez: gana el primero y los dos ven ese intento.
		if open, err := s.store.OpenAttempt(ctx, sc, q.ID, actorID); err == nil {
			return s.current(ctx, sc, w, open)
		}
	}
	if err != nil {
		return AttemptView{}, err
	}
	s.record(ctx, sc, audit.QuizAttemptStart, q.ID, map[string]any{"attempt_id": a.ID, "version": a.Version, "request_id": rr.ID})
	return s.view(a, st, qs, s.renderer(ctx, sc, w), false)
}

func (s *Service) insert(ctx context.Context, sc repo.Scope, q repo.Quiz, userID, requestID string, version, number int, preview bool,
	st Settings, qs []Question) (repo.QuizAttempt, error) {
	lay, _ := json.Marshal(layout(st, qs, s.rand))
	now := s.now().UTC().Truncate(time.Second)
	a := repo.QuizAttempt{ID: ulid.Make().String(), QuizID: q.ID, UserID: userID, RequestID: requestID, Version: version, Number: number,
		Preview: preview, Layout: string(lay), Answers: "{}", Status: "in_progress", StartedAt: now}
	if st.TimeLimitS > 0 {
		a.DeadlineAt = now.Add(time.Duration(st.TimeLimitS) * time.Second)
	}
	return a, s.store.InsertAttempt(ctx, sc, a)
}

// current es el intento abierto tal como se dejó, con sus respuestas guardadas.
func (s *Service) current(ctx context.Context, sc repo.Scope, w repo.Workspace, a repo.QuizAttempt) (AttemptView, error) {
	st, qs, _, err := s.version(ctx, sc, a.QuizID, a.Version)
	if err != nil {
		return AttemptView{}, err
	}
	return s.view(a, st, qs, s.renderer(ctx, sc, w), false)
}

// Preview es un intento de prueba sobre el borrador: no cuenta ni aparece en los resultados
// (§4.16.6). Una prueba anterior sin terminar se descarta.
func (s *Service) Preview(ctx context.Context, sc repo.Scope, actorID, id string) (AttemptView, error) {
	w, q, err := s.manage(ctx, sc, actorID, id)
	if err != nil {
		return AttemptView{}, err
	}
	v, err := frozen(q)
	if err != nil {
		return AttemptView{}, err
	}
	if err := s.store.SavePreviewVersion(ctx, sc, q.ID, actorID, v); err != nil {
		return AttemptView{}, err
	}
	if err := s.store.DiscardPreview(ctx, sc, q.ID, actorID); err != nil {
		return AttemptView{}, err
	}
	st, qs, _, err := s.version(ctx, sc, q.ID, 0)
	if err != nil {
		return AttemptView{}, err
	}
	a, err := s.insert(ctx, sc, q, actorID, "", 0, 0, true, st, qs)
	if err != nil {
		return AttemptView{}, err // ErrConflict: tiene un intento de verdad abierto
	}
	return s.view(a, st, qs, s.renderer(ctx, sc, w), true)
}

// own es el intento de actorID, si sigue viendo el cuestionario.
func (s *Service) own(ctx context.Context, sc repo.Scope, actorID, attemptID string) (repo.Workspace, repo.Quiz, repo.QuizAttempt, error) {
	a, err := s.store.AttemptByID(ctx, sc, attemptID)
	if err != nil {
		return repo.Workspace{}, repo.Quiz{}, a, err
	}
	if a.UserID != actorID {
		return repo.Workspace{}, repo.Quiz{}, a, repo.ErrNotFound
	}
	w, q, err := s.quiz(ctx, sc, a.QuizID)
	return w, q, a, err
}

// Save guarda lo respondido hasta ahora (§4.16.4). Pasado el tiempo, el intento se corrige con
// lo que ya estaba guardado y lo nuevo no entra.
func (s *Service) Save(ctx context.Context, sc repo.Scope, actorID, attemptID string, answers map[string][]string) error {
	_, q, a, err := s.own(ctx, sc, actorID, attemptID)
	if err != nil {
		return err
	}
	if a.Status != "in_progress" {
		return ErrAttemptClosed
	}
	if !a.DeadlineAt.IsZero() && !s.now().Before(a.DeadlineAt) {
		if _, err := s.finish(ctx, sc, q, a, nil); err != nil {
			return err
		}
		return ErrAttemptClosed
	}
	raw, err := checkAnswers(a, answers)
	if err != nil {
		return err
	}
	if err := s.store.SaveAnswers(ctx, sc, a.ID, raw); errors.Is(err, repo.ErrNotFound) {
		return ErrAttemptClosed
	} else if err != nil {
		return err
	}
	return nil
}

func checkAnswers(a repo.QuizAttempt, answers map[string][]string) (string, error) {
	var lay []slot
	if err := json.Unmarshal([]byte(a.Layout), &lay); err != nil {
		return "", err
	}
	if answers == nil {
		answers = map[string][]string{}
	}
	if !valid(lay, answers) {
		return "", ErrInvalidQuiz
	}
	raw, _ := json.Marshal(answers)
	return string(raw), nil
}

// Submit envía el intento y lo corrige en el servidor. Si se le acabó el tiempo, se corrige con
// lo guardado hasta el límite: enviar tarde no cambia el puntaje (§17).
func (s *Service) Submit(ctx context.Context, sc repo.Scope, actorID, attemptID string, answers map[string][]string) (AttemptView, error) {
	w, q, a, err := s.own(ctx, sc, actorID, attemptID)
	if err != nil {
		return AttemptView{}, err
	}
	if a.Status != "in_progress" {
		return AttemptView{}, ErrAttemptClosed
	}
	var given *string
	if a.DeadlineAt.IsZero() || s.now().Before(a.DeadlineAt) {
		raw, err := checkAnswers(a, answers)
		if err != nil {
			return AttemptView{}, err
		}
		given = &raw
	}
	done, err := s.finish(ctx, sc, q, a, given)
	if err != nil {
		return AttemptView{}, err
	}
	return s.closed(ctx, sc, w, q, done)
}

// closed es la vista de un intento terminado, con las respuestas a la vista si corresponde.
func (s *Service) closed(ctx context.Context, sc repo.Scope, w repo.Workspace, q repo.Quiz, a repo.QuizAttempt) (AttemptView, error) {
	st, qs, _, err := s.version(ctx, sc, q.ID, a.Version)
	if err != nil {
		return AttemptView{}, err
	}
	stats, err := s.store.AttemptStats(ctx, sc, q.ID, a.UserID, a.RequestID)
	if err != nil {
		return AttemptView{}, err
	}
	return s.view(a, st, qs, s.renderer(ctx, sc, w), revealed(st, stats))
}

// finish corrige y cierra el intento. given nil = se acabó el tiempo y vale lo guardado. Si
// aprueba, la solicitud por la que lo hizo queda confirmada con la revisión de hoy de la página
// (§4.14); si se queda sin intentos sin aprobar, se le avisa a quien la pidió.
func (s *Service) finish(ctx context.Context, sc repo.Scope, q repo.Quiz, a repo.QuizAttempt, given *string) (repo.QuizAttempt, error) {
	st, qs, _, err := s.version(ctx, sc, q.ID, a.Version)
	if err != nil {
		return a, err
	}
	status := "submitted"
	if given != nil {
		a.Answers = *given
	} else {
		status = "timed_out"
	}
	answers := map[string][]string{}
	if err := json.Unmarshal([]byte(a.Answers), &answers); err != nil {
		return a, err
	}
	var lay []slot
	if err := json.Unmarshal([]byte(a.Layout), &lay); err != nil {
		return a, err
	}
	byID := make(map[string]Question, len(qs))
	for _, x := range qs {
		byID[x.ID] = x
	}
	a.Score, a.Max = 0, 0
	for _, sl := range lay {
		x := byID[sl.Q]
		earned, _ := grade(x, answers[x.ID], st.MultipleScoring)
		a.Score += earned
		a.Max += float64(x.Points)
	}
	if a.Max > 0 {
		a.Percent = float64(int(a.Score/a.Max*1000+0.5)) / 10
	}
	a.Passed = a.Percent >= float64(st.PassPercent)
	a.Status = status
	a.SubmittedAt = s.now().UTC().Truncate(time.Second)
	if status == "timed_out" && !a.DeadlineAt.IsZero() {
		a.SubmittedAt = a.DeadlineAt
	}
	out := repo.AttemptOutcome{Attempt: a}
	if !a.Preview && a.RequestID != "" {
		if a.Passed {
			out.AckRevision = q.PageRevision
		} else if n, err := s.failedOut(ctx, sc, q, a, st); err != nil {
			return a, err
		} else if n != nil {
			out.Notifications = []repo.NewNotification{*n}
		}
	}
	if err := s.store.FinishAttempt(ctx, sc, out); errors.Is(err, repo.ErrConflict) {
		return s.store.AttemptByID(ctx, sc, a.ID) // otro envío lo cerró primero: vale ese
	} else if err != nil {
		return a, err
	}
	if !a.Preview {
		action := audit.QuizAttemptSubmit
		if status == "timed_out" {
			action = audit.QuizAttemptTimeout
		}
		s.record(ctx, sc, action, q.ID, map[string]any{"attempt_id": a.ID, "version": a.Version, "percent": a.Percent, "passed": a.Passed})
	}
	return a, nil
}

// failedOut es el aviso a quien pidió la lectura cuando este intento fallido era el último.
func (s *Service) failedOut(ctx context.Context, sc repo.Scope, q repo.Quiz, a repo.QuizAttempt, st Settings) (*repo.NewNotification, error) {
	stats, err := s.store.AttemptStats(ctx, sc, q.ID, a.UserID, a.RequestID)
	if err != nil {
		return nil, err
	}
	// Este intento todavía figura abierto: ya está contado en Used.
	if st.MaxAttempts == 0 || stats.Passed || stats.Used < st.MaxAttempts+stats.Extra {
		return nil, nil
	}
	rr, err := s.store.ReadRequestByID(ctx, sc, a.RequestID)
	if err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(map[string]string{"title": q.Title, "actor": a.Name})
	return &repo.NewNotification{UserID: rr.CreatedBy, Kind: "quiz_failed_out", Category: "quiz_failed", PageID: q.PageID,
		MinRank: q.PageRank, GroupKey: "quiz_failed:" + a.RequestID, Payload: string(payload)}, nil
}

// expire corrige los intentos del cuestionario a los que se les acabó el tiempo (§10.3: al
// siguiente acceso).
func (s *Service) expire(ctx context.Context, sc repo.Scope, q repo.Quiz) error {
	list, err := s.store.ExpiredAttempts(ctx, sc, q.ID, s.now())
	if err != nil {
		return err
	}
	for _, a := range list {
		if _, err := s.finish(ctx, sc, q, a, nil); err != nil {
			return err
		}
	}
	return nil
}

// MyAttempts es el historial propio (§4.16.5): cada intento con su corrección, según lo que el
// cuestionario deja ver.
func (s *Service) MyAttempts(ctx context.Context, sc repo.Scope, actorID, id string) ([]AttemptView, error) {
	w, q, err := s.quiz(ctx, sc, id)
	if err != nil {
		return nil, err
	}
	if err := s.expire(ctx, sc, q); err != nil {
		return nil, err
	}
	list, err := s.store.UserAttempts(ctx, sc, q.ID, actorID)
	if err != nil {
		return nil, err
	}
	out := []AttemptView{}
	render := s.renderer(ctx, sc, w)
	stats := map[string]repo.AttemptStats{}
	for _, a := range list {
		if a.Status == "in_progress" {
			continue
		}
		st, qs, _, err := s.version(ctx, sc, q.ID, a.Version)
		if err != nil {
			return nil, err
		}
		x, ok := stats[a.RequestID]
		if !ok {
			if x, err = s.store.AttemptStats(ctx, sc, q.ID, actorID, a.RequestID); err != nil {
				return nil, err
			}
			stats[a.RequestID] = x
		}
		v, err := s.view(a, st, qs, render, revealed(st, x))
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// PersonResult es una persona en los resultados (§4.16.5).
type PersonResult struct {
	UserID, Name, Email string
	Attempts            int
	Percent             float64 // el intento que cuenta según la configuración
	Passed, InProgress  bool
	LastAt              time.Time
	LastDurationS       int
}

// QuestionStat es una pregunta en los resultados: cuántos la respondieron, cuántos acertaron y
// la alternativa incorrecta más elegida. Una que falla casi todo el mundo suele decir que el
// procedimiento está mal explicado (§4.16.5).
type QuestionStat struct {
	ID, Prompt, Kind  string
	Answered, Correct int
	TopWrong          string
	TopWrongCount     int
	Version           int // la última versión en la que aparece
}

type Results struct {
	People    []PersonResult
	Questions []QuestionStat
}

// Results son los resultados del cuestionario para quien lo administra o lo asignó. Las pruebas
// no cuentan; los intentos en curso solo marcan a la persona.
func (s *Service) Results(ctx context.Context, sc repo.Scope, actorID, id string) (Results, error) {
	_, q, err := s.quiz(ctx, sc, id)
	if err != nil {
		return Results{}, err
	}
	if ok, err := s.seesResults(ctx, sc, actorID, q); err != nil {
		return Results{}, err
	} else if !ok {
		return Results{}, repo.ErrNotFound
	}
	if err := s.expire(ctx, sc, q); err != nil {
		return Results{}, err
	}
	attempts, err := s.store.QuizAttempts(ctx, sc, q.ID)
	if err != nil {
		return Results{}, err
	}
	policy := "best"
	if q.PublishedVersion > 0 {
		if st, _, _, err := s.version(ctx, sc, q.ID, q.PublishedVersion); err == nil {
			policy = st.ScorePolicy
		}
	}
	versions := map[int]versionData{}
	people := map[string]*PersonResult{}
	var order []string
	stats := map[string]*QuestionStat{}
	wrong := map[string]map[string]int{}
	for _, a := range attempts {
		p := people[a.UserID]
		if p == nil {
			p = &PersonResult{UserID: a.UserID, Name: a.Name, Email: a.Email}
			people[a.UserID] = p
			order = append(order, a.UserID)
		}
		if a.Status == "in_progress" {
			p.InProgress = true
			continue
		}
		p.Attempts++
		counted := policy == "last" || p.Attempts == 1 || (policy == "best" && a.Percent > p.Percent)
		if counted {
			p.Percent, p.Passed = a.Percent, a.Passed
		}
		if policy == "best" && a.Passed {
			p.Passed = true
		}
		p.LastAt, p.LastDurationS = a.SubmittedAt, int(a.SubmittedAt.Sub(a.StartedAt).Seconds())
		vd, ok := versions[a.Version]
		if !ok {
			st, qs, _, err := s.version(ctx, sc, q.ID, a.Version)
			if err != nil {
				return Results{}, err
			}
			vd = versionData{st: st, byID: map[string]Question{}}
			for _, x := range qs {
				vd.byID[x.ID] = x
			}
			versions[a.Version] = vd
		}
		var lay []slot
		answers := map[string][]string{}
		if json.Unmarshal([]byte(a.Layout), &lay) != nil || json.Unmarshal([]byte(a.Answers), &answers) != nil {
			continue
		}
		for _, sl := range lay {
			x := vd.byID[sl.Q]
			qs := stats[x.ID]
			if qs == nil {
				qs = &QuestionStat{ID: x.ID}
				stats[x.ID] = qs
				wrong[x.ID] = map[string]int{}
			}
			if a.Version >= qs.Version {
				qs.Prompt, qs.Kind, qs.Version = x.Prompt, x.Kind, a.Version
			}
			qs.Answered++
			if _, ok := grade(x, answers[x.ID], vd.st.MultipleScoring); ok {
				qs.Correct++
				continue
			}
			for _, c := range answers[x.ID] {
				if o := option(x, c); o != nil && !o.Correct {
					wrong[x.ID][label(x, *o)]++
				}
			}
		}
	}
	r := Results{People: make([]PersonResult, len(order)), Questions: []QuestionStat{}}
	for i, u := range order {
		r.People[i] = *people[u]
	}
	for id, qs := range stats {
		for text, n := range wrong[id] {
			if n > qs.TopWrongCount || n == qs.TopWrongCount && text < qs.TopWrong {
				qs.TopWrong, qs.TopWrongCount = text, n
			}
		}
		r.Questions = append(r.Questions, *qs)
	}
	// La que más se falla primero: es la que hay que mirar.
	slices.SortFunc(r.Questions, func(a, b QuestionStat) int {
		return cmp.Or(cmp.Compare(rate(a), rate(b)), cmp.Compare(a.ID, b.ID))
	})
	return r, nil
}

type versionData struct {
	st   Settings
	byID map[string]Question
}

func rate(q QuestionStat) float64 {
	if q.Answered == 0 {
		return 1
	}
	return float64(q.Correct) / float64(q.Answered)
}

func option(q Question, id string) *Option {
	for i := range q.Options {
		if q.Options[i].ID == id {
			return &q.Options[i]
		}
	}
	return nil
}

// label es cómo se nombra una alternativa en los resultados; las de verdadero/falso no tienen
// texto y van como su id, que la interfaz traduce.
func label(q Question, o Option) string {
	if q.Kind == KindTrueFalse {
		return o.ID
	}
	return o.Text
}

// ExportResults son los mismos resultados para el CSV; exportarlos queda en la auditoría
// (§4.16.5).
func (s *Service) ExportResults(ctx context.Context, sc repo.Scope, actorID, id string) (Results, error) {
	r, err := s.Results(ctx, sc, actorID, id)
	if err != nil {
		return r, err
	}
	s.record(ctx, sc, audit.QuizResultsExport, id, map[string]any{"people": len(r.People)})
	return r, nil
}

// GrantExtra da intentos extra a una persona, con un motivo, por una solicitud (requestID) o en
// el cuestionario abierto. Los da quien pidió la lectura o un admin; en el abierto, quien lo
// administra (§4.16.5). La persona recibe un aviso.
func (s *Service) GrantExtra(ctx context.Context, sc repo.Scope, actorID, id, userID, requestID string, extra int, reason string) error {
	_, q, err := s.quiz(ctx, sc, id)
	if err != nil {
		return err
	}
	if sc.MaxRank < editor {
		return repo.ErrNotFound
	}
	if requestID != "" {
		rr, err := s.store.ReadRequestByID(ctx, sc, requestID)
		if err != nil {
			return err
		}
		if rr.QuizID != q.ID || (sc.MaxRank < admin && rr.CreatedBy != actorID) {
			return repo.ErrNotFound
		}
	} else if !manages(sc, actorID, q) {
		return repo.ErrNotFound
	}
	reason = strings.TrimSpace(reason)
	if extra < 1 || extra > maxExtra || reason == "" || utf8.RuneCountInString(reason) > maxReason {
		return ErrInvalidQuiz
	}
	members, err := s.store.Members(ctx, sc)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(members, func(m repo.Member) bool { return m.UserID == userID && repo.RoleRank(m.Role) >= q.PageRank }) {
		return ErrInvalidQuiz
	}
	actor, err := s.store.UserByID(ctx, actorID)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"title": q.Title, "actor": actor.FullName})
	n := repo.NewNotification{UserID: userID, Kind: "quiz_extra_attempts", Category: "quiz_extra", PageID: q.PageID, MinRank: q.PageRank,
		Payload: string(payload)}
	if err := s.store.GrantExtraAttempts(ctx, sc, q.ID, userID, requestID, extra, reason, actorID, n); err != nil {
		return err
	}
	s.record(ctx, sc, audit.QuizExtraAttemptsGrant, q.ID, map[string]any{"user_id": userID, "extra": extra, "request_id": requestID})
	return nil
}

// Assignable: el cuestionario se puede pedir junto con la lectura de la página (§4.14): es de
// esa página, está publicado y el área los tiene encendidos.
func (s *Service) Assignable(ctx context.Context, sc repo.Scope, pageID, id string) error {
	_, q, err := s.quiz(ctx, sc, id)
	if err == nil && (q.PageID != pageID || q.Status != "published") {
		err = repo.ErrNotFound
	}
	return err
}

// Progress es el avance de cada destinatario en el cuestionario de una solicitud, con los
// intentos que da la versión publicada (0 = sin límite).
func (s *Service) Progress(ctx context.Context, sc repo.Scope, quizID, requestID string) (map[string]repo.QuizProgress, int, error) {
	q, err := s.store.QuizByID(ctx, sc, quizID)
	if err != nil {
		return nil, 0, err
	}
	if err := s.expire(ctx, sc, q); err != nil {
		return nil, 0, err
	}
	max := 0
	if q.PublishedVersion > 0 {
		st, _, _, err := s.version(ctx, sc, q.ID, q.PublishedVersion)
		if err != nil {
			return nil, 0, err
		}
		max = st.MaxAttempts
	}
	p, err := s.store.RequestQuizProgress(ctx, sc, requestID)
	return p, max, err
}
