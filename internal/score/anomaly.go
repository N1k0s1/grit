package score

import (
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/n1k0s1/grit/internal/model"
)

// Anomaly flags point a reviewer at sessions worth a closer look. They are
// signals, not verdicts: they never change credited time. A person decides.
//
//   - regular-timing: six or more prompts spaced almost exactly evenly
//     (the gaps vary by under 10%), the way a script sends them.
//   - repeated-prompts: the same prompt, or one template with only numbers
//     changed, three or more times and in at least 30% of prompts. Bare
//     go-aheads such as "continue" are left to the autopilot flag.
//   - no-input-prompts: with activity data, at least two prompts (and a
//     quarter of all prompts) arrived with no keyboard or mouse input in
//     their minute or the minute before.
//   - no-output: an hour or more of session with no file touched, no edit
//     seen on disk and no commit.
const (
	RegularMinPrompts = 6
	RegularMaxCV      = 0.1
	RepeatMin         = 3
	NoOutputWall      = 3600
)

// Anomalies returns the anomaly flags for a scored session. act may be nil.
// The server calls it on submitted bundles without activity data, so it
// works from what is in the Result alone.
func Anomalies(r model.Result, act Activity) []string {
	var out []string
	if regularTiming(r.Turns) {
		out = append(out, "regular-timing")
	}
	if repeatedPrompts(r.Turns) {
		out = append(out, "repeated-prompts")
	}
	if act != nil && r.Signal == "activity" && noInputPrompts(r.Turns, act) {
		out = append(out, "no-input-prompts")
	}
	if r.WallSec >= NoOutputWall && r.FilesTouched == 0 && len(r.Commits) == 0 && r.AgentEdits == 0 && r.HandEdits == 0 {
		out = append(out, "no-output")
	}
	return out
}

// Flag adds any anomaly flags the session does not already carry. It only
// touches Flags.
func Flag(r *model.Result, act Activity) {
	for _, f := range Anomalies(*r, act) {
		if !contains(r.Flags, f) {
			r.Flags = append(r.Flags, f)
		}
	}
}

func regularTiming(ts []model.Turn) bool {
	if len(ts) < RegularMinPrompts {
		return false
	}
	gaps := make([]float64, 0, len(ts)-1)
	sum := 0.0
	for i := 1; i < len(ts); i++ {
		g := ts[i].PromptAt.Sub(ts[i-1].PromptAt).Seconds()
		gaps = append(gaps, g)
		sum += g
	}
	mean := sum / float64(len(gaps))
	if mean <= 0 {
		return false
	}
	v := 0.0
	for _, g := range gaps {
		v += (g - mean) * (g - mean)
	}
	return math.Sqrt(v/float64(len(gaps)))/mean < RegularMaxCV
}

var (
	digits    = regexp.MustCompile(`[0-9]+`)
	nonLetter = regexp.MustCompile(`[^a-z#]+`)
)

func template(text string) string {
	t := digits.ReplaceAllString(strings.ToLower(text), "#")
	return strings.TrimSpace(nonLetter.ReplaceAllString(t, " "))
}

func repeatedPrompts(ts []model.Turn) bool {
	seen := map[string]int{}
	for _, t := range ts {
		if t.PromptText == "" || IsLowEffort(t.PromptText) {
			continue
		}
		k := template(t.PromptText)
		if len(k) < 12 {
			continue
		}
		seen[k]++
	}
	for _, n := range seen {
		if n >= RepeatMin && n*10 >= len(ts)*3 {
			return true
		}
	}
	return false
}

func noInputPrompts(ts []model.Turn, act Activity) bool {
	n := 0
	for _, t := range ts {
		if !act.Active(t.PromptAt) && !act.Active(t.PromptAt.Add(-time.Minute)) {
			n++
		}
	}
	return n >= 2 && n*4 >= len(ts)
}
