package edits

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"time"

	"github.com/n1k0s1/grit/internal/model"
)

// Who made a change.
const (
	Agent   = "agent"
	Human   = "human"
	Unknown = "unknown"
)

// Rules for attribution and credit.
const (
	RunGrace     = time.Minute      // writes this soon after a run ends are the agent's
	ToolLag      = 3 * time.Minute  // a write to a file the agent touched this recently is the agent's
	HandLookback = 5 * time.Minute  // minutes with input this long before a hand edit count
	HandGap      = 30 * time.Minute // a gap this long between hand edits starts a new session
)

// Activity is per-minute keyboard and mouse data (score.Activity).
type Activity interface {
	Seen(t time.Time) bool
	Active(t time.Time) bool
}

// Tool is an agent tool call that named a file.
type Tool struct {
	At   time.Time
	Path string // slash path relative to the run's Root
}

// Run is one agent turn in a project: from the prompt to the agent finishing.
type Run struct {
	Session string
	Root    string
	Start   time.Time
	End     time.Time
	Tools   []Tool
}

// Classify decides who made one change. It leans towards the agent: any
// agent activity in the same project at that moment wins, so a person
// typing alongside a running agent is never credited for the agent's work.
func Classify(c Change, runs []Run, act Activity) string {
	if r := agentRun(c, runs); r != nil {
		return Agent
	}
	if act == nil || !act.Seen(c.At) || !act.Active(c.At) {
		return Unknown
	}
	return Human
}

func agentRun(c Change, runs []Run) *Run {
	for i := range runs {
		r := &runs[i]
		if r.Root != c.Root {
			continue
		}
		if !c.At.Before(r.Start) && !c.At.After(r.End.Add(RunGrace)) {
			return r
		}
		for _, t := range r.Tools {
			if t.Path == c.Path && !c.At.Before(t.At) && !c.At.After(t.At.Add(ToolLag)) {
				return r
			}
		}
	}
	return nil
}

// Counts is how many changes went each way, plus agent changes per session.
type Counts struct {
	Human, Agent, Unknown int
	BySession             map[string]int
}

// HandSessions classifies changes and turns the hand-written ones into
// "manual" sessions. Each hand edit credits the minutes with input in the
// HandLookback before it. Edits less than HandGap apart share a session.
// projectOf names the project for a root.
func HandSessions(changes []Change, runs []Run, act Activity, projectOf func(root string) string) ([]model.Result, Counts) {
	counts := Counts{BySession: map[string]int{}}
	byRoot := map[string][]Change{}
	for _, c := range changes {
		if r := agentRun(c, runs); r != nil {
			counts.Agent++
			counts.BySession[r.Session]++
			continue
		}
		if Classify(c, nil, act) != Human {
			counts.Unknown++
			continue
		}
		counts.Human++
		byRoot[c.Root] = append(byRoot[c.Root], c)
	}
	var out []model.Result
	roots := make([]string, 0, len(byRoot))
	for r := range byRoot {
		roots = append(roots, r)
	}
	sort.Strings(roots)
	for _, root := range roots {
		cs := byRoot[root]
		sort.Slice(cs, func(i, j int) bool { return cs[i].At.Before(cs[j].At) })
		start := 0
		for i := 1; i <= len(cs); i++ {
			if i == len(cs) || cs[i].At.Sub(cs[i-1].At) > HandGap {
				out = append(out, handResult(root, projectOf(root), cs[start:i], act))
				start = i
			}
		}
	}
	return out, counts
}

const handRule = "hand edit with no agent run or tool call nearby: minutes with keyboard or mouse input in the 5m before it"

func handResult(root, project string, cs []Change, act Activity) model.Result {
	h := sha256.Sum256([]byte(root))
	r := model.Result{
		SessionID: fmt.Sprintf("hand-%s-%d", hex.EncodeToString(h[:4]), cs[0].At.Unix()),
		Harness:   "manual",
		Project:   project,
		Signal:    "activity",
		HandEdits: len(cs),
	}
	var ivs []model.Interval
	for _, c := range cs {
		from := c.At.Add(-HandLookback)
		for m := from.Truncate(time.Minute); m.Before(c.At); m = m.Add(time.Minute) {
			if !act.Active(m) {
				continue
			}
			s, e := m, m.Add(time.Minute)
			if s.Before(from) {
				s = from
			}
			if e.After(c.At) {
				e = c.At
			}
			ivs = append(ivs, model.Interval{Start: s, End: e, Kind: "hand", Rule: handRule})
		}
	}
	ivs = merge(ivs)
	r.Intervals = ivs
	if len(ivs) > 0 {
		r.Start, r.End = ivs[0].Start, ivs[len(ivs)-1].End
	} else {
		r.Start, r.End = cs[0].At, cs[len(cs)-1].At
	}
	r.WallSec = int(r.End.Sub(r.Start).Seconds())
	for _, iv := range ivs {
		r.CreditedSec += iv.Seconds()
	}
	return r
}

// merge sorts intervals and joins overlapping or touching ones.
func merge(in []model.Interval) []model.Interval {
	sort.Slice(in, func(i, j int) bool { return in[i].Start.Before(in[j].Start) })
	var out []model.Interval
	for _, iv := range in {
		if n := len(out); n > 0 && !iv.Start.After(out[n-1].End) {
			if iv.End.After(out[n-1].End) {
				out[n-1].End = iv.End
			}
			continue
		}
		out = append(out, iv)
	}
	return out
}
