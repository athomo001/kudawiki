package httpapi

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"time"

	"github.com/athomo001/kudawiki/internal/reading"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/go-chi/chi/v5"
)

// readingRoutes es el acuse de lectura (§4.14). No hay en el espacio personal: no hay a quién
// pedirle nada. Quién pide a quién lo decide reading.Service.
func (a api) readingRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(a.areaOnly)
		r.Get("/read-requests", a.listReadRequests)
		r.Get("/read-requests/candidates", a.readCandidates)
		r.Post("/read-requests", a.createReadRequest)
		r.Get("/read-requests/{id}", a.readRequest)
		r.Post("/read-requests/{id}/close", a.closeReadRequest)
		r.Post("/read-requests/{id}/ack", a.ackReadRequest)
	})
}

func readRequestJSON(rr repo.ReadRequest) map[string]any {
	out := map[string]any{"id": rr.ID, "page": map[string]any{"slug": rr.PageSlug, "title": rr.PageTitle, "level": rr.PageRank},
		"revision": rr.Revision, "statement": rr.Statement, "audience": rr.Audience, "due_at": optTime(rr.DueAt),
		"created_by": rr.Creator, "created_at": rr.CreatedAt, "closed_at": optTime(rr.ClosedAt), "quiz_id": rr.QuizID}
	if rr.Audience.Users == nil {
		out["audience"] = map[string]any{"all": rr.Audience.All, "roles": nonNil(rr.Audience.Roles)}
	}
	return out
}

func optTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func (a api) listReadRequests(w http.ResponseWriter, r *http.Request) {
	list, err := a.Reading.List(r.Context(), scopeOf(r), currentUser(r).ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]map[string]any, len(list))
	for i, s := range list {
		out[i] = readRequestJSON(s.ReadRequest)
		out[i]["recipients"], out[i]["opened"], out[i]["confirmed"] = s.Recipients, s.Opened, s.Confirmed
	}
	a.json(w, http.StatusOK, out)
}

// readCandidates: a quiénes puede elegir, una a una, quien pide el acuse sobre ?page=.
func (a api) readCandidates(w http.ResponseWriter, r *http.Request) {
	list, err := a.Reading.Candidates(r.Context(), scopeOf(r), currentUser(r).ID, r.URL.Query().Get("page"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]map[string]string, len(list))
	for i, m := range list {
		out[i] = map[string]string{"user_id": m.UserID, "name": m.FullName, "role": m.Role}
	}
	a.json(w, http.StatusOK, out)
}

func (a api) createReadRequest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Page      string        `json:"page"`
		Statement string        `json:"statement"`
		DueAt     *time.Time    `json:"due_at"`
		Audience  repo.Audience `json:"audience"`
		QuizID    string        `json:"quiz_id"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	req := reading.Input{Slug: in.Page, Statement: in.Statement, Audience: in.Audience, QuizID: in.QuizID}
	if in.DueAt != nil {
		req.DueAt = *in.DueAt
	}
	id, err := a.Reading.Create(r.Context(), scopeOf(r), currentUser(r).ID, req)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.json(w, http.StatusCreated, map[string]string{"id": id})
}

// readRequest es el panel de la solicitud; con ?format=csv, el mismo estado por persona como
// planilla. Las columnas y los estados van como identificadores, igual que en el JSON.
func (a api) readRequest(w http.ResponseWriter, r *http.Request) {
	d, err := a.Reading.Get(r.Context(), scopeOf(r), currentUser(r).ID, chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if r.URL.Query().Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="read-request-`+d.ID+`.csv"`)
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"name", "email", "role", "status", "first_opened_at", "last_opened_at", "opens", "confirmed_at", "confirmed_revision",
			"attempts", "best_percent"})
		for _, p := range d.People {
			_ = cw.Write([]string{p.Name, p.Email, p.Role, p.Status, csvTime(p.FirstOpen), csvTime(p.LastOpen), strconv.Itoa(p.Opens),
				csvTime(p.AckAt), strconv.Itoa(p.AckRevision), strconv.Itoa(p.Quiz.Used), strconv.FormatFloat(p.Quiz.BestPercent, 'f', 1, 64)})
		}
		cw.Flush()
		return
	}
	people := make([]map[string]any, len(d.People))
	for i, p := range d.People {
		people[i] = map[string]any{"user_id": p.UserID, "name": p.Name, "email": p.Email, "role": p.Role, "status": p.Status,
			"first_opened_at": optTime(p.FirstOpen), "last_opened_at": optTime(p.LastOpen), "opens": p.Opens,
			"confirmed_at": optTime(p.AckAt), "confirmed_revision": p.AckRevision, "attempts": p.Quiz.Used, "extra_attempts": p.Quiz.Extra,
			"best_percent": p.Quiz.BestPercent}
	}
	out := readRequestJSON(d.ReadRequest)
	out["people"] = people
	a.json(w, http.StatusOK, out)
}

func csvTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func (a api) closeReadRequest(w http.ResponseWriter, r *http.Request) {
	if err := a.Reading.Close(r.Context(), scopeOf(r), currentUser(r).ID, chi.URLParam(r, "id")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ackReadRequest es "Confirmo que lo leí", con la revisión que la persona tenía en pantalla.
func (a api) ackReadRequest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int `json:"revision"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	if err := a.Reading.Ack(r.Context(), scopeOf(r), currentUser(r).ID, chi.URLParam(r, "id"), in.Revision); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// myReadsJSON son las solicitudes abiertas que quien lee tiene sobre la página (§4.14): el aviso
// y el botón al final. Abrir la página queda anotado para quien la pidió.
func myReadsJSON(reads []repo.MyRead) []map[string]any {
	out := make([]map[string]any, len(reads))
	for i, x := range reads {
		out[i] = map[string]any{"id": x.ID, "statement": x.Statement, "requested_by": x.Creator, "revision": x.Revision,
			"due_at": optTime(x.DueAt), "confirmed_at": optTime(x.AckAt), "confirmed_revision": x.AckRevision, "quiz_id": x.QuizID}
	}
	return out
}

// myReadRequests son las lecturas por confirmar en todas las áreas (/me, B5).
func (a api) myReadRequests(w http.ResponseWriter, r *http.Request) {
	list, err := a.Read.PendingReads(r.Context(), currentUser(r).ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]map[string]any, len(list))
	for i, x := range list {
		out[i] = map[string]any{"id": x.ID, "area": map[string]string{"slug": x.AreaSlug, "name": x.AreaName},
			"page": map[string]string{"slug": x.PageSlug, "title": x.PageTitle}, "statement": x.Statement,
			"requested_by": x.Creator, "due_at": optTime(x.DueAt), "quiz_id": x.QuizID}
	}
	a.json(w, http.StatusOK, out)
}
