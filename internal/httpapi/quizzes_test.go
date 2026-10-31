package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
)

const quizSettings = `"settings":{"max_attempts":2,"pass_percent":70,"score_policy":"best","cooldown_min":0,"time_limit_s":0,"estimated_min":0,
	"shuffle_questions":true,"shuffle_options":true,"pick":0,"feedback":"all","reveal":"after_final","multiple_scoring":"all_or_nothing"}`

// quizQuestions: q1 alternativa única (a), q2 selección múltiple (x e y), q3 verdadero.
const quizQuestions = `"questions":[
	{"id":"q1","kind":"single","prompt_md":"¿Cada cuánto?","points":1,"explanation_md":"Cada **lunes**","anchor":"pasos",
	 "options":[{"id":"a","text":"Lunes","correct":true},{"id":"b","text":"Martes","correct":false}]},
	{"id":"q2","kind":"multiple","prompt_md":"¿Qué se revisa?","points":1,"explanation_md":"","anchor":"",
	 "options":[{"id":"x","text":"Respaldo","correct":true},{"id":"y","text":"Log","correct":true},{"id":"z","text":"Nada","correct":false}]},
	{"id":"q3","kind":"true_false","prompt_md":"¿Es obligatorio?","points":1,"explanation_md":"","anchor":"",
	 "options":[{"id":"true","text":"","correct":true},{"id":"false","text":"","correct":false}]}]`

const (
	wrongAnswers = `{"answers":{"q1":["b"],"q2":["x"],"q3":["true"]}}`
	rightAnswers = `{"answers":{"q1":["a"],"q2":["x","y"],"q3":["true"]}}`
)

type attemptOut struct {
	ID        string
	Status    string
	Questions []struct {
		ID      string
		Options []struct{ ID string }
	}
	Result *struct {
		Percent  float64
		Passed   bool
		TimedOut bool `json:"timed_out"`
		Revealed bool
	}
}

func attemptOf(t *testing.T, body string) attemptOut {
	t.Helper()
	var a attemptOut
	if err := json.Unmarshal([]byte(body), &a); err != nil || a.ID == "" {
		t.Fatalf("no attempt in %s", body)
	}
	return a
}

// noAnswers: nada de lo que llega antes de enviar dice cuál es la correcta (§4.16.7, §17).
func noAnswers(t *testing.T, what, body string) {
	t.Helper()
	for _, leak := range []string{`"correct"`, "correct_options", "explanation", "Cada **lunes**", "draft"} {
		if strings.Contains(body, leak) {
			t.Errorf("%s leaks %s: %s", what, leak, body)
		}
	}
}

// TestQuizzes son los cuestionarios (§4.16): crear y publicar, asignarlos con una lectura, los
// intentos controlados en el servidor, el historial, los resultados y los intentos extra.
func TestQuizzes(t *testing.T) {
	f := newFixture(t)
	uniform := f.do("adminTI", "GET", "/api/w/no-existe/tree", "")
	f.expect(t, "adminTI", "PUT", "/api/w/ti/pages/respaldo", `{"title":"Respaldo","markdown":"## Pasos\n\nCada lunes.","base_revision":0}`, http.StatusNoContent)
	f.expect(t, "adminTI", "PUT", "/api/w/ti/pages/otra", `{"title":"Otra","markdown":"Uno","base_revision":0}`, http.StatusNoContent)

	// Lo crea un editor sobre una página que puede editar; un lector no.
	if w := f.do("viewerTI", "POST", "/api/w/ti/quizzes", `{"page":"respaldo","title":"Q"}`); w.Code != uniform.Code {
		t.Errorf("a viewer created a quiz: %d", w.Code)
	}
	id := idOf(t, f.expect(t, "editorTI", "POST", "/api/w/ti/quizzes", `{"page":"respaldo","title":"Respaldo semanal"}`, http.StatusCreated))
	q := "/api/w/ti/quizzes/" + id
	// Un borrador a medias se guarda; para publicarlo tiene que estar completo.
	f.expect(t, "editorTI", "PATCH", q, `{"draft":{`+quizSettings+`,"questions":[{"id":"q1","kind":"single","prompt_md":"","options":[]}]}}`, http.StatusOK)
	f.expect(t, "editorTI", "POST", q+"/publish", "", http.StatusUnprocessableEntity)
	f.expect(t, "editorTI", "PATCH", q, `{"draft":{`+quizSettings+`,"questions":[{"id":"q1","kind":"essay","prompt_md":"x","options":[]}]}}`, http.StatusUnprocessableEntity)
	f.expect(t, "editorTI", "PATCH", q, `{"draft":{`+quizSettings+`,`+quizQuestions+`}}`, http.StatusOK)
	// Mientras sea borrador, para el lector no existe.
	if w := f.do("viewerTI", "GET", q, ""); w.Code != uniform.Code {
		t.Errorf("a viewer sees a draft: %d", w.Code)
	}
	if body := f.expect(t, "viewerTI", "GET", "/api/w/ti/quizzes?page=respaldo", "", http.StatusOK); strings.TrimSpace(body) != "[]" {
		t.Errorf("a viewer lists a draft: %s", body)
	}
	f.expect(t, "editorTI", "POST", q+"/publish", "", http.StatusOK)

	// Se asigna con una lectura de la misma página; el lector la recibe como cuestionario.
	f.expect(t, "editorTI", "POST", "/api/w/ti/read-requests", `{"page":"otra","audience":{"all":true},"quiz_id":"`+id+`"}`, http.StatusUnprocessableEntity)
	req := idOf(t, f.expect(t, "editorTI", "POST", "/api/w/ti/read-requests", `{"page":"respaldo","audience":{"all":true},"quiz_id":"`+id+`"}`, http.StatusCreated))
	if items, _ := f.inbox(t, "viewerTI"); len(items) != 1 || items[0].Kind != "quiz_assigned" {
		t.Errorf("viewer inbox: %+v", items)
	}
	body := f.expect(t, "viewerTI", "GET", q, "", http.StatusOK)
	noAnswers(t, "the summary", body)
	if !strings.Contains(body, `"remaining":2`) || !strings.Contains(body, `"questions":3`) || !strings.Contains(body, `"request_id":"`+req+`"`) {
		t.Errorf("summary: %s", body)
	}
	f.expect(t, "viewerTI", "POST", "/api/w/ti/read-requests/"+req+"/ack", `{"revision":1}`, http.StatusConflict)

	// Dos "empezar" a la vez: un solo intento.
	ids := make([]string, 4)
	var wg sync.WaitGroup
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if w := f.do("viewerTI", "POST", q+"/attempts", ""); w.Code == http.StatusOK {
				var a attemptOut
				json.Unmarshal(w.Body.Bytes(), &a)
				ids[i] = a.ID
			}
		}()
	}
	wg.Wait()
	for _, x := range ids {
		if x != ids[0] || x == "" {
			t.Fatalf("concurrent starts made several attempts: %v", ids)
		}
	}
	body = f.expect(t, "viewerTI", "POST", q+"/attempts", "", http.StatusOK)
	noAnswers(t, "the attempt", body)
	first := attemptOf(t, body)
	if first.ID != ids[0] || len(first.Questions) != 3 {
		t.Fatalf("resume: %+v", first)
	}
	att := "/api/w/ti/attempts/" + first.ID
	f.expect(t, "viewerTI", "PUT", att, `{"answers":{"q1":["nope"]}}`, http.StatusUnprocessableEntity)
	f.expect(t, "editorTI", "PUT", att, `{"answers":{"q1":["a"]}}`, http.StatusNotFound)
	f.expect(t, "viewerTI", "PUT", att, `{"answers":{"q1":["b"]}}`, http.StatusNoContent)

	// Primer intento, mal: no aprueba y, como le queda otro, no ve las respuestas.
	body = f.expect(t, "viewerTI", "POST", att+"/submit", wrongAnswers, http.StatusOK)
	if r := attemptOf(t, body).Result; r == nil || r.Passed || r.Percent != 33.3 || r.Revealed || strings.Contains(body, "correct_options") {
		t.Errorf("first attempt: %s", body)
	}
	f.expect(t, "viewerTI", "POST", att+"/submit", rightAnswers, http.StatusConflict)
	if p := f.readPeople(t, "editorTI", req)["viewerTI"]; p.Status != "opened" {
		t.Errorf("after failing once: %+v", p)
	}
	// Segundo, bien: aprueba, ve las respuestas y la lectura queda cumplida.
	second := attemptOf(t, f.expect(t, "viewerTI", "POST", q+"/attempts", "", http.StatusOK))
	body = f.expect(t, "viewerTI", "POST", "/api/w/ti/attempts/"+second.ID+"/submit", rightAnswers, http.StatusOK)
	if r := attemptOf(t, body).Result; r == nil || !r.Passed || r.Percent != 100 || !r.Revealed || !strings.Contains(body, "correct_options") {
		t.Errorf("second attempt: %s", body)
	}
	if p := f.readPeople(t, "editorTI", req)["viewerTI"]; p.Status != "passed" {
		t.Errorf("after passing: %+v", p)
	}
	if body := f.expect(t, "viewerTI", "GET", "/api/me/read-requests", "", http.StatusOK); strings.Contains(body, req) {
		t.Errorf("a passed quiz is still pending: %s", body)
	}
	// Lo sigue viendo, aprobado, para revisar su historial; otro intento ya no.
	if body := f.expect(t, "viewerTI", "GET", q, "", http.StatusOK); !strings.Contains(body, `"passed":true`) || !strings.Contains(body, `"remaining":0`) {
		t.Errorf("summary after passing: %s", body)
	}
	f.expect(t, "viewerTI", "POST", q+"/attempts", "", http.StatusConflict)
	if body := f.expect(t, "viewerTI", "GET", q+"/my-attempts", "", http.StatusOK); strings.Count(body, `"passed":`) != 2 {
		t.Errorf("history: %s", body)
	}
	if body := f.expect(t, "viewerTI", "GET", "/api/me/quizzes", "", http.StatusOK); strings.Count(body, `"quiz":`) != 2 {
		t.Errorf("my quizzes: %s", body)
	}

	// Resultados: quien lo creó, quien lo asignó y el admin; el lector no.
	var res struct {
		People []struct {
			Name     string
			Attempts int
			Percent  float64
			Passed   bool
		}
		Questions []struct {
			ID                string
			Answered, Correct int
		}
	}
	json.Unmarshal([]byte(f.expect(t, "editorTI", "GET", q+"/results", "", http.StatusOK)), &res)
	if len(res.People) != 1 || res.People[0].Attempts != 2 || !res.People[0].Passed || res.People[0].Percent != 100 {
		t.Errorf("people: %+v", res.People)
	}
	if len(res.Questions) != 3 || res.Questions[0].Correct != 1 || res.Questions[0].Answered != 2 {
		t.Errorf("questions: %+v", res.Questions)
	}
	if !strings.Contains(f.expect(t, "editorTI", "GET", q+"/results", "", http.StatusOK), `"top_wrong":"Martes"`) {
		t.Error("the most chosen wrong option is missing")
	}
	f.expect(t, "adminTI", "GET", q+"/results", "", http.StatusOK)
	f.expect(t, "viewerTI", "GET", q+"/results", "", http.StatusNotFound)
	if csv := f.expect(t, "editorTI", "GET", q+"/results?format=csv", "", http.StatusOK); !strings.HasPrefix(csv, "name,email,attempts,score_percent,passed") ||
		!strings.Contains(csv, "viewerTI@empresa.cl,2,100.0,true") {
		t.Errorf("csv: %s", csv)
	}
	if e := find(auditOf(t, f, "adminTI", "/api/w/ti/audit"), "quiz.results_export", "editorTI"); e == nil {
		t.Error("exporting left no audit trail")
	}

	// Probarlo no cuenta; editar y volver a publicar no cambia los puntajes de antes.
	prev := attemptOf(t, f.expect(t, "editorTI", "POST", q+"/preview", "", http.StatusCreated))
	if body := f.expect(t, "editorTI", "POST", "/api/w/ti/attempts/"+prev.ID+"/submit", wrongAnswers, http.StatusOK); !strings.Contains(body, "correct_options") {
		t.Errorf("a preview hides the answers: %s", body)
	}
	f.expect(t, "editorTI", "PATCH", q, `{"draft":{`+quizSettings+`,`+strings.Replace(strings.Replace(quizQuestions,
		`"text":"Lunes","correct":true`, `"text":"Lunes","correct":false`, 1), `"text":"Martes","correct":false`, `"text":"Martes","correct":true`, 1)+`}}`, http.StatusOK)
	if body := f.expect(t, "editorTI", "GET", q, "", http.StatusOK); !strings.Contains(body, `"dirty":true`) {
		t.Errorf("unpublished changes: %s", body)
	}
	f.expect(t, "editorTI", "POST", q+"/publish", "", http.StatusOK)
	json.Unmarshal([]byte(f.expect(t, "editorTI", "GET", q+"/results", "", http.StatusOK)), &res)
	if len(res.People) != 1 || res.People[0].Attempts != 2 || res.People[0].Percent != 100 {
		t.Errorf("results after a new version: %+v", res.People)
	}

	// Una lectura nueva vuelve a dar los intentos. Quien se queda sin aprobar le avisa a quien
	// pidió; un intento extra lo deja seguir.
	req2 := idOf(t, f.expect(t, "adminTI", "POST", "/api/w/ti/read-requests", `{"page":"respaldo","audience":{"roles":["viewer"]},"quiz_id":"`+id+`"}`, http.StatusCreated))
	for range 2 {
		a := attemptOf(t, f.expect(t, "viewerTI", "POST", q+"/attempts", "", http.StatusOK))
		f.expect(t, "viewerTI", "POST", "/api/w/ti/attempts/"+a.ID+"/submit", rightAnswers, http.StatusOK) // la versión 2 cambió la respuesta
	}
	f.expect(t, "viewerTI", "POST", q+"/attempts", "", http.StatusConflict)
	if p := f.readPeople(t, "adminTI", req2)["viewerTI"]; p.Status != "failed_out" {
		t.Errorf("after running out: %+v", p)
	}
	if items, _ := f.inbox(t, "adminTI"); len(items) == 0 || items[0].Kind != "quiz_failed_out" {
		t.Errorf("assigner inbox: %+v", items)
	}
	f.expect(t, "editorTI", "POST", q+"/extra-attempts", `{"user_id":"`+f.users["viewerTI"]+`","request_id":"`+req2+`","extra":1,"reason":"Otra oportunidad"}`, http.StatusNotFound)
	f.expect(t, "adminTI", "POST", q+"/extra-attempts", `{"user_id":"`+f.users["viewerTI"]+`","request_id":"`+req2+`","extra":1,"reason":""}`, http.StatusUnprocessableEntity)
	f.expect(t, "adminTI", "POST", q+"/extra-attempts", `{"user_id":"`+f.users["viewerTI"]+`","request_id":"`+req2+`","extra":1,"reason":"Otra oportunidad"}`, http.StatusNoContent)
	if p := f.readPeople(t, "adminTI", req2)["viewerTI"]; p.Status != "opened" {
		t.Errorf("after an extra attempt: %+v", p)
	}
	if items, _ := f.inbox(t, "viewerTI"); len(items) == 0 || items[0].Kind != "quiz_extra_attempts" {
		t.Errorf("viewer inbox after the extra attempt: %+v", items)
	}

	// El tiempo límite y la espera se controlan en el servidor: enviar tarde vale lo guardado.
	f.expect(t, "adminTI", "POST", "/api/w/ti/read-requests/"+req2+"/close", "", http.StatusNoContent)
	f.expect(t, "editorTI", "PATCH", q, `{"open_to_readers":true,"draft":{`+strings.Replace(strings.Replace(quizSettings,
		`"time_limit_s":0`, `"time_limit_s":60`, 1), `"cooldown_min":0`, `"cooldown_min":15`, 1)+`,`+quizQuestions+`}}`, http.StatusOK)
	f.expect(t, "editorTI", "POST", q+"/publish", "", http.StatusOK)
	timed := attemptOf(t, f.expect(t, "viewerTI", "POST", q+"/attempts", "", http.StatusOK))
	f.expect(t, "viewerTI", "PUT", "/api/w/ti/attempts/"+timed.ID, `{"answers":{"q1":["a"]}}`, http.StatusNoContent)
	f.db.Write.Exec(`UPDATE quiz_attempts SET deadline_at = datetime('now', '-1 minute') WHERE id = ?`, timed.ID)
	f.expect(t, "viewerTI", "PUT", "/api/w/ti/attempts/"+timed.ID, rightAnswers, http.StatusConflict)
	body = f.expect(t, "viewerTI", "GET", q+"/my-attempts", "", http.StatusOK)
	if !strings.Contains(body, `"status":"timed_out"`) || !strings.Contains(body, `"percent":33.3`) {
		t.Errorf("a late answer changed the score: %s", body)
	}
	f.expect(t, "viewerTI", "POST", q+"/attempts", "", http.StatusConflict) // la espera de 15 minutos
	if e := find(auditOf(t, f, "adminTI", "/api/w/ti/audit"), "quiz.attempt_timeout", "viewerTI"); e == nil {
		t.Error("the timeout left no audit trail")
	}

	// Aislamiento: otra área no lo ve; apagarlos en el área los esconde; archivado no se empieza.
	for _, who := range []string{"adminFin", "outsider"} {
		for _, path := range []string{q, q + "/results", q + "/my-attempts", "/api/w/ti/quizzes?page=respaldo"} {
			if w := f.do(who, "GET", path, ""); w.Code != uniform.Code {
				t.Errorf("%s %s: %d", who, path, w.Code)
			}
		}
	}
	f.expect(t, "editorTI", "DELETE", q, "", http.StatusConflict)
	f.expect(t, "editorTI", "POST", q+"/archive", "", http.StatusNoContent)
	f.expect(t, "viewerTI", "GET", q, "", http.StatusNotFound)
	draft := idOf(t, f.expect(t, "editorTI", "POST", "/api/w/ti/quizzes", `{"page":"respaldo","title":"Borrador"}`, http.StatusCreated))
	f.expect(t, "editorTI", "DELETE", "/api/w/ti/quizzes/"+draft, "", http.StatusNoContent)
	f.expect(t, "adminTI", "PATCH", "/api/w/ti/settings", `{"quizzes_enabled":false}`, http.StatusNoContent)
	f.expect(t, "editorTI", "GET", q, "", http.StatusNotFound)
	if body := f.expect(t, "editorTI", "GET", "/api/w/ti/quizzes?page=respaldo", "", http.StatusOK); strings.TrimSpace(body) != "[]" {
		t.Errorf("quizzes switched off still list: %s", body)
	}
}
