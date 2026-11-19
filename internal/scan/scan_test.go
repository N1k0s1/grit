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
