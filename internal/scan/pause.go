package scan

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Pausing stops grit completely for a while: no activity is sampled, no
// edits are logged, nothing is synced, and AI sessions that start while
// paused are never scored or sent, even after resuming. ~/.grit/paused
// holds the start of the current pause; ~/.grit/pauses.jsonl keeps past
// ones.

type pause struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

func pausedPath(home string) string { return filepath.Join(home, ".grit", "paused") }
func pausesPath(home string) string { return filepath.Join(home, ".grit", "pauses.jsonl") }

// Paused reports whether grit is paused, and since when.
func Paused(home string) (bool, time.Time) {
	b, err := os.ReadFile(pausedPath(home))
	if err != nil {
		return false, time.Time{}
	}
	t, err := time.Parse(time.RFC3339, string(b))
	if err != nil {
		return true, time.Time{} // an unreadable pause file still pauses
	}
	return true, t
}

// Pause starts a pause now (a no-op if already paused).
func Pause(home string) error {
	if ok, _ := Paused(home); ok {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(pausedPath(home)), 0o700); err != nil {
		return err
	}
	return os.WriteFile(pausedPath(home), []byte(time.Now().UTC().Format(time.RFC3339)), 0o600)
}

// Resume ends the current pause and records it.
func Resume(home string) error {
	ok, from := Paused(home)
	if !ok {
		return nil
	}
	f, err := os.OpenFile(pausesPath(home), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(pause{From: from, To: time.Now().UTC()})
	_, err = f.Write(append(b, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := os.Remove(pausedPath(home)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// pauses returns every pause, with an open one running to the far future.
func pauses(home string) []pause {
	var out []pause
	if f, err := os.Open(pausesPath(home)); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			var p pause
			if json.Unmarshal(sc.Bytes(), &p) == nil {
				out = append(out, p)
			}
		}
		f.Close()
	}
	if ok, from := Paused(home); ok {
		out = append(out, pause{From: from, To: time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)})
	}
	return out
}

func inPause(ps []pause, t time.Time) bool {
	for _, p := range ps {
		if !t.Before(p.From) && t.Before(p.To) {
			return true
		}
	}
	return false
}
