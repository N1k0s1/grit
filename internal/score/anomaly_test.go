package score

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/n1k0s1/grit/internal/model"
)

func has(flags []string, f string) bool {
	for _, x := range flags {
		if x == f {
			return true
		}
	}
	return false
}

// steady builds n turns with prompts every gap minutes plus jitter[i].
func steady(n int, gap float64, jitter []float64, text func(i int) string) model.Session {
	var ev []model.Event
	for i := 0; i < n; i++ {
		m := float64(i) * gap
		if i < len(jitter) {
			m += jitter[i]
		}
		ev = append(ev, p(m, text(i)), tool(m+0.3, fmt.Sprintf("f%d.go", i)), a(m+1))
	}
	return model.Session{ID: "s", Events: ev}
}

func varied(i int) string {
	return []string{
		"set up the project with a main loop and a window",
		"draw the player as a square and move it with the arrow keys",
		"why does the player slide forever after I let go",
		"add friction and cap the speed at 300 pixels a second",
		"put three platforms in and make the player land on them",
		"the player falls through the second platform at high speed",
		"add a coin counter in the top left",
		"make the coins spin",
	}[i%8]
}

func TestRegularTiming(t *testing.T) {
	cases := []struct {
		name string
		s    model.Session
		want bool
	}{
		{"prompts exactly every 4 minutes", steady(8, 4, nil, varied), true},
		{"a human rhythm", steady(8, 4, []float64{0, 2.5, -1, 3, 0.5, 2, 5, 9}, varied), false},
		{"too few prompts to tell", steady(4, 4, nil, varied), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Score(c.s, Default)
			if got := has(Anomalies(r, nil), "regular-timing"); got != c.want {
				t.Fatalf("regular-timing %v, want %v", got, c.want)
			}
		})
	}
}

func TestRepeatedPrompts(t *testing.T) {
	templated := func(i int) string { return fmt.Sprintf("Improve the game. Iteration %d. Make it better.", i) }
	cases := []struct {
		name string
		s    model.Session
		want bool
	}{
		{"templated prompt with a counter", steady(6, 3, []float64{0, 1, 3, 2, 5, 6}, templated), true},
		{"the same prompt pasted again and again", steady(5, 3, []float64{0, 1, 3, 2, 5}, func(int) string { return "fix all the bugs and add more features" }), true},
		{"bare go-aheads are autopilot, not templates", steady(6, 3, []float64{0, 1, 3, 2, 5, 6}, func(i int) string {
			if i == 0 {
				return "make a platformer"
			}
			return "continue"
		}), false},
		{"varied prompts", steady(8, 3, []float64{0, 1, 3, 2, 5, 6, 7, 8}, varied), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Score(c.s, Default)
			if got := has(Anomalies(r, nil), "repeated-prompts"); got != c.want {
				t.Fatalf("repeated-prompts %v, want %v", got, c.want)
			}
		})
	}
}

func TestPromptsWithoutInput(t *testing.T) {
	s := steady(6, 5, []float64{0, 1, 3, 2, 5, 1}, varied)
	prompts := []int{}
	for _, e := range s.Events {
		if e.Kind == model.Prompt {
			prompts = append(prompts, minuteIdx(e.At))
		}
	}
	all := fakeActivity{seenAll: true, active: rangeMins(-5, 60)}
	// Input everywhere except around the last three prompts.
	gaps := fakeActivity{seenAll: true, active: rangeMins(-5, 60)}
	for _, m := range prompts[3:] {
		delete(gaps.active, m)
		delete(gaps.active, m-1)
	}
	cases := []struct {
		name string
		act  Activity
		want bool
	}{
		{"input while every prompt was typed", all, false},
		{"three prompts arrived with no input", gaps, true},
		{"no activity data: nothing to compare", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := ScoreWith(s, Default, c.act)
			if got := has(Anomalies(r, c.act), "no-input-prompts"); got != c.want {
				t.Fatalf("no-input-prompts %v, want %v", got, c.want)
			}
		})
	}
}

func TestNoOutput(t *testing.T) {
	long := func(withFiles bool) model.Session {
		var ev []model.Event
		for i := 0; i < 10; i++ {
			m := float64(i) * 9
			ev = append(ev, p(m, varied(i)), a(m+3))
			if withFiles {
				ev = append(ev, tool(m+1, "game.js"))
			}
		}
		return model.Session{ID: "s", Events: ev}
	}
	commit := []model.Commit{{SHA: strings.Repeat("a", 40), At: at(30), Subject: "add coins"}}
	cases := []struct {
		name    string
		s       model.Session
		commits []model.Commit
		want    bool
	}{
		{"an hour and a half of chat, nothing written", long(false), nil, true},
		{"files changed", long(true), nil, false},
		{"a commit landed", long(false), commit, false},
		{"short session", steady(3, 4, nil, varied), nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Score(c.s, Default)
			r.Commits = c.commits
			if got := has(Anomalies(r, nil), "no-output"); got != c.want {
				t.Fatalf("no-output %v, want %v (wall %ds)", got, c.want, r.WallSec)
			}
		})
	}
}

func TestFlagsChangeNoCreditedSeconds(t *testing.T) {
	act := fakeActivity{seenAll: true, active: rangeMins(-5, 10)}
	sessions := []model.Session{
		steady(8, 4, nil, func(i int) string { return fmt.Sprintf("Improve the game. Iteration %d.", i) }),
		partialSession,
	}
	for _, s := range sessions {
		r := ScoreWith(s, Default, act)
		before := r
		before.Intervals = append([]model.Interval(nil), r.Intervals...)
		before.Turns = append([]model.Turn(nil), r.Turns...)
		Flag(&r, act)
		if len(r.Flags) <= len(before.Flags) && s.ID != "partial" {
			t.Fatalf("expected new flags on %s, got %v", s.ID, r.Flags)
		}
		if r.CreditedSec != before.CreditedSec || !reflect.DeepEqual(r.Intervals, before.Intervals) {
			t.Fatalf("flagging changed credited time on %s", s.ID)
		}
		for i := range r.Turns {
			if r.Turns[i].ThinkSec != before.Turns[i].ThinkSec || r.Turns[i].AgentCreditSec != before.Turns[i].AgentCreditSec {
				t.Fatalf("flagging changed turn %d credit", i)
			}
		}
		// Flagging twice adds nothing new.
		n := len(r.Flags)
		Flag(&r, act)
		if len(r.Flags) != n {
			t.Fatalf("flags duplicated: %v", r.Flags)
		}
	}
}
