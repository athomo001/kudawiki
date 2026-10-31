package quiz

import (
	"math/rand/v2"
	"slices"
	"testing"
)

func TestGrade(t *testing.T) {
	multi := Question{Kind: KindMultiple, Points: 2, Options: []Option{{ID: "a", Correct: true}, {ID: "b", Correct: true}, {ID: "c"}, {ID: "d"}}}
	single := Question{Kind: KindSingle, Points: 1, Options: []Option{{ID: "a", Correct: true}, {ID: "b"}}}
	for _, c := range []struct {
		name    string
		q       Question
		chosen  []string
		scoring string
		earned  float64
		correct bool
	}{
		{"single right", single, []string{"a"}, "all_or_nothing", 1, true},
		{"single wrong", single, []string{"b"}, "all_or_nothing", 0, false},
		{"single with both picked", single, []string{"a", "b"}, "all_or_nothing", 0, false},
		{"single unanswered", single, nil, "all_or_nothing", 0, false},
		{"multiple all", multi, []string{"b", "a"}, "all_or_nothing", 2, true},
		{"multiple half, all or nothing", multi, []string{"a"}, "all_or_nothing", 0, false},
		{"multiple half, partial", multi, []string{"a"}, "partial", 1, false},
		{"multiple hit and miss, partial", multi, []string{"a", "c"}, "partial", 0, false},
		{"multiple more misses than hits, minimum 0", multi, []string{"a", "c", "d"}, "partial", 0, false},
		{"multiple repeated answer counts once", multi, []string{"a", "a"}, "partial", 1, false},
	} {
		earned, correct := grade(c.q, c.chosen, c.scoring)
		if earned != c.earned || correct != c.correct {
			t.Errorf("%s: got %v %v, want %v %v", c.name, earned, correct, c.earned, c.correct)
		}
	}
}

func TestLayout(t *testing.T) {
	qs := []Question{
		{ID: "1", Kind: KindSingle, Options: []Option{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}}},
		{ID: "2", Kind: KindTrueFalse, Options: []Option{{ID: "true"}, {ID: "false"}}},
		{ID: "3", Kind: KindMultiple, Options: []Option{{ID: "a"}, {ID: "b"}}},
		{ID: "4", Kind: KindSingle, Options: []Option{{ID: "a"}, {ID: "b"}}},
	}
	r := rand.New(rand.NewPCG(1, 2))
	s := Defaults()
	s.Pick = 2
	seen := map[string]bool{}
	for range 50 {
		lay := layout(s, qs, r)
		if len(lay) != 2 || lay[0].Q == lay[1].Q {
			t.Fatalf("pick 2 of 4: %+v", lay)
		}
		for _, sl := range lay {
			seen[sl.Q] = true
			if sl.Q == "2" && !slices.Equal(sl.O, []string{"true", "false"}) {
				t.Fatalf("true/false shuffled: %v", sl.O)
			}
		}
	}
	if len(seen) != 4 {
		t.Errorf("not every question came up in 50 attempts: %v", seen)
	}
	s = Defaults()
	s.ShuffleQuestions, s.ShuffleOptions = false, false
	lay := layout(s, qs, r)
	if lay[0].Q != "1" || lay[3].Q != "4" || !slices.Equal(lay[0].O, []string{"a", "b", "c", "d"}) {
		t.Errorf("without shuffling the order changed: %+v", lay)
	}
}

func TestNormalize(t *testing.T) {
	d := Draft{Settings: Defaults(), Questions: []Question{{Kind: KindTrueFalse, Prompt: "x", Options: []Option{{ID: "false", Correct: true}}}}}
	if err := d.normalize(true); err != nil {
		t.Fatal(err)
	}
	q := d.Questions[0]
	if q.ID == "" || q.Points != 1 || len(q.Options) != 2 || q.Options[0].ID != "true" || q.Options[0].Correct || !q.Options[1].Correct {
		t.Errorf("true/false normalized: %+v", q)
	}
	for _, bad := range []Draft{
		{Settings: Defaults()}, // sin preguntas no se publica
		{Settings: Defaults(), Questions: []Question{{Kind: KindSingle, Prompt: "x", Options: []Option{{Text: "a", Correct: true}, {Text: "b", Correct: true}}}}},
		{Settings: Defaults(), Questions: []Question{{Kind: KindMultiple, Prompt: "x", Options: []Option{{Text: "a"}, {Text: "b"}}}}},
		{Settings: Settings{PassPercent: 70, ScorePolicy: "best", Feedback: "wrong", Reveal: "always", MultipleScoring: "partial", CooldownMin: 5}},
	} {
		if err := bad.normalize(true); err == nil {
			t.Errorf("accepted: %+v", bad)
		}
	}
	if got := EstimatedMinutes(Defaults(), []Question{{Kind: KindTrueFalse}, {Kind: KindSingle}, {Kind: KindMultiple}}); got != 3 {
		t.Errorf("estimated: %d", got) // 30 + 45 + 60 s = 2,25 min → 3
	}
}
