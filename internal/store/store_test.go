package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/n1k0s1/grit/internal/attest"
	"github.com/n1k0s1/grit/internal/model"
)

var t0 = time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)

func open(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "grit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() }) // Windows cannot delete an open database
	return st
}

func user(t *testing.T, st *Store, name string) User {
	t.Helper()
	if _, err := st.CreateUser(name); err != nil {
		t.Fatal(err)
	}
	u, err := st.UserByName(name)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// session builds a synced (public) result with an attestation chain.
func session(id string, signal string, prompts ...string) model.Result {
	r := model.Result{SessionID: id, Harness: "claude-code", Project: "game", Start: t0, End: t0.Add(10 * time.Minute), Signal: signal, Prompts: len(prompts)}
	for i, p := range prompts {
		r.Turns = append(r.Turns, model.Turn{Index: i, PromptAt: t0.Add(time.Duration(i*3) * time.Minute), PromptText: p})
	}
	salt := attest.Salt([]byte("test secret"), id)
	for i, h := range attest.Chain(id, salt, r.Turns) {
		r.Turns[i].Link = h
	}
	return r.Public()
}

func trust(t *testing.T, st *Store, u User, id string) SessionRow {
	t.Helper()
	p, err := st.Profile(u)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range p.Sessions {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("session %s not found", id)
	return SessionRow{}
}

func TestWitnessAndTier(t *testing.T) {
	cases := []struct {
		name          string
		syncDelay     time.Duration
		signal        string
		links         bool
		witness, tier string
	}{
		{"live sync with activity is high", 30 * time.Second, "activity", true, "witnessed", "high"},
		{"live sync without activity is medium", 30 * time.Second, "", true, "witnessed", "medium"},
		{"late sync with activity is medium", 48 * time.Hour, "activity", true, "backfilled", "medium"},
		{"late sync without activity is low", 48 * time.Hour, "", true, "backfilled", "low"},
		{"no attestation without activity is low", 30 * time.Second, "", false, "", "low"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := open(t)
			u := user(t, st, "ava")
			r := session("s1", c.signal, "add a pause menu", "make it work with a controller too")
			if !c.links {
				for i := range r.Turns {
					r.Turns[i].Link = ""
				}
			}
			// Sync at the end of the session plus the delay.
			st.Now = func() time.Time { return r.End.Add(c.syncDelay) }
			if err := st.UpsertSessions(u.ID, []model.Result{r}); err != nil {
				t.Fatal(err)
			}
			got := trust(t, st, u, "s1")
			if got.Witness != c.witness || got.Tier != c.tier || got.Signal != c.signal {
				t.Fatalf("witness %q tier %q signal %q, want %q %q %q", got.Witness, got.Tier, got.Signal, c.witness, c.tier, c.signal)
			}
		})
	}
}

func TestFirstSeenNeverMoves(t *testing.T) {
	st := open(t)
	u := user(t, st, "ava")
	first := session("s1", "activity", "add a pause menu", "make it work with a controller too")
	st.Now = func() time.Time { return t0.Add(5 * time.Minute) }
	if err := st.UpsertSessions(u.ID, []model.Result{first}); err != nil {
		t.Fatal(err)
	}
	// The same links synced again a day later keep their first-seen time.
	st.Now = func() time.Time { return t0.Add(24 * time.Hour) }
	if err := st.UpsertSessions(u.ID, []model.Result{first}); err != nil {
		t.Fatal(err)
	}
	if got := trust(t, st, u, "s1"); got.Witness != "witnessed" {
		t.Fatalf("re-sync must not lose the witness: %q", got.Witness)
	}
	seen, err := st.SeenLinks(u.ID, "s1")
	if err != nil || len(seen) != 2 || !seen[0][0].FirstSeen.Equal(t0.Add(5*time.Minute)) {
		t.Fatalf("seen %+v err %v", seen, err)
	}
	// A third turn that only reaches the server a day later is backfilled,
	// and so is the session.
	more := session("s1", "activity", "add a pause menu", "make it work with a controller too", "and a settings page")
	if err := st.UpsertSessions(u.ID, []model.Result{more}); err != nil {
		t.Fatal(err)
	}
	if got := trust(t, st, u, "s1"); got.Witness != "backfilled" || got.Tier != "medium" {
		t.Fatalf("got %+v", got)
	}
}

func TestMigratesOldDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// The sessions table as the MVP created it.
	if _, err := db.Exec(`CREATE TABLE sessions (user_id INTEGER NOT NULL, id TEXT NOT NULL, project_id INTEGER NOT NULL,
		harness TEXT NOT NULL, model TEXT NOT NULL DEFAULT '', branch TEXT NOT NULL DEFAULT '', start_at INTEGER NOT NULL,
		end_at INTEGER NOT NULL, wall_s INTEGER NOT NULL, agent_s INTEGER NOT NULL, credited_s INTEGER NOT NULL,
		prompts INTEGER NOT NULL, flags TEXT NOT NULL DEFAULT '[]', body TEXT NOT NULL, updated_at INTEGER NOT NULL,
		PRIMARY KEY (user_id, id))`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	u := user(t, st, "ava")
	if err := st.UpsertSessions(u.ID, []model.Result{session("s1", "", "hello there agent")}); err != nil {
		t.Fatal(err)
	}
}
