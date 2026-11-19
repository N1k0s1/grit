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

// Score computes a Result for one session.
func Score(s model.Session, c Config) model.Result {
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
		if low && think > c.LowEffortCap {
			think = c.LowEffortCap
			t.ThinkRule += ", capped (low-effort prompt)"
		}
		t.ThinkSec = int(think.Seconds())

		// Agent credit: supervising the run that followed.
		run := t.AgentEnd.Sub(t.PromptAt)
		t.AgentSec = int(run.Seconds())
		r.AgentSec += t.AgentSec
		var agent time.Duration
		if i+1 < len(turns) {
			back := turns[i+1].PromptAt.Sub(t.AgentEnd)
			if back <= c.ReturnWindow {
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
		t.AgentCreditSec = int(agent.Seconds())

		// Intervals, in time order.
		if think > 0 {
			r.Intervals = append(r.Intervals, model.Interval{Start: t.PromptAt.Add(-think), End: t.PromptAt, Kind: "think"})
		}
		if agent > 0 {
			r.Intervals = append(r.Intervals, model.Interval{Start: t.PromptAt, End: t.PromptAt.Add(agent), Kind: "agent"})
		}
		if i == len(turns)-1 {
			r.Intervals = append(r.Intervals, model.Interval{Start: t.AgentEnd, End: t.AgentEnd.Add(c.ReviewTail), Kind: "review"})
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
