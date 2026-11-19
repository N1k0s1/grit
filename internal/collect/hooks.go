package collect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/n1k0s1/grit/internal/model"
)

// Hooks is the plugin path for harnesses that keep no usable local log
// (Cursor, Windsurf, Copilot, browser chat via an extension, anything new).
// Their hook or extension pipes a JSON blob into `grit hook`, which appends a
// HookEvent line to ~/.grit/events.jsonl. This collector reads that file back
// and groups lines into sessions.
type Hooks struct{}

// HookEvent is one line of ~/.grit/events.jsonl.
type HookEvent struct {
	At      time.Time `json:"at"`
	Harness string    `json:"harness"`
	Session string    `json:"session"`
	Cwd     string    `json:"cwd,omitempty"`
	Kind    string    `json:"kind"` // prompt | agent | tool | stop
	Text    string    `json:"text,omitempty"`
	Tool    string    `json:"tool,omitempty"`
	File    string    `json:"file,omitempty"`
	Model   string    `json:"model,omitempty"`
}

func (Hooks) Name() string { return "hooks" }

// EventsPath is where `grit hook` appends.
func EventsPath(home string) string { return filepath.Join(home, ".grit", "events.jsonl") }

func (Hooks) Paths(home string) []string {
	if _, err := os.Stat(EventsPath(home)); err != nil {
		return nil
	}
	return []string{EventsPath(home)}
}

func (Hooks) Parse(path string) ([]model.Session, error) {
	by := map[string]*model.Session{}
	err := eachLine(path, func(line []byte) {
		var h HookEvent
		if json.Unmarshal(line, &h) != nil || h.Session == "" {
			return
		}
		key := h.Harness + "/" + h.Session
		s := by[key]
		if s == nil {
			s = &model.Session{ID: key, Harness: "hook:" + h.Harness, Source: path}
			by[key] = s
		}
		if h.Cwd != "" {
			s.Cwd = h.Cwd
		}
		if h.Model != "" {
			s.Model = h.Model
		}
		kind := model.Agent
		switch h.Kind {
		case "prompt":
			kind = model.Prompt
		case "tool":
			kind = model.Tool
		}
		s.Events = append(s.Events, model.Event{At: h.At, Kind: kind, Text: h.Text, Tool: h.Tool, File: h.File})
	})
	keys := make([]string, 0, len(by))
	for k := range by {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]model.Session, 0, len(by))
	for _, k := range keys {
		out = append(out, *by[k])
	}
	return out, err
}

// FromHookInput turns whatever JSON a harness hook passes on stdin into a
// HookEvent. Field names differ per harness, so this tries the common ones:
// Claude Code (session_id, cwd, prompt), Cursor (conversation_id,
// workspace_roots, prompt), and generic (session, text, message).
func FromHookInput(harness, kind string, raw []byte) HookEvent {
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	pick := func(keys ...string) string {
		for _, k := range keys {
			if v := str(m, k); v != "" {
				return v
			}
		}
		return ""
	}
	h := HookEvent{
		At: time.Now().UTC(), Harness: harness, Kind: kind,
		Session: pick("session_id", "conversation_id", "sessionId", "session", "chat_id"),
		Cwd:     pick("cwd", "workspace", "project_dir"),
		Text:    pick("prompt", "text", "message", "user_message"),
		Tool:    pick("tool_name", "tool"),
		File:    pick("file_path", "path"),
		Model:   pick("model"),
	}
	if h.Cwd == "" {
		if roots, ok := m["workspace_roots"].([]any); ok && len(roots) > 0 {
			h.Cwd, _ = roots[0].(string)
		}
	}
	if h.Session == "" {
		h.Session = "default"
	}
	return h
}

// AppendHook writes one event line to ~/.grit/events.jsonl.
func AppendHook(home string, h HookEvent) error {
	p := EventsPath(home)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	b, _ := json.Marshal(h)
	_, err = f.Write(append(b, '\n'))
	return err
}
