package score

import (
	"testing"
	"time"

	"github.com/n1k0s1/grit/internal/model"
)

var t0 = time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)

func at(m float64) time.Time { return t0.Add(time.Duration(m * float64(time.Minute))) }

func p(m float64, text string) model.Event { return model.Event{At: at(m), Kind: model.Prompt, Text: text} }
func a(m float64) model.Event              { return model.Event{At: at(m), Kind: model.Agent} }
func tool(m float64, f string) model.Event {
	return model.Event{At: at(m), Kind: model.Tool, Tool: "Edit", File: f}
}

func TestEngagedSessionCreditsMostTime(t *testing.T) {
	s := model.Session{ID: "flow", Events: []model.Event{
		p(0, "build a websocket server that broadcasts player positions at 20hz, keep it in one file"),
		tool(0.5, "server.go"), a(2),
		p(4, "the client jitters when two players overlap, why?"),
		tool(4.5, "client.js"), a(5),
		p(7, "still jitters, the interpolation buffer is too small I think"),
		a(8),
	}}
	r := Score(s, Default)
	if r.Prompts != 3 || r.DebugPrompts != 2 {
		t.Fatalf("prompts=%d debug=%d", r.Prompts, r.DebugPrompts)
	}
	// wall is 8 min; nearly all should be credited (plus a 2m review tail).
	if r.CreditedSec < 8*60 {
		t.Fatalf("credited %ds, want at least 8m for an engaged session", r.CreditedSec)
	}
	if len(r.Flags) != 0 {
		t.Fatalf("unexpected flags %v", r.Flags)
	}
}

func TestAutopilotSessionIsDeflated(t *testing.T) {
	var ev []model.Event
	ev = append(ev, p(0, "make me a counter strike clone"))
	for i := 1; i <= 8; i++ {
		start := float64(i-1) * 30
		ev = append(ev, tool(start+1, "game.js"), a(start+25))
		ev = append(ev, p(float64(i)*30, "continue"))
	}
	ev = append(ev, a(8*30+25))
	r := Score(model.Session{ID: "afk", Events: ev}, Default)
	if r.WallSec < 4*3600 {
		t.Fatalf("wall %d", r.WallSec)
	}
	if r.CreditedSec > 30*60 {
		t.Fatalf("credited %ds for an afk session, want under 30m", r.CreditedSec)
	}
	want := map[string]bool{"autopilot": true, "mostly-agent": true, "long-idle": true}
	for _, f := range r.Flags {
		delete(want, f)
	}
	if len(want) != 0 {
		t.Fatalf("missing flags %v, got %v", want, r.Flags)
	}
}

func TestDedupeAcrossParallelSessions(t *testing.T) {
	a1 := Score(model.Session{ID: "a", Events: []model.Event{p(0, "write the parser for the save file format please"), a(3), p(4, "now add tests for it"), a(6)}}, Default)
	b1 := Score(model.Session{ID: "b", Events: []model.Event{p(1, "fix the css on the landing page header"), a(2), p(3, "and the footer too please"), a(5)}}, Default)
	sum := a1.CreditedSec + b1.CreditedSec
	rs := []*model.Result{&a1, &b1}
	Dedupe(rs)
	after := a1.CreditedSec + b1.CreditedSec
	if after >= sum {
		t.Fatalf("dedupe removed nothing: %d -> %d", sum, after)
	}
	if a1.OverlapSec+b1.OverlapSec != sum-after {
		t.Fatalf("overlap accounting off: %d+%d vs %d", a1.OverlapSec, b1.OverlapSec, sum-after)
	}
	// Union of time can never exceed the span from first to last credited second.
	span := 0.0
	for _, r := range rs {
		for _, iv := range r.Intervals {
			span = max(span, iv.End.Sub(t0).Seconds())
		}
	}
	if float64(after) > span+60 {
		t.Fatalf("credited %d exceeds span %v", after, span)
	}
}

func TestLowEffort(t *testing.T) {
	for _, s := range []string{"continue", "Continue.", "yes", "ok!", "go ahead", "y"} {
		if !IsLowEffort(s) {
			t.Errorf("%q should be low effort", s)
		}
	}
	for _, s := range []string{"continue but use a hashmap", "yes, and rename it to Player"} {
		if IsLowEffort(s) {
			t.Errorf("%q should not be low effort", s)
		}
	}
}

func TestFocusNeverExceedsHundred(t *testing.T) {
	r := Score(model.Session{ID: "short", Events: []model.Event{p(0, "a long and careful first prompt describing the whole feature in detail"), a(0.5)}}, Default)
	if r.CreditedSec > r.WallSec {
		t.Fatalf("credited %d > wall %d", r.CreditedSec, r.WallSec)
	}
}
