package edits

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// DefaultDir is where changes are logged for a home directory. The log
// stays on this machine: only the resulting numbers are synced.
func DefaultDir(home string) string { return filepath.Join(home, ".grit", "edits") }

// Append writes changes to one JSON lines file per UTC day.
func Append(dir string, cs []Change) error {
	if len(cs) == 0 {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	byDay := map[string][]Change{}
	for _, c := range cs {
		k := c.At.UTC().Format("2006-01-02")
		byDay[k] = append(byDay[k], c)
	}
	for day, list := range byDay {
		f, err := os.OpenFile(filepath.Join(dir, day+".jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		w := bufio.NewWriter(f)
		enc := json.NewEncoder(w)
		for _, c := range list {
			_ = enc.Encode(c)
		}
		err = w.Flush()
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Read returns logged changes at or after since.
func Read(dir string, since time.Time) ([]Change, error) {
	var out []Change
	for d := since.UTC().Truncate(24 * time.Hour); !d.After(time.Now().UTC().Add(24 * time.Hour)); d = d.Add(24 * time.Hour) {
		f, err := os.Open(filepath.Join(dir, d.Format("2006-01-02")+".jsonl"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return out, err
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			var c Change
			if json.Unmarshal(sc.Bytes(), &c) == nil && !c.At.Before(since) {
				out = append(out, c)
			}
		}
		f.Close()
	}
	return out, nil
}
