package httpapi

import (
	"encoding/csv"
	"net/http"
	"strconv"

	"github.com/athomo001/kudawiki/internal/quiz"
	"github.com/go-chi/chi/v5"
)

// quizRoutes son los cuestionarios (§4.16, §14.3). No hay en el espacio personal (§4.16.1). Quién
// ve qué lo decide quiz.Service: el resumen para quien lo hace, el borrador con las respuestas
// solo para quien lo administra. Los de una página van con ?page= (y "page" al crear): la ruta de
// una página puede tener barras y /pages/* no deja poner nada después.
func (a api) quizRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(a.areaOnly)
		r.Get("/quizzes", a.pageQuizzes)
		r.Post("/quizzes", a.createQuiz)
		r.Get("/quizzes/{id}", a.getQuiz)
		r.Patch("/quizzes/{id}", a.updateQuiz)
		r.Delete("/quizzes/{id}", a.deleteQuiz)
		r.Post("/quizzes/{id}/publish", a.publishQuiz)
		r.Post("/quizzes/{id}/archive", a.archiveQuiz)
		r.Post("/quizzes/{id}/preview", a.previewQuiz)
		r.Post("/quizzes/{id}/attempts", a.startAttempt)
		r.Get("/quizzes/{id}/my-attempts", a.myAttempts)
		r.Get("/quizzes/{id}/results", a.quizResults)
		r.Post("/quizzes/{id}/extra-attempts", a.extraAttempts)
		r.Put("/attempts/{id}", a.saveAttempt)
		r.Post("/attempts/{id}/submit", a.submitAttempt)
	})
}

func summaryJSON(s *quiz.Summary) any {
	if s == nil {
		return nil
	}
	return s
}

func (a api) pageQuizzes(w http.ResponseWriter, r *http.Request) {
	list, err := a.Quizzes.List(r.Context(), scopeOf(r), currentUser(r).ID, r.URL.Query().Get("page"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]map[string]any, len(list))
	for i, q := range list {
		out[i] = map[string]any{"id": q.ID, "title": q.Title, "status": q.Status, "manage": q.Manage, "summary": summaryJSON(q.Summary)}
	}
	a.json(w, http.StatusOK, out)
}

func (a api) createQuiz(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Page  string `json:"page"`
		Title string `json:"title"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	id, err := a.Quizzes.Create(r.Context(), scopeOf(r), currentUser(r).ID, in.Page, in.Title)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusCreated, map[string]string{"id": id})
}

// getQuiz: quien lo administra recibe el borrador (con las respuestas correctas, para editarlo);
// el resto, el resumen de lo que le toca hacer.
func (a api) getQuiz(w http.ResponseWriter, r *http.Request) {
	v, err := a.Quizzes.Get(r.Context(), scopeOf(r), currentUser(r).ID, chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := map[string]any{"id": v.ID, "title": v.Title, "status": v.Status, "open_to_readers": v.OpenToReaders,
		"page":       map[string]any{"slug": v.PageSlug, "title": v.PageTitle, "level": v.PageRank},
		"created_by": v.Creator, "manage": v.Manage, "results": v.Results, "summary": summaryJSON(v.Summary)}
	if v.Manage {
		out["draft"], out["dirty"], out["page_changed"], out["published_version"] = v.Draft, v.Dirty, v.PageChanged, v.PublishedVersion
	}
	a.json(w, http.StatusOK, out)
}

func (a api) updateQuiz(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title         *string     `json:"title"`
		OpenToReaders *bool       `json:"open_to_readers"`
		Draft         *quiz.Draft `json:"draft"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	d, err := a.Quizzes.Update(r.Context(), scopeOf(r), currentUser(r).ID, chi.URLParam(r, "id"),
		quiz.Patch{Title: in.Title, OpenToReaders: in.OpenToReaders, Draft: in.Draft})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, map[string]any{"draft": d})
}

func (a api) deleteQuiz(w http.ResponseWriter, r *http.Request) {
	if err := a.Quizzes.Delete(r.Context(), scopeOf(r), currentUser(r).ID, chi.URLParam(r, "id")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a api) publishQuiz(w http.ResponseWriter, r *http.Request) {
	n, err := a.Quizzes.Publish(r.Context(), scopeOf(r), currentUser(r).ID, chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, map[string]int{"version": n})
}

func (a api) archiveQuiz(w http.ResponseWriter, r *http.Request) {
	if err := a.Quizzes.Archive(r.Context(), scopeOf(r), currentUser(r).ID, chi.URLParam(r, "id")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a api) previewQuiz(w http.ResponseWriter, r *http.Request) {
	v, err := a.Quizzes.Preview(r.Context(), scopeOf(r), currentUser(r).ID, chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusCreated, v)
}

// startAttempt devuelve el intento: las preguntas sin las respuestas correctas y deadline_at.
func (a api) startAttempt(w http.ResponseWriter, r *http.Request) {
	v, err := a.Quizzes.Start(r.Context(), scopeOf(r), currentUser(r).ID, chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, v)
}

func (a api) myAttempts(w http.ResponseWriter, r *http.Request) {
	list, err := a.Quizzes.MyAttempts(r.Context(), scopeOf(r), currentUser(r).ID, chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, list)
}

type answersBody struct {
	Answers map[string][]string `json:"answers"`
}

func (a api) saveAttempt(w http.ResponseWriter, r *http.Request) {
	var in answersBody
	if !a.decode(w, r, &in) {
		return
	}
	if err := a.Quizzes.Save(r.Context(), scopeOf(r), currentUser(r).ID, chi.URLParam(r, "id"), in.Answers); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a api) submitAttempt(w http.ResponseWriter, r *http.Request) {
	var in answersBody
	if !a.decode(w, r, &in) {
		return
	}
	v, err := a.Quizzes.Submit(r.Context(), scopeOf(r), currentUser(r).ID, chi.URLParam(r, "id"), in.Answers)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusOK, v)
}

// quizResults son los resultados por persona y por pregunta; con ?format=csv, una fila por
// persona, con encabezados como identificadores. Exportar queda en la auditoría.
func (a api) quizResults(w http.ResponseWriter, r *http.Request) {
	sc, actor, id := scopeOf(r), currentUser(r).ID, chi.URLParam(r, "id")
	if r.URL.Query().Get("format") == "csv" {
		res, err := a.Quizzes.ExportResults(r.Context(), sc, actor, id)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="quiz-results-`+id+`.csv"`)
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"name", "email", "attempts", "score_percent", "passed", "in_progress", "last_attempt_at", "last_duration_s"})
		for _, p := range res.People {
			_ = cw.Write([]string{p.Name, p.Email, strconv.Itoa(p.Attempts), strconv.FormatFloat(p.Percent, 'f', 1, 64),
				strconv.FormatBool(p.Passed), strconv.FormatBool(p.InProgress), csvTime(p.LastAt), strconv.Itoa(p.LastDurationS)})
		}
		cw.Flush()
		return
	}
	res, err := a.Quizzes.Results(r.Context(), sc, actor, id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	people := make([]map[string]any, len(res.People))
	for i, p := range res.People {
		people[i] = map[string]any{"user_id": p.UserID, "name": p.Name, "email": p.Email, "attempts": p.Attempts, "percent": p.Percent,
			"passed": p.Passed, "in_progress": p.InProgress, "last_attempt_at": optTime(p.LastAt), "last_duration_s": p.LastDurationS}
	}
	questions := make([]map[string]any, len(res.Questions))
	for i, q := range res.Questions {
		questions[i] = map[string]any{"id": q.ID, "prompt": q.Prompt, "kind": q.Kind, "answered": q.Answered, "correct": q.Correct,
			"top_wrong": q.TopWrong, "top_wrong_count": q.TopWrongCount}
	}
	a.json(w, http.StatusOK, map[string]any{"people": people, "questions": questions})
}

func (a api) extraAttempts(w http.ResponseWriter, r *http.Request) {
	var in struct {
		UserID    string `json:"user_id"`
		RequestID string `json:"request_id"`
		Extra     int    `json:"extra"`
		Reason    string `json:"reason"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	if err := a.Quizzes.GrantExtra(r.Context(), scopeOf(r), currentUser(r).ID, chi.URLParam(r, "id"), in.UserID, in.RequestID, in.Extra, in.Reason); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// myQuizzes es el historial de intentos propio en todas las áreas (/me, §14.1). Los pendientes
// van con las lecturas por confirmar (/me/read-requests): son solicitudes con cuestionario.
func (a api) myQuizzes(w http.ResponseWriter, r *http.Request) {
	list, err := a.Read.MyQuizAttempts(r.Context(), currentUser(r).ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]map[string]any, len(list))
	for i, x := range list {
		out[i] = map[string]any{"id": x.ID, "quiz": map[string]string{"id": x.QuizID, "title": x.QuizTitle},
			"area": map[string]string{"slug": x.AreaSlug, "name": x.AreaName}, "page": x.PageSlug, "status": x.Status,
			"started_at": x.StartedAt, "submitted_at": optTime(x.SubmittedAt), "percent": x.Percent, "passed": x.Passed}
	}
	a.json(w, http.StatusOK, out)
}
