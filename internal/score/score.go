// Package score turns a session's raw events into credited human time.
//
// The idea: an agent can run for hours on its own, so wall-clock time and
// agent time say nothing about how hard a person worked. What we can see in
// every harness is when the human spoke and when the agent stopped. Time is
// credited only where those timestamps show a person engaged:
//
//   - think:  the gap between the agent finishing and the human's next prompt,
//     if it is short (they were reading, testing, deciding). After a long
//     idle gap only a typing estimate for the prompt is credited.
//   - agent:  time spent supervising a run, only when the human came back
//     within a couple of minutes of it finishing (they were watching).
//   - review: a short tail after the final turn.
//
// Every credited second carries a rule string so a reviewer can see why.
package score

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/n1k0s1/grit/internal/model"
)

// Config holds the tunable rules. Defaults are deliberately conservative.
type Config struct {
	IdleTimeout  time.Duration // a reply within this gap counts as engaged
	ComposeCPS   float64       // chars per second used for the typing estimate
	ComposeMin   time.Duration
	ComposeMax   time.Duration
	LowEffortCap time.Duration // think credit cap for "continue", "yes", ...
	ReturnWindow time.Duration // came back this fast: the run was supervised
	AgentCap     time.Duration // max supervised credit per agent run
	Unsupervised time.Duration // credit for watching a run start when not supervised
	ReviewTail   time.Duration // credit after the last turn
}

// Default is the rule set used everywhere unless overridden.
var Default = Config{
	IdleTimeout:  10 * time.Minute,
	ComposeCPS:   3,
	ComposeMin:   20 * time.Second,
	ComposeMax:   4 * time.Minute,
	LowEffortCap: 90 * time.Second,
	ReturnWindow: 2 * time.Minute,
	AgentCap:     8 * time.Minute,
	Unsupervised: time.Minute,
	ReviewTail:   2 * time.Minute,
}

var (
	lowEffort = map[string]bool{
		"continue": true, "go": true, "go on": true, "yes": true, "y": true, "ok": true,
		"okay": true, "proceed": true, "keep going": true, "do it": true, "next": true,
		"sure": true, "carry on": true, "fix it": true, "yep": true, "yeah": true,
		"k": true, "please continue": true, "continue please": true, "go ahead": true,
		"looks good": true, "lgtm": true, "again": true, "try again": true, "more": true,
	}
	debugRe = regexp.MustCompile(`(?i)\b(error|errors|bug|broken|doesn'?t work|not working|fails?|failing|failed|crash(es|ed)?|exception|traceback|undefined|null|nan|why|wrong|stuck|panic|segfault|hangs?|jitter(s|y)?|glitch(es|y)?|flicker(s|ing)?|lag(s|gy)?|still)\b`)
	trimRe  = regexp.MustCompile(`[^a-z ]+`)
)

// IsLowEffort reports whether a prompt is a bare go-ahead.
func IsLowEffort(text string) bool {
	t := strings.TrimSpace(trimRe.ReplaceAllString(strings.ToLower(text), ""))
	return lowEffort[t] || len([]rune(strings.TrimSpace(text))) < 4
}

// IsDebugging reports whether a prompt looks like the human chasing a problem.
func IsDebugging(text string) bool { return debugRe.MatchString(text) }

func clamp(d, lo, hi time.Duration) time.Duration {
	if d < lo {
		return lo
	}
	if d > hi {
		return hi
	}
	return d
}

func mins(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%.0fm", d.Minutes())
}

// Activity reports, per minute, whether there was keyboard or mouse input.
// activity.Reader implements it. Seen says whether the sampler was running
// at all, so "no input" and "no data" stay different things.
type Activity interface {
	Seen(t time.Time) bool
	Active(t time.Time) bool
}

// MinCoverage is the share of a session's minutes the sampler must have seen
// for the activity rules (v2) to apply. Below it the session is scored with
// the v1 rules and carries no activity signal, which lowers its trust tier.
const MinCoverage = 0.5

// Score computes a Result for one session with the v1 rules (timestamps only).
func Score(s model.Session, c Config) model.Result { return ScoreWith(s, c, nil) }

// ScoreWith computes a Result, using activity data when there is enough of
// it. With activity (v2) the v1 windows still decide what could count, and
// inside them only minutes with input are credited:
//
//   - think: minutes with input inside the v1 think window;
//   - agent: a supervised run counts in full only if there was input during
//     it or just after it; otherwise only its first minute, where it had input;
//   - review: minutes with input in the tail after the last run.
func ScoreWith(s model.Session, c Config, act Activity) model.Result {
	ev := append([]model.Event(nil), s.Events...)
	sort.SliceStable(ev, func(i, j int) bool { return ev[i].At.Before(ev[j].At) })

	r := model.Result{
		SessionID: s.ID, Harness: s.Harness, Model: s.Model, Project: s.Project,
		Branch: s.GitBranch, Remote: s.GitRemote,
	}
	if len(ev) == 0 {
		return r
	}
	r.Start, r.End = ev[0].At, ev[len(ev)-1].At
	r.WallSec = int(r.End.Sub(r.Start).Seconds())

	// Split into turns at each prompt.
	var turns []model.Turn
	files := map[string]bool{}
	for _, e := range ev {
		switch e.Kind {
		case model.Prompt:
			turns = append(turns, model.Turn{
				Index: len(turns), PromptAt: e.At, AgentEnd: e.At,
				PromptChars: len([]rune(e.Text)), PromptText: e.Text,
			})
		case model.Agent, model.Tool:
			if len(turns) == 0 {
				continue // agent output before any prompt (resume noise)
			}
			t := &turns[len(turns)-1]
			if e.At.After(t.AgentEnd) {
				t.AgentEnd = e.At
			}
			if e.Kind == model.Tool {
				t.ToolCalls++
				r.ToolCalls++
				if e.File != "" && !contains(t.Files, e.File) {
					t.Files = append(t.Files, e.File)
				}
				if e.File != "" {
					files[e.File] = true
				}
				if e.Command != "" {
					t.Commands = append(t.Commands, e.Command)
				}
			}
		}
	}
	r.FilesTouched = len(files)
	r.Prompts = len(turns)

	v2 := act != nil && len(turns) > 0 && coverage(act, r.Start, r.End.Add(c.ReviewTail)) >= MinCoverage
	if v2 {
		r.Signal = "activity"
	}

	for i := range turns {
		t := &turns[i]
		low := IsLowEffort(t.PromptText)
		if low {
			t.Flags = append(t.Flags, "low-effort")
			r.LowEffortPrompts++
		}
		if IsDebugging(t.PromptText) {
			t.Flags = append(t.Flags, "debugging")
			r.DebugPrompts++
		}
		if t.PromptChars >= 200 {
			t.Flags = append(t.Flags, "specific")
			r.SpecificPrompts++
		}

		// Think credit: time before this prompt.
		compose := clamp(time.Duration(float64(t.PromptChars)/c.ComposeCPS*float64(time.Second)), c.ComposeMin, c.ComposeMax)
		var think time.Duration
		if i == 0 {
			think = compose
			t.ThinkRule = "first prompt: typing estimate (" + mins(compose) + ")"
		} else {
			gap := t.PromptAt.Sub(turns[i-1].AgentEnd)
			if gap <= c.IdleTimeout {
				think = gap
				t.ThinkRule = "replied " + mins(gap) + " after agent finished: engaged"
			} else {
				think = compose
				t.ThinkRule = "idle " + mins(gap) + " before prompt: typing estimate only"
			}
		}
		var thinkParts []model.Interval
		if v2 {
			thinkParts = activeParts(act, t.PromptAt.Add(-think), t.PromptAt)
			got := sumParts(thinkParts)
			if got == 0 {
				t.ThinkRule += ", but no keyboard or mouse input: not counted"
			} else if got < think {
				t.ThinkRule += ", input in " + mins(got) + " of it"
			} else {
				t.ThinkRule += ", input throughout"
			}
			if low && got > c.LowEffortCap {
				thinkParts = keepLast(thinkParts, c.LowEffortCap)
				t.ThinkRule += ", capped (low-effort prompt)"
			}
		} else {
			if low && think > c.LowEffortCap {
				think = c.LowEffortCap
				t.ThinkRule += ", capped (low-effort prompt)"
			}
			if think > 0 {
				thinkParts = []model.Interval{{Start: t.PromptAt.Add(-think), End: t.PromptAt}}
			}
		}
		t.ThinkSec = int(sumParts(thinkParts).Seconds())

		// Agent credit: supervising the run that followed.
		run := t.AgentEnd.Sub(t.PromptAt)
		t.AgentSec = int(run.Seconds())
		r.AgentSec += t.AgentSec
		var agent time.Duration
		supervised := false
		if i+1 < len(turns) {
			back := turns[i+1].PromptAt.Sub(t.AgentEnd)
			if back <= c.ReturnWindow {
				supervised = true
				agent = min(run, c.AgentCap)
				t.AgentRule = "came back within " + mins(back) + ": supervised"
				if run > c.AgentCap {
					t.AgentRule += ", capped at " + mins(c.AgentCap)
				}
			} else {
				agent = min(run, c.Unsupervised)
				t.AgentRule = "came back after " + mins(back) + ": only the first " + mins(c.Unsupervised) + " counts"
			}
		} else {
			agent = min(run, c.Unsupervised)
			t.AgentRule = "last run: first " + mins(c.Unsupervised) + " counts"
		}
		var agentParts []model.Interval
		switch {
		case agent <= 0:
		case !v2:
			agentParts = []model.Interval{{Start: t.PromptAt, End: t.PromptAt.Add(agent)}}
		case supervised && anyActive(act, t.PromptAt, t.AgentEnd.Add(c.ReturnWindow)):
			agentParts = []model.Interval{{Start: t.PromptAt, End: t.PromptAt.Add(agent)}}
			t.AgentRule += ", input during or just after the run"
		case supervised:
			t.AgentRule += ", but no keyboard or mouse input during or just after the run: not counted"
		default:
			agentParts = activeParts(act, t.PromptAt, t.PromptAt.Add(agent))
			if len(agentParts) == 0 {
				t.AgentRule += ", but no keyboard or mouse input then: not counted"
			} else {
				t.AgentRule += ", where there was input"
			}
		}
		t.AgentCreditSec = int(sumParts(agentParts).Seconds())

		// Intervals, in time order.
		r.Intervals = append(r.Intervals, label(thinkParts, "think", t.ThinkRule)...)
		r.Intervals = append(r.Intervals, label(agentParts, "agent", t.AgentRule)...)
		if i == len(turns)-1 {
			rule := "review tail: " + mins(c.ReviewTail) + " after the last run"
			tail := []model.Interval{{Start: t.AgentEnd, End: t.AgentEnd.Add(c.ReviewTail)}}
			if v2 {
				tail = activeParts(act, t.AgentEnd, t.AgentEnd.Add(c.ReviewTail))
				rule += ", where there was input"
			}
			r.Intervals = append(r.Intervals, label(tail, "review", rule)...)
		}
	}
	r.Intervals = mergeOwn(r.Intervals)
	// Session time covers the credited window too (the thinking before the
	// first prompt and the review tail), so focus can never pass 100%.
	if n := len(r.Intervals); n > 0 {
		lo, hi := r.Start, r.End
		if r.Intervals[0].Start.Before(lo) {
			lo = r.Intervals[0].Start
		}
		if r.Intervals[n-1].End.After(hi) {
			hi = r.Intervals[n-1].End
		}
		r.WallSec = int(hi.Sub(lo).Seconds())
	}
	r.Turns = turns
	r.CreditedSec = total(r.Intervals)
	r.Flags = sessionFlags(r)
	return r
}

// coverage is the share of minutes in [from, to) the sampler saw.
func coverage(a Activity, from, to time.Time) float64 {
	n, seen := 0, 0
	for m := from.Truncate(time.Minute); m.Before(to); m = m.Add(time.Minute) {
		n++
		if a.Seen(m) {
			seen++
		}
	}
	if n == 0 {
		return 0
	}
	return float64(seen) / float64(n)
}

// anyActive reports whether any minute overlapping [from, to] had input.
func anyActive(a Activity, from, to time.Time) bool {
	for m := from.Truncate(time.Minute); !m.After(to); m = m.Add(time.Minute) {
		if a.Active(m) {
			return true
		}
	}
	return false
}

// activeParts returns the parts of [from, to) that fall in minutes with
// input, merged into continuous stretches.
func activeParts(a Activity, from, to time.Time) []model.Interval {
	var out []model.Interval
	for m := from.Truncate(time.Minute); m.Before(to); m = m.Add(time.Minute) {
		if !a.Active(m) {
			continue
		}
		s, e := m, m.Add(time.Minute)
		if s.Before(from) {
			s = from
		}
		if e.After(to) {
			e = to
		}
		if n := len(out); n > 0 && out[n-1].End.Equal(s) {
			out[n-1].End = e
			continue
		}
		out = append(out, model.Interval{Start: s, End: e})
	}
	return out
}

func sumParts(ps []model.Interval) time.Duration {
	var d time.Duration
	for _, p := range ps {
		d += p.End.Sub(p.Start)
	}
	return d
}

// keepLast trims parts from the front until they total at most limit, so the
// time kept is the time closest to the prompt.
func keepLast(ps []model.Interval, limit time.Duration) []model.Interval {
	var out []model.Interval
	left := limit
	for i := len(ps) - 1; i >= 0 && left > 0; i-- {
		p := ps[i]
		if d := p.End.Sub(p.Start); d > left {
			p.Start = p.End.Add(-left)
		}
		left -= p.End.Sub(p.Start)
		out = append([]model.Interval{p}, out...)
	}
	return out
}

func label(ps []model.Interval, kind, rule string) []model.Interval {
	out := make([]model.Interval, 0, len(ps))
	for _, p := range ps {
		if p.End.After(p.Start) {
			p.Kind, p.Rule = kind, rule
			out = append(out, p)
		}
	}
	return out
}

// mergeOwn removes self-overlap inside one session (for example a think
// window reaching back before the previous run ended).
func mergeOwn(in []model.Interval) []model.Interval {
	sort.SliceStable(in, func(i, j int) bool { return in[i].Start.Before(in[j].Start) })
	var out []model.Interval
	for _, iv := range in {
		if n := len(out); n > 0 && iv.Start.Before(out[n-1].End) {
			if !iv.End.After(out[n-1].End) {
				continue
			}
			iv.Start = out[n-1].End
		}
		if iv.End.After(iv.Start) {
			out = append(out, iv)
		}
	}
	return out
}

func total(ivs []model.Interval) int {
	s := 0
	for _, iv := range ivs {
		s += iv.Seconds()
	}
	return s
}

func sessionFlags(r model.Result) []string {
	var f []string
	if r.Prompts >= 4 && r.LowEffortPrompts*2 >= r.Prompts {
		f = append(f, "autopilot")
	}
	if r.CreditedSec > 0 && r.AgentSec > 4*r.CreditedSec {
		f = append(f, "mostly-agent")
	}
	if r.WallSec > 3600 && r.CreditedSec*5 < r.WallSec {
		f = append(f, "long-idle")
	}
	return f
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// Dedupe removes time counted twice across sessions that ran at the same
// moment (two agents, two projects, one person). Earlier-starting intervals
// keep the overlap. It edits results in place and records the removed time.
func Dedupe(rs []*model.Result) {
	type ref struct {
		r  *model.Result
		iv model.Interval
	}
	var all []ref
	for _, r := range rs {
		for _, iv := range r.Intervals {
			all = append(all, ref{r, iv})
		}
		r.Intervals = nil
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].iv.Start.Before(all[j].iv.Start) })
	var frontier time.Time
	for _, a := range all {
		iv := a.iv
		if iv.Start.Before(frontier) {
			if !iv.End.After(frontier) {
				a.r.OverlapSec += iv.Seconds()
				continue
			}
			a.r.OverlapSec += int(frontier.Sub(iv.Start).Seconds())
			iv.Start = frontier
		}
		a.r.Intervals = append(a.r.Intervals, iv)
		if iv.End.After(frontier) {
			frontier = iv.End
		}
	}
	for _, r := range rs {
		r.CreditedSec = total(r.Intervals)
	}
}
