// Package quiz son los cuestionarios (SPEC §4.16): preguntas sobre una página, intentos con
// orden revuelto y tiempo controlado en el servidor, historial y resultados. Las respuestas
// correctas no salen de este paquete hasta que el intento se corrige.
package quiz

import (
	"errors"
	"math"
	"math/rand/v2"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/oklog/ulid/v2"
)

var (
	// ErrInvalidQuiz: las preguntas o la configuración no sirven (para publicar, o en absoluto).
	ErrInvalidQuiz = errors.New("invalid_quiz")
	// ErrNoAttempts: la persona ya usó todos sus intentos.
	ErrNoAttempts = errors.New("no_attempts_left")
	// ErrCooldown: todavía no pasa la espera entre intentos.
	ErrCooldown = errors.New("quiz_cooldown")
	// ErrAttemptClosed: el intento ya se envió o se le acabó el tiempo.
	ErrAttemptClosed = errors.New("attempt_closed")
	// ErrQuizPublished: un cuestionario que ya se publicó tiene resultados; se archiva, no se borra.
	ErrQuizPublished = errors.New("quiz_published")
)

// Tipos de pregunta (§4.16.2). Sin preguntas abiertas (§19.27).
const (
	KindSingle    = "single"
	KindMultiple  = "multiple"
	KindTrueFalse = "true_false"
)

type Option struct {
	ID      string `json:"id"`
	Text    string `json:"text"`
	Correct bool   `json:"correct"`
}

type Question struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Prompt      string   `json:"prompt_md"`
	Points      int      `json:"points"`
	Explanation string   `json:"explanation_md"`
	Anchor      string   `json:"anchor"` // la sección de la página que la responde
	Options     []Option `json:"options"`
}

// Settings es la configuración de §4.16.3. Los ceros son "sin": sin límite de intentos, sin
// espera, sin tiempo límite, tiempo estimado automático, todas las preguntas.
type Settings struct {
	MaxAttempts      int    `json:"max_attempts"`
	PassPercent      int    `json:"pass_percent"`
	ScorePolicy      string `json:"score_policy"` // best | last | first
	CooldownMin      int    `json:"cooldown_min"`
	TimeLimitS       int    `json:"time_limit_s"`
	EstimatedMin     int    `json:"estimated_min"`
	ShuffleQuestions bool   `json:"shuffle_questions"`
	ShuffleOptions   bool   `json:"shuffle_options"`
	Pick             int    `json:"pick"`
	Feedback         string `json:"feedback"`         // score | wrong | all
	Reveal           string `json:"reveal"`           // never | after_final | always
	MultipleScoring  string `json:"multiple_scoring"` // all_or_nothing | partial
}

// Defaults son los de §4.16.3.
func Defaults() Settings {
	return Settings{MaxAttempts: 2, PassPercent: 70, ScorePolicy: "best", ShuffleQuestions: true, ShuffleOptions: true,
		Feedback: "wrong", Reveal: "after_final", MultipleScoring: "all_or_nothing"}
}

// Draft es lo que se edita; al publicar se congela en una versión.
type Draft struct {
	Settings  Settings   `json:"settings"`
	Questions []Question `json:"questions"`
}

const (
	maxTitle, maxQuestions, maxOptions = 200, 100, 10
	maxPrompt, maxOptionText           = 4000, 500
)

var idRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)

// normalize ordena el borrador para guardarlo: ids a lo que no tiene, verdadero/falso con sus
// dos alternativas fijas. Revisa lo que tiene que valer siempre; con ready, además, lo que hace
// falta para que alguien lo responda (publicar o probar).
func (d *Draft) normalize(ready bool) error {
	s := d.Settings
	if s.MaxAttempts < 0 || s.MaxAttempts > 100 || s.PassPercent < 1 || s.PassPercent > 100 ||
		!slices.Contains([]string{"best", "last", "first"}, s.ScorePolicy) || !slices.Contains([]int{0, 15, 60, 1440}, s.CooldownMin) ||
		s.TimeLimitS < 0 || s.TimeLimitS > 24*3600 || s.EstimatedMin < 0 || s.EstimatedMin > 600 || s.Pick < 0 ||
		!slices.Contains([]string{"score", "wrong", "all"}, s.Feedback) || !slices.Contains([]string{"never", "after_final", "always"}, s.Reveal) ||
		!slices.Contains([]string{"all_or_nothing", "partial"}, s.MultipleScoring) || len(d.Questions) > maxQuestions {
		return ErrInvalidQuiz
	}
	if ready && (len(d.Questions) == 0 || s.Pick > len(d.Questions)) {
		return ErrInvalidQuiz
	}
	if d.Questions == nil {
		d.Questions = []Question{}
	}
	seen := map[string]bool{}
	for i := range d.Questions {
		q := &d.Questions[i]
		if q.ID == "" {
			q.ID = ulid.Make().String()
		}
		if q.Points == 0 {
			q.Points = 1
		}
		q.Prompt, q.Explanation, q.Anchor = strings.TrimSpace(q.Prompt), strings.TrimSpace(q.Explanation), strings.TrimSpace(q.Anchor)
		if !idRe.MatchString(q.ID) || seen[q.ID] || q.Points < 1 || q.Points > 100 || utf8.RuneCountInString(q.Prompt) > maxPrompt ||
			utf8.RuneCountInString(q.Explanation) > maxPrompt || utf8.RuneCountInString(q.Anchor) > maxTitle || (ready && q.Prompt == "") {
			return ErrInvalidQuiz
		}
		seen[q.ID] = true
		switch q.Kind {
		case KindTrueFalse:
			// Siempre las mismas dos, en este orden; el texto lo pone la interfaz en su idioma.
			correct := len(q.Options) > 0 && q.Options[0].ID == "true" && q.Options[0].Correct ||
				len(q.Options) > 1 && q.Options[1].ID == "true" && q.Options[1].Correct
			q.Options = []Option{{ID: "true", Correct: correct}, {ID: "false", Correct: !correct}}
		case KindSingle, KindMultiple:
			if len(q.Options) > maxOptions {
				return ErrInvalidQuiz
			}
			ids, right := map[string]bool{}, 0
			for j := range q.Options {
				o := &q.Options[j]
				if o.ID == "" {
					o.ID = ulid.Make().String()
				}
				o.Text = strings.TrimSpace(o.Text)
				if !idRe.MatchString(o.ID) || ids[o.ID] || utf8.RuneCountInString(o.Text) > maxOptionText || (ready && o.Text == "") {
					return ErrInvalidQuiz
				}
				ids[o.ID] = true
				if o.Correct {
					right++
				}
			}
			if ready && (len(q.Options) < 2 || right == 0 || (q.Kind == KindSingle && right != 1)) {
				return ErrInvalidQuiz
			}
		default:
			return ErrInvalidQuiz
		}
		if q.Options == nil {
			q.Options = []Option{}
		}
	}
	return nil
}

// EstimatedMinutes es el tiempo que se anuncia antes de empezar: el que puso quien lo creó o el
// calculado (30 s verdadero/falso, 45 s alternativa única, 60 s selección múltiple, §4.16.3).
func EstimatedMinutes(s Settings, qs []Question) int {
	if s.EstimatedMin > 0 {
		return s.EstimatedMin
	}
	secs := 0
	for _, q := range qs {
		switch q.Kind {
		case KindTrueFalse:
			secs += 30
		case KindSingle:
			secs += 45
		default:
			secs += 60
		}
	}
	if s.Pick > 0 && s.Pick < len(qs) {
		secs = secs * s.Pick / len(qs)
	}
	return max(1, int(math.Ceil(float64(secs)/60)))
}

// QuestionCount son las preguntas que tiene cada intento.
func QuestionCount(s Settings, qs []Question) int {
	if s.Pick > 0 && s.Pick < len(qs) {
		return s.Pick
	}
	return len(qs)
}

// slot es una pregunta del intento, con sus alternativas en el orden en que se mostraron.
type slot struct {
	Q string   `json:"q"`
	O []string `json:"o"`
}

// layout arma el intento: las preguntas (todas o N al azar) y el orden de preguntas y
// alternativas. Verdadero/falso nunca se revuelve (§4.16.3).
func layout(s Settings, qs []Question, r *rand.Rand) []slot {
	idx := make([]int, len(qs))
	for i := range idx {
		idx[i] = i
	}
	if s.Pick > 0 && s.Pick < len(qs) {
		r.Shuffle(len(idx), func(i, j int) { idx[i], idx[j] = idx[j], idx[i] })
		idx = idx[:s.Pick]
		if !s.ShuffleQuestions {
			slices.Sort(idx)
		}
	} else if s.ShuffleQuestions {
		r.Shuffle(len(idx), func(i, j int) { idx[i], idx[j] = idx[j], idx[i] })
	}
	out := make([]slot, len(idx))
	for i, k := range idx {
		q := qs[k]
		opts := make([]string, len(q.Options))
		for j, o := range q.Options {
			opts[j] = o.ID
		}
		if s.ShuffleOptions && q.Kind != KindTrueFalse {
			r.Shuffle(len(opts), func(i, j int) { opts[i], opts[j] = opts[j], opts[i] })
		}
		out[i] = slot{Q: q.ID, O: opts}
	}
	return out
}

// grade corrige una pregunta. Selección múltiple: todo o nada, o aciertos menos errores con
// mínimo 0 (§4.16.2). correct es si sacó el puntaje completo.
func grade(q Question, chosen []string, scoring string) (earned float64, correct bool) {
	right := map[string]bool{}
	for _, o := range q.Options {
		if o.Correct {
			right[o.ID] = true
		}
	}
	hits, wrong := 0, 0
	for _, c := range slices.Compact(slices.Sorted(slices.Values(chosen))) {
		if right[c] {
			hits++
		} else {
			wrong++
		}
	}
	full := hits == len(right) && wrong == 0 && len(right) > 0
	if q.Kind != KindMultiple {
		full = full && len(chosen) == 1
	}
	if full {
		return float64(q.Points), true
	}
	if q.Kind == KindMultiple && scoring == "partial" && len(right) > 0 {
		return math.Max(0, float64(hits-wrong)) / float64(len(right)) * float64(q.Points), false
	}
	return 0, false
}

// valid revisa que las respuestas sean de preguntas y alternativas del intento.
func valid(lay []slot, answers map[string][]string) bool {
	byQ := make(map[string][]string, len(lay))
	for _, s := range lay {
		byQ[s.Q] = s.O
	}
	for q, chosen := range answers {
		opts, ok := byQ[q]
		if !ok || len(chosen) > len(opts) {
			return false
		}
		for _, c := range chosen {
			if !slices.Contains(opts, c) {
				return false
			}
		}
	}
	return true
}
