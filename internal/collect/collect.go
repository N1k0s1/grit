// Package collect reads agent sessions from the logs that harnesses already
// write to disk. Nothing has to be installed into the harness: Claude Code and
// Codex both keep full session transcripts locally, so tracking is passive.
// Harnesses without local transcripts report through `grit hook` instead,
// which appends to ~/.grit/events.jsonl (see hooks.go).
//
// Adding a harness means adding one Collector.
package collect

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/n1k0s1/grit/internal/model"
)

// Collector finds and parses one harness's session logs.
type Collector interface {
	Name() string
	Paths(home string) []string
	Parse(path string) ([]model.Session, error)
}

// All returns every built-in collector.
func All() []Collector {
	return []Collector{ClaudeCode{}, Codex{}, Hooks{}}
}

// HomeDir returns the user's home, honouring GRIT_HOME for testing and demos.
func HomeDir() string {
	if h := os.Getenv("GRIT_HOME"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return h
}

func glob(patterns ...string) []string {
	var out []string
	for _, p := range patterns {
		m, _ := filepath.Glob(p)
		out = append(out, m...)
	}
	return out
}

// eachLine calls fn for every JSON line in a file. Lines can be very long
// (tool output), so this avoids bufio.Scanner's token limit.
func eachLine(path string, fn func([]byte)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<16)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 1 {
			fn(line)
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func parseTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	return t, err == nil
}

func str(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func obj(m map[string]any, k string) map[string]any {
	if v, ok := m[k].(map[string]any); ok {
		return v
	}
	return nil
}

func decode(line []byte) map[string]any {
	var m map[string]any
	if json.Unmarshal(line, &m) != nil {
		return nil
	}
	return m
}
