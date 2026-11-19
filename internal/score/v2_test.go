package score

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/n1k0s1/grit/internal/model"
)

// fakeActivity answers per minute from sets of minute offsets from t0.
// seenAll marks every minute as sampled.
type fakeActivity struct {
	seenAll bool
	seen    map[int]bool
	active  map[int]bool
}

func minuteIdx(t time.Time) int { return int(math.Floor(t.Sub(t0).Minutes())) }

func (f fakeActivity) Seen(t time.Time) bool   { return f.seenAll || f.seen[minuteIdx(t)] }
func (f fakeActivity) Active(t time.Time) bool { return f.active[minuteIdx(t)] }

func mins_(ms ...int) map[int]bool {
	m := map[int]bool{}
	for _, x := range ms {
		m[x] = true
	}
	return m
}

func rangeMins(from, to int) map[int]bool {
	m := map[int]bool{}
	for i := from; i < to; i++ {
		m[i] = true
	}
	return m
}

// A 90 character first prompt (30s typing estimate), a run that ends at
// 2m, a reply at 6m (4m gap, so the first run is unsupervised), a last run
// ending at 7m. v1 credits 30s + 60s + 240s + 60s + 120s review = 510s.
var partialSession = model.Session{ID: "partial", Events: []model.Event{
	p(0, strings.Repeat("a", 90)),
	tool(0.5, "main.go"), a(2),
	p(6, "now make the score counter persist between rounds please"),
	a(7),
}}

func TestScoreV2(t *testing.T) {
	engaged := model.Session{ID: "flow", Events: []model.Event{
		p(0, "build a websocket server that broadcasts player positions at 20hz, keep it in one file"),
		tool(0.5, "server.go"), a(2),
		p(4, "the client jitters when two players overlap, why?"),
		tool(4.5, "client.js"), a(5),
		p(7, "still jitters, the interpolation buffer is too small I think"),
		a(8),
	}}
	v1Engaged := Score(engaged, Default).CreditedSec
	v1Partial := Score(partialSession, Default).CreditedSec
	if v1Partial != 510 {
		t.Fatalf("partial session v1 credit %d, want 510 (test setup)", v1Partial)
	}

	cases := []struct {
		name     string
		s        model.Session
		act      Activity
		credited int
		signal   string
	}{
		{"no data: v1 rules", engaged, nil, v1Engaged, ""},
		{"sampler never ran: v1 rules", engaged, fakeActivity{}, v1Engaged, ""},
		{"sampler ran for under half the session: v1 rules", engaged, fakeActivity{seen: rangeMins(-1, 2), active: rangeMins(-1, 12)}, v1Engaged, ""},
		{"input every minute: same as v1", engaged, fakeActivity{seenAll: true, active: rangeMins(-5, 15)}, v1Engaged, "activity"},
		{"no input at all: nothing credited", engaged, fakeActivity{seenAll: true}, 0, "activity"},
		// Input at 08:59 (typing the first prompt), 09:03 and 09:04 (reading
		// before the reply) and 09:06 (typing the reply): 30s + 0 + 120s + 60s
		// for the last run's first minute, and no review tail.
		{"partial input: only minutes with input", partialSession, fakeActivity{seenAll: true, active: mins_(-1, 3, 4, 6)}, 210, "activity"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := ScoreWith(c.s, Default, c.act)
			if r.CreditedSec != c.credited {
				t.Errorf("credited %ds, want %ds", r.CreditedSec, c.credited)
				for _, iv := range r.Intervals {
					t.Logf("  %s %s-%s %s", iv.Kind, iv.Start.Format("15:04:05"), iv.End.Format("15:04:05"), iv.Rule)
				}
			}
			if r.Signal != c.signal {
				t.Errorf("signal %q, want %q", r.Signal, c.signal)
			}
			assertRules(t, r)
		})
	}
}

// assertRules checks every credited second carries a rule string and that
// the turn totals match the intervals.
func assertRules(t *testing.T, r model.Result) {
	t.Helper()
	sum := 0
	for _, iv := range r.Intervals {
		if iv.Rule == "" {
			t.Errorf("interval %s %s-%s has no rule", iv.Kind, iv.Start.Format("15:04:05"), iv.End.Format("15:04:05"))
		}
		sum += iv.Seconds()
	}
	if sum != r.CreditedSec {
		t.Errorf("intervals sum to %d, credited %d", sum, r.CreditedSec)
	}
	for _, tn := range r.Turns {
		if tn.ThinkRule == "" || tn.AgentRule == "" {
			t.Errorf("turn %d missing a rule: %+v", tn.Index, tn)
		}
	}
}

func TestV1IntervalsCarryRules(t *testing.T) {
	assertRules(t, Score(partialSession, Default))
}

func TestV2SupervisedRunNeedsInput(t *testing.T) {
	// Prompt at 0, run to 5m, reply at 6m (came back within 2m: supervised
	// under v1, 5m of agent credit). With input only while typing the
	// first prompt, the reply looks scripted and the run earns nothing.
	s := model.Session{ID: "sup", Events: []model.Event{
		p(0, strings.Repeat("b", 60)), a(5),
		p(6, strings.Repeat("c", 60)), a(6.5),
	}}
	v1 := Score(s, Default)
	if v1.Turns[0].AgentCreditSec != 300 {
		t.Fatalf("v1 agent credit %d", v1.Turns[0].AgentCreditSec)
	}
	none := ScoreWith(s, Default, fakeActivity{seenAll: true, active: mins_(-1)})
	if got := none.Turns[0].AgentCreditSec; got != 0 {
		t.Fatalf("agent credit with no input during or after the run: %d, want 0", got)
	}
	if !strings.Contains(none.Turns[0].AgentRule, "no keyboard or mouse input") {
		t.Fatalf("rule should say why: %q", none.Turns[0].AgentRule)
	}
	// Input while watching (minute 3) restores full supervised credit.
	watched := ScoreWith(s, Default, fakeActivity{seenAll: true, active: mins_(-1, 3, 5)})
	if got := watched.Turns[0].AgentCreditSec; got != 300 {
		t.Fatalf("supervised credit with input: %d, want 300", got)
	}
}

func TestV2LowEffortCapKeepsLatestInput(t *testing.T) {
	// "continue" after a 5m gap with input throughout: capped at 90s, the
	// 90s right before the prompt.
	s := model.Session{ID: "low", Events: []model.Event{
		p(0, strings.Repeat("d", 60)), a(1),
		p(6, "continue"), a(7),
	}}
	r := ScoreWith(s, Default, fakeActivity{seenAll: true, active: rangeMins(-2, 12)})
	tn := r.Turns[1]
	if tn.ThinkSec != 90 || !strings.Contains(tn.ThinkRule, "capped") {
		t.Fatalf("think %d rule %q", tn.ThinkSec, tn.ThinkRule)
	}
	for _, iv := range r.Intervals {
		if iv.Kind == "think" && iv.End.Equal(at(6)) && !iv.Start.Equal(at(4.5)) {
			t.Fatalf("capped think should be the last 90s, got %s", iv.Start.Format("15:04:05"))
		}
	}
}

func TestDedupeKeepsRules(t *testing.T) {
	x := ScoreWith(partialSession, Default, fakeActivity{seenAll: true, active: rangeMins(-2, 12)})
	y := ScoreWith(partialSession, Default, fakeActivity{seenAll: true, active: rangeMins(-2, 12)})
	y.SessionID = "copy"
	Dedupe([]*model.Result{&x, &y})
	assertRules(t, x)
	assertRules(t, y)
	if y.CreditedSec != 0 || x.CreditedSec == 0 {
		t.Fatalf("x %d y %d", x.CreditedSec, y.CreditedSec)
	}
}
