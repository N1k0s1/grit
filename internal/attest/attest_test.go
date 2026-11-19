package attest

import (
	"testing"
	"time"

	"github.com/n1k0s1/grit/internal/model"
)

var t0 = time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)

func turns() []model.Turn {
	return []model.Turn{
		{Index: 0, PromptAt: t0, PromptText: "add a pause menu with resume and quit buttons"},
		{Index: 1, PromptAt: t0.Add(4 * time.Minute), PromptText: "the quit button closes the tab, it should go to the title screen"},
		{Index: 2, PromptAt: t0.Add(9 * time.Minute), PromptText: "yes"},
	}
}

var salt = Salt([]byte("device secret for tests only, 32b"), "s1")

// record simulates what the server stored at sync: each link with the time
// it was first seen, delay after the prompt happened.
func record(ts []model.Turn, delay time.Duration) map[int][]Seen {
	chain := Chain("s1", salt, ts)
	m := map[int][]Seen{}
	for i, h := range chain {
		m[i] = append(m[i], Seen{Hash: h, FirstSeen: ts[i].PromptAt.Add(delay)})
	}
	return m
}

func lookup(m map[int][]Seen) func(int) []Seen { return func(i int) []Seen { return m[i] } }

func TestVerify(t *testing.T) {
	live := record(turns(), 40*time.Second)
	late := record(turns(), 48*time.Hour)

	tampered := turns()
	tampered[1].PromptText = "the quit button closes the tab, it should go to the title screen please"

	reordered := turns()
	reordered[1].PromptText, reordered[2].PromptText = reordered[2].PromptText, reordered[1].PromptText

	moved := turns()
	moved[1].PromptAt = moved[1].PromptAt.Add(-3 * time.Minute) // claim the reply came sooner

	dropped := turns()[:1]
	dropped = append(dropped, turns()[2])
	dropped[1].Index = 1

	cases := []struct {
		name   string
		bundle []model.Turn
		salt   []byte
		seen   map[int][]Seen
		want   Status
	}{
		{"live sync is witnessed", turns(), salt, live, Witnessed},
		{"late sync is backfilled", turns(), salt, late, Backfilled},
		{"never synced is backfilled", turns(), salt, nil, Backfilled},
		{"tampered prompt fails", tampered, salt, live, Mismatch},
		{"reordered turns fail", reordered, salt, live, Mismatch},
		{"moved timestamp fails", moved, salt, live, Mismatch},
		{"dropped turn fails", dropped, salt, live, Mismatch},
		{"wrong salt fails", turns(), Salt([]byte("another device"), "s1"), live, Mismatch},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, links := Verify("s1", c.salt, c.bundle, lookup(c.seen), Window)
			if got != c.want {
				t.Fatalf("status %s, want %s (links %+v)", got, c.want, links)
			}
			if len(links) != len(c.bundle) {
				t.Fatalf("%d link results for %d turns", len(links), len(c.bundle))
			}
		})
	}
}

func TestPartlyLateIsBackfilled(t *testing.T) {
	ts := turns()
	seen := record(ts, 30*time.Second)
	// The last turn was first synced a day later.
	seen[2][0].FirstSeen = ts[2].PromptAt.Add(24 * time.Hour)
	got, links := Verify("s1", salt, ts, lookup(seen), Window)
	if got != Backfilled || links[0].Status != Witnessed || links[2].Status != Backfilled {
		t.Fatalf("%s %+v", got, links)
	}
}

func TestLaterConflictingLinkDoesNotHideOriginal(t *testing.T) {
	// A client first syncs the real chain, then a forged one for the same
	// turns. The server keeps both; a bundle matching the original still
	// verifies, and its timing comes from the original.
	ts := turns()
	seen := record(ts, 20*time.Second)
	seen[1] = append(seen[1], Seen{Hash: "forged", FirstSeen: ts[1].PromptAt.Add(time.Hour)})
	if got, _ := Verify("s1", salt, ts, lookup(seen), Window); got != Witnessed {
		t.Fatalf("got %s", got)
	}
}

func TestRewrittenAfterLiveSyncFails(t *testing.T) {
	// The real chain was synced live. Later a client sends a rewritten
	// prompt with its own matching link (for example inside a submission).
	// The rewritten link matches the bundle, but it is not the first thing
	// the server saw at that index, so the session fails.
	ts := turns()
	seen := record(ts, 20*time.Second)
	forged := turns()
	forged[1].PromptText = "a rewritten prompt"
	fc := Chain("s1", salt, forged)
	for i := 1; i < len(fc); i++ {
		seen[i] = append(seen[i], Seen{Hash: fc[i], FirstSeen: ts[i].PromptAt.Add(time.Hour)})
	}
	if got, _ := Verify("s1", salt, forged, lookup(seen), Window); got != Mismatch {
		t.Fatalf("got %s, want mismatch", got)
	}
}

func TestTiming(t *testing.T) {
	cases := []struct {
		delay time.Duration
		want  Status
	}{
		{0, Witnessed},
		{Window, Witnessed},
		{Window + time.Second, Backfilled},
		{-2 * time.Minute, Witnessed}, // small clock skew
		{-time.Hour, Backfilled},      // claims a prompt from the future
	}
	for _, c := range cases {
		if got := Timing(t0, t0.Add(c.delay), Window); got != c.want {
			t.Errorf("delay %v: %s, want %s", c.delay, got, c.want)
		}
	}
}

func TestChainIsDeterministicAndSalted(t *testing.T) {
	a := Chain("s1", salt, turns())
	b := Chain("s1", salt, turns())
	if len(a) != 3 || a[2] != b[2] {
		t.Fatal("same input must give the same chain")
	}
	c := Chain("s1", Salt([]byte("x"), "s1"), turns())
	if c[0] == a[0] {
		t.Fatal("salt must change every link")
	}
	if len(a[0]) != 64 {
		t.Fatalf("hex sha256 expected, got %q", a[0])
	}
}
