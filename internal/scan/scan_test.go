package scan

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/n1k0s1/grit/internal/activity"
	"github.com/n1k0s1/grit/internal/edits"
)

// TestHandAndAgentEdits runs the whole pipeline on synthetic data: a Claude
// Code transcript in a git repo, logged file changes, and activity minutes.
func TestHandAndAgentEdits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home := t.TempDir()
	repo := filepath.Join(home, "w", "game")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	base := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Minute)
	ts := func(m float64) string {
		return base.Add(time.Duration(m * float64(time.Minute))).Format(time.RFC3339Nano)
	}
	transcript := filepath.Join(home, ".claude", "projects", "-w-game", "s1.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"type":"user","sessionId":"s1","cwd":%q,"timestamp":%q,"origin":{"kind":"human"},"message":{"role":"user","content":"add a pause menu to the game"}}
{"type":"assistant","timestamp":%q,"message":{"model":"claude-opus-5-5","content":[{"type":"tool_use","name":"Edit","input":{"file_path":%q}}]}}
{"type":"assistant","timestamp":%q,"message":{"content":[{"type":"text","text":"done"}]}}
`, repo, ts(0), ts(1), filepath.Join(repo, "src", "pause.js"), ts(2))
	if err := os.WriteFile(transcript, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	at := func(m float64) time.Time { return base.Add(time.Duration(m * float64(time.Minute))) }
	// Like grit watch: a first scan names the roots to watch, and the
	// watcher logs changes under those roots.
	sc := New(home)
	if _, err := sc.Run(time.Now().Add(-24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(sc.Roots()) != 1 {
		t.Fatalf("roots %v", sc.Roots())
	}
	root := sc.Roots()[0]
	if filepath.Base(root) != "game" {
		t.Fatalf("root %s", root)
	}
	if err := edits.Append(edits.DefaultDir(home), []edits.Change{
		{At: at(1.5), Root: root, Path: "src/pause.js"}, // during the agent run
		{At: at(30.5), Root: root, Path: "src/menu.js"}, // by hand, with input
		{At: at(50), Root: root, Path: "README.md"},     // no input: unknown
	}); err != nil {
		t.Fatal(err)
	}
	log := activity.Log{Dir: activity.DefaultDir(home)}
	for m := 0.0; m <= 60; m++ {
		var last time.Time
		if m == 0 || m == 28 || m == 29 || m == 30 {
			last = at(m + 0.2)
		}
		if err := log.Record(at(m+0.5), last); err != nil {
			t.Fatal(err)
		}
	}

	rs, err := sc.Run(time.Now().Add(-24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var agentEdits, handEdits, handCredit int
	for _, r := range rs {
		agentEdits += r.AgentEdits
		handEdits += r.HandEdits
		if r.Harness == "manual" {
			handCredit += r.CreditedSec
		}
	}
	if agentEdits != 1 || handEdits != 1 {
		t.Fatalf("agent edits %d, hand edits %d, want 1 and 1", agentEdits, handEdits)
	}
	// Input at 28, 29 and 30 inside the 5m before the 30.5 edit: 2m30s.
	if handCredit != 150 {
		t.Fatalf("hand credit %ds, want 150", handCredit)
	}
}

func TestExcludedSessionsAreSkipped(t *testing.T) {
	home := t.TempDir()
	write := func(dir, id string) {
		path := filepath.Join(home, ".claude", "projects", dir, id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		ts := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
		body := fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":%q,"origin":{"kind":"human"},"message":{"role":"user","content":"make the menu"}}
`, id, filepath.Join(home, dir), ts)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("game", "keep")
	write("diary", "skip")
	if err := AddExclude(home, "~/diary"); err != nil {
		t.Fatal(err)
	}
	rs, err := New(home).Run(time.Now().Add(-24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].SessionID != "keep" {
		t.Fatalf("got %d results, want only the session outside the excluded folder", len(rs))
	}
}

func TestIncrementalMatchesFullParse(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude", "projects", "-w", "inc.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	line := func(m int, kind string) string {
		ts := base.Add(time.Duration(m) * time.Minute).Format(time.RFC3339)
		if kind == "user" {
			return fmt.Sprintf(`{"type":"user","sessionId":"inc","timestamp":%q,"origin":{"kind":"human"},"message":{"role":"user","content":"prompt number %d for the parser"}}`, ts, m) + "\n"
		}
		return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go test"}}]}}`, ts) + "\n"
	}
	appendTo := func(s string) {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString(s)
		f.Close()
	}
	sc := New(home)
	since := time.Now().Add(-24 * time.Hour)
	appendTo(line(0, "user") + line(1, "agent"))
	if _, err := sc.Run(since); err != nil {
		t.Fatal(err)
	}
	// A half-written line must wait until it is complete.
	half := line(5, "user")
	appendTo(line(3, "agent") + half[:20])
	r1, _ := sc.Run(since)
	if r1[0].Prompts != 1 {
		t.Fatalf("partial line was parsed: %d prompts", r1[0].Prompts)
	}
	appendTo(half[20:] + line(6, "agent"))
	got, _ := sc.Run(since)
	want, _ := New(home).Run(since)
	if len(got) != 1 || len(want) != 1 {
		t.Fatalf("got %d want %d results", len(got), len(want))
	}
	g, w := got[0], want[0]
	if g.Prompts != 2 || g.Prompts != w.Prompts || g.CreditedSec != w.CreditedSec || g.ToolCalls != w.ToolCalls || g.AgentSec != w.AgentSec {
		t.Fatalf("incremental %+v\nfull %+v", g, w)
	}
}

func TestChanged(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude", "projects", "-w", "c.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	ts := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	line := fmt.Sprintf(`{"type":"user","sessionId":"c","timestamp":%q,"origin":{"kind":"human"},"message":{"role":"user","content":"hello agent, start here"}}`+"\n", ts)
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	sc := New(home)
	if !sc.Changed() {
		t.Fatal("a never-scanned file is a change")
	}
	if _, err := sc.Run(time.Now().Add(-24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if sc.Changed() {
		t.Fatal("nothing changed since the run")
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(line)
	f.Close()
	if !sc.Changed() {
		t.Fatal("an appended line is a change")
	}
}
