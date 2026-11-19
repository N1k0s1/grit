// Package demo seeds a server with generated example builders so the
// dashboard has something to show. Every number is produced by the real
// scoring engine from generated event streams; only the events are fake.
package demo

import (
	"encoding/hex"
	"fmt"
	"math/rand"
	"time"

	"github.com/n1k0s1/grit/internal/attest"
	"github.com/n1k0s1/grit/internal/model"
	"github.com/n1k0s1/grit/internal/score"
	"github.com/n1k0s1/grit/internal/store"
)

type persona struct {
	name     string
	projects []string
	harness  []string
	days     float64 // chance of working on a given day
	sessions [2]int  // sessions per active day, min and max
	turns    [2]int
	replyGap [2]int // seconds between agent finishing and next prompt
	runLen   [2]int // seconds per agent run
	lazy     float64
	parallel bool
	submit   string
	reviewed string
	activity bool // runs the activity sampler: scored with keyboard and mouse data
	live     bool // syncs as they work: the server witnesses each session
}

var people = []persona{
	{name: "ava", projects: []string{"voxel-racer", "chem-flashcards"}, harness: []string{"claude-code", "codex"}, days: 0.85, sessions: [2]int{1, 3}, turns: [2]int{8, 22}, replyGap: [2]int{20, 420}, runLen: [2]int{30, 240}, lazy: 0.05, submit: "voxel-racer", activity: true, live: true},
	{name: "sam", projects: []string{"ai-trading-bot"}, harness: []string{"claude-code"}, days: 0.7, sessions: [2]int{1, 2}, turns: [2]int{6, 12}, replyGap: [2]int{900, 2400}, runLen: [2]int{300, 1500}, lazy: 0.75, submit: "ai-trading-bot"},
	{name: "lee", projects: []string{"discord-study-bot", "portfolio-site"}, harness: []string{"codex", "hook:cursor"}, days: 0.6, sessions: [2]int{1, 2}, turns: [2]int{6, 16}, replyGap: [2]int{30, 500}, runLen: [2]int{20, 200}, lazy: 0.1, parallel: true, live: true},
	{name: "mia", projects: []string{"rp2040-synth"}, harness: []string{"claude-code"}, days: 0.5, sessions: [2]int{1, 2}, turns: [2]int{2, 6}, replyGap: [2]int{20, 240}, runLen: [2]int{20, 120}, lazy: 0.05, reviewed: "rp2040-synth", activity: true, live: true},
}

var good = []string{
	"the car clips through the ramp at high speed, I think the collision step is too coarse",
	"add a ghost replay of the best lap, store inputs not positions",
	"why does the leaderboard request 500 when the name has an emoji?",
	"split the physics into its own module and write two tests for the friction curve",
	"the flashcard spacing algorithm repeats cards too soon, compare it to SM-2 and fix it",
	"make the mobile layout work under 400px, the buttons overlap",
	"still broken: the websocket reconnect loop never backs off",
	"rename Player to Racer everywhere and update the README",
	"read the error in the console and fix the null check in loadDeck",
	"add a settings screen for sensitivity, save it to localStorage",
	"the audio crackles on the pico when the envelope releases, check the buffer size",
	"draw the filter cutoff on the OLED as a bar, refresh at 30fps max",
}
var lazy = []string{"continue", "yes", "ok", "keep going", "do it", "continue"}
var subjects = []string{"fix collision step", "add ghost replay", "tests for friction curve", "mobile layout fixes", "settings screen", "reconnect backoff", "rename Player to Racer", "null check in loadDeck", "envelope release buffer", "OLED cutoff bar"}

// busy is activity data for a builder who was at the keyboard throughout.
type busy struct{}

func (busy) Seen(time.Time) bool   { return true }
func (busy) Active(time.Time) bool { return true }

func scoreFor(p persona, s model.Session) model.Result {
	if p.activity {
		return score.ScoreWith(s, score.Default, busy{})
	}
	return score.Score(s, score.Default)
}

// attestDemo adds the attestation chain the real client would compute.
func attestDemo(r *model.Result) {
	salt := attest.Salt([]byte("grit demo device"), r.SessionID)
	for i, h := range attest.Chain(r.SessionID, salt, r.Turns) {
		r.Turns[i].Link = h
	}
	r.Salt = hex.EncodeToString(salt)
}

func randIn(r *rand.Rand, a [2]int) int { return a[0] + r.Intn(a[1]-a[0]+1) }

func session(r *rand.Rand, p persona, project, harness string, start time.Time) model.Session {
	s := model.Session{ID: fmt.Sprintf("demo-%s-%d", p.name, start.Unix()), Harness: harness, Project: project, GitBranch: "main"}
	if harness == "codex" {
		s.Model = "gpt-6-codex"
	} else {
		s.Model = []string{"claude-sonnet-5-5", "claude-opus-5-5"}[r.Intn(2)]
	}
	t := start
	n := randIn(r, p.turns)
	for i := 0; i < n; i++ {
		text := good[r.Intn(len(good))]
		if i > 0 && r.Float64() < p.lazy {
			text = lazy[r.Intn(len(lazy))]
		}
		s.Events = append(s.Events, model.Event{At: t, Kind: model.Prompt, Text: text})
		run := time.Duration(randIn(r, p.runLen)) * time.Second
		steps := 1 + r.Intn(4)
		for k := 1; k <= steps; k++ {
			s.Events = append(s.Events, model.Event{At: t.Add(run * time.Duration(k) / time.Duration(steps)), Kind: model.Tool, Tool: "Edit", File: fmt.Sprintf("src/file%d.js", r.Intn(6))})
		}
		t = t.Add(run)
		s.Events = append(s.Events, model.Event{At: t, Kind: model.Agent})
		t = t.Add(time.Duration(randIn(r, p.replyGap)) * time.Second)
	}
	return s
}

// Seed fills the store. It is a no-op if the demo users already exist.
func Seed(st *store.Store) error {
	r := rand.New(rand.NewSource(42))
	now := time.Now()
	for _, p := range people {
		if _, err := st.UserByName(p.name); err == nil {
			continue
		}
		if _, err := st.CreateUser(p.name); err != nil {
			return err
		}
		u, err := st.UserByName(p.name)
		if err != nil {
			return err
		}
		var results []*model.Result
		for d := 13; d >= 0; d-- {
			if r.Float64() > p.days && d != 0 {
				continue
			}
			day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local).AddDate(0, 0, -d)
			for k := 0; k < randIn(r, p.sessions); k++ {
				start := day.Add(time.Duration(7+r.Intn(14))*time.Hour + time.Duration(r.Intn(60))*time.Minute)
				if start.After(now.Add(-90 * time.Minute)) {
					start = now.Add(-time.Duration(90+r.Intn(120)) * time.Minute)
				}
				proj := p.projects[r.Intn(len(p.projects))]
				res := scoreFor(p, session(r, p, proj, p.harness[r.Intn(len(p.harness))], start))
				if p.parallel && len(p.projects) > 1 && r.Float64() < 0.5 {
					other := p.projects[(indexOf(p.projects, proj)+1)%len(p.projects)]
					par := scoreFor(p, session(r, p, other, p.harness[r.Intn(len(p.harness))], start.Add(time.Duration(r.Intn(600))*time.Second)))
					results = append(results, &par)
				}
				results = append(results, &res)
			}
		}
		score.Dedupe(results)
		var pub []model.Result
		byProject := map[string][]model.Result{}
		for _, x := range results {
			if r.Float64() < 0.6 {
				for c := 0; c < 1+r.Intn(3); c++ {
					x.Commits = append(x.Commits, model.Commit{
						SHA: fmt.Sprintf("%040x", r.Uint64())[:40], At: x.End.Add(time.Duration(r.Intn(600)) * time.Second),
						Subject: subjects[r.Intn(len(subjects))], Additions: 5 + r.Intn(200), Deletions: r.Intn(60),
					})
				}
			}
			score.Flag(x, nil)
			attestDemo(x)
			pub = append(pub, x.Public())
			byProject[x.Project] = append(byProject[x.Project], *x)
		}
		for _, x := range pub {
			// A live builder's client syncs every 30 seconds, so the server
			// sees each turn moments after its prompt. Others sync later.
			if p.live {
				for k := 1; k < len(x.Turns); k++ {
					part := x
					part.Turns = x.Turns[:k]
					seen := x.Turns[k-1].PromptAt.Add(30 * time.Second)
					st.Now = func() time.Time { return seen }
					if err := st.UpsertSessions(u.ID, []model.Result{part}); err != nil {
						return err
					}
				}
				last := x.Turns[len(x.Turns)-1].PromptAt.Add(30 * time.Second)
				st.Now = func() time.Time { return last }
			} else {
				st.Now = time.Now
			}
			if err := st.UpsertSessions(u.ID, []model.Result{x}); err != nil {
				return err
			}
		}
		st.Now = time.Now
		for _, proj := range []string{p.submit, p.reviewed} {
			if proj == "" {
				continue
			}
			id, err := st.CreateSubmission(u.ID, store.Bundle{Project: proj, Note: "Two weeks of work, playable build in the repo.", Sessions: byProject[proj]})
			if err != nil {
				return err
			}
			if proj == p.reviewed {
				total := 0
				for _, x := range byProject[proj] {
					total += x.CreditedSec
				}
				if err := st.Review(id, "approved", total, "Prompts build on each other and match the commits."); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func indexOf(xs []string, x string) int {
	for i, y := range xs {
		if y == x {
			return i
		}
	}
	return 0
}
