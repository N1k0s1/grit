package edits

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)

func at(m float64) time.Time { return t0.Add(time.Duration(m * float64(time.Minute))) }

func put(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestPollDetectsChanges(t *testing.T) {
	root := t.TempDir()
	put(t, filepath.Join(root, "main.go"), at(-60))
	put(t, filepath.Join(root, "src", "game.js"), at(-60))
	put(t, filepath.Join(root, "node_modules", "lib", "index.js"), at(-60))

	w := &Watcher{}
	w.SetRoots([]string{root})
	if got := w.Poll(); len(got) != 0 {
		t.Fatalf("first poll is a baseline, got %v", got)
	}
	put(t, filepath.Join(root, "src", "game.js"), at(3))
	put(t, filepath.Join(root, "src", "new.js"), at(4))
	put(t, filepath.Join(root, "node_modules", "lib", "index.js"), at(5))
	put(t, filepath.Join(root, ".git", "index"), at(5))
	put(t, filepath.Join(root, "dist", "bundle.js"), at(5))

	got := w.Poll()
	if len(got) != 2 {
		t.Fatalf("want 2 changes, got %+v", got)
	}
	want := map[string]time.Time{"src/game.js": at(3), "src/new.js": at(4)}
	for _, c := range got {
		if c.Root != root || !c.At.Equal(want[c.Path]) {
			t.Errorf("unexpected change %+v", c)
		}
	}
	if again := w.Poll(); len(again) != 0 {
		t.Fatalf("no new changes expected, got %v", again)
	}
}

func TestPollStaysUnderRAMBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("creates 20,000 files")
	}
	root := t.TempDir()
	const files = 20000
	for i := 0; i < files; i++ {
		dir := filepath.Join(root, fmt.Sprintf("pkg%03d", i/100))
		if i%100 == 0 {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file_with_a_longish_name_%05d.go", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	w := &Watcher{}
	w.SetRoots([]string{root})
	w.Poll()
	w.Poll()
	runtime.GC()
	runtime.ReadMemStats(&after)
	if w.Files() != files {
		t.Fatalf("tracked %d files, want %d", w.Files(), files)
	}
	grew := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	const budget = 4 << 20 // 4 MB for 20,000 files
	t.Logf("heap grew %.2f MB for %d files", float64(grew)/(1<<20), files)
	if grew > budget {
		t.Fatalf("heap grew %d bytes, budget %d", grew, budget)
	}
	runtime.KeepAlive(w)
}

func TestPollSkipsHugeRoots(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 30; i++ {
		put(t, filepath.Join(root, fmt.Sprintf("f%d.txt", i)), at(-60))
	}
	w := &Watcher{MaxFiles: 10}
	w.SetRoots([]string{root})
	w.Poll()
	put(t, filepath.Join(root, "f1.txt"), at(1))
	if got := w.Poll(); len(got) != 0 || w.Files() != 0 {
		t.Fatalf("a root over MaxFiles must be dropped, got %v and %d files", got, w.Files())
	}
}

type fakeAct struct {
	seenAll bool
	active  map[int]bool
}

func (f fakeAct) idx(t time.Time) int     { return int(math.Floor(t.Sub(t0).Minutes())) }
func (f fakeAct) Seen(t time.Time) bool   { return f.seenAll }
func (f fakeAct) Active(t time.Time) bool { return f.active[f.idx(t)] }

func minutes(from, to int) map[int]bool {
	m := map[int]bool{}
	for i := from; i < to; i++ {
		m[i] = true
	}
	return m
}

func TestClassify(t *testing.T) {
	const root = "/w/game"
	runs := []Run{
		// An agent run from 09:00 to 09:05 that edited src/game.js at 09:04.
		{Root: root, Start: at(0), End: at(5), Tools: []Tool{{At: at(4), Path: "src/game.js"}}},
	}
	allInput := fakeAct{seenAll: true, active: minutes(-30, 60)}
	noInput := fakeAct{seenAll: true}
	cases := []struct {
		name string
		c    Change
		act  Activity
		want string
	}{
		{"during an agent run", Change{At: at(2), Root: root, Path: "README.md"}, allInput, Agent},
		{"just after the run ended", Change{At: at(5.5), Root: root, Path: "README.md"}, allInput, Agent},
		{"same file shortly after the tool call", Change{At: at(6.5), Root: root, Path: "src/game.js"}, allInput, Agent},
		{"other file at the same moment", Change{At: at(6.5), Root: root, Path: "src/menu.js"}, allInput, Human},
		{"no agent nearby, with input", Change{At: at(20), Root: root, Path: "src/game.js"}, allInput, Human},
		{"no agent nearby, no input", Change{At: at(20), Root: root, Path: "src/game.js"}, noInput, Unknown},
		{"no activity data", Change{At: at(20), Root: root, Path: "src/game.js"}, nil, Unknown},
		{"agent run in another project", Change{At: at(2), Root: "/w/other", Path: "a.go"}, allInput, Human},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Classify(c.c, runs, c.act); got != c.want {
				t.Fatalf("got %s want %s", got, c.want)
			}
		})
	}
}

func TestHandSessions(t *testing.T) {
	const root = "/w/game"
	// Input from 09:20 to 09:26 and 10:30 to 10:33. Hand edits at 09:24 and
	// 09:26 (one session) and at 10:32 (a second session, over 30m later).
	// An edit at 09:40 has no input that minute and is not hand-written.
	act := fakeAct{seenAll: true, active: func() map[int]bool {
		m := minutes(20, 26)
		for k := range minutes(90, 93) {
			m[k] = true
		}
		return m
	}()}
	changes := []Change{
		{At: at(24.5), Root: root, Path: "a.go"},
		{At: at(25.9), Root: root, Path: "b.go"},
		{At: at(40), Root: root, Path: "c.go"},
		{At: at(92.2), Root: root, Path: "a.go"},
	}
	rs, counts := HandSessions(changes, nil, act, func(string) string { return "game" })
	if len(rs) != 2 {
		t.Fatalf("want 2 hand sessions, got %d", len(rs))
	}
	if counts.Human != 3 || counts.Unknown != 1 || counts.Agent != 0 {
		t.Fatalf("counts %+v", counts)
	}
	first := rs[0]
	// Lookback 5m from 09:24:30 is 09:19:30, but input only from 09:20:
	// credited 09:20:00 to 09:25:54 (the second edit extends it).
	if first.CreditedSec != 5*60+54 {
		t.Fatalf("first hand session credited %d", first.CreditedSec)
	}
	if first.Harness != "manual" || first.Project != "game" || first.HandEdits != 2 || first.Signal != "activity" {
		t.Fatalf("first %+v", first)
	}
	if rs[1].CreditedSec != 2*60+12 { // 10:30:00 to 10:32:12
		t.Fatalf("second hand session credited %d", rs[1].CreditedSec)
	}
	for _, r := range rs {
		if !strings.HasPrefix(r.SessionID, "hand-") {
			t.Fatalf("id %q", r.SessionID)
		}
		for _, iv := range r.Intervals {
			if iv.Rule == "" || iv.Kind != "hand" {
				t.Fatalf("interval %+v", iv)
			}
			if strings.Contains(iv.Rule, ".go") {
				t.Fatalf("rules are synced publicly and must not name files: %q", iv.Rule)
			}
		}
	}
	// Stable IDs: the same input gives the same sessions.
	again, _ := HandSessions(changes, nil, act, func(string) string { return "game" })
	if again[0].SessionID != first.SessionID {
		t.Fatal("hand session IDs must be stable across scans")
	}
}

func TestLogRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cs := []Change{{At: at(1), Root: "/w/a", Path: "x.go"}, {At: at(60 * 24), Root: "/w/a", Path: "y.go"}}
	if err := Append(dir, cs); err != nil {
		t.Fatal(err)
	}
	got, err := Read(dir, at(0))
	if err != nil || len(got) != 2 {
		t.Fatalf("err %v got %v", err, got)
	}
	got, _ = Read(dir, at(60*24-1))
	if len(got) != 1 || got[0].Path != "y.go" {
		t.Fatalf("since filter: %v", got)
	}
}
