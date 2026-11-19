package collect

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/n1k0s1/grit/internal/model"
)

// ClaudeCode reads ~/.claude/projects/<project>/<session>.jsonl.
type ClaudeCode struct{}

func (ClaudeCode) Name() string { return "claude-code" }

func (ClaudeCode) Paths(home string) []string {
	return glob(filepath.Join(home, ".claude", "projects", "*", "*.jsonl"))
}

var slashCmd = regexp.MustCompile(`<command-name>([^<]*)</command-name>`)

// ccLine is the part of a transcript line grit reads. Decoding into a
// struct skips everything else (tool output can be megabytes) without
// allocating it, which keeps the daemon small on huge transcripts.
type ccLine struct {
	Type             string     `json:"type"`
	Timestamp        string     `json:"timestamp"`
	SessionID        string     `json:"sessionId"`
	Cwd              string     `json:"cwd"`
	GitBranch        string     `json:"gitBranch"`
	IsMeta           bool       `json:"isMeta"`
	IsCompactSummary bool       `json:"isCompactSummary"`
	IsSidechain      bool       `json:"isSidechain"`
	Origin           *ccOrigin  `json:"origin"`
	Message          *ccMessage `json:"message"`
}

type ccOrigin struct {
	Kind string `json:"kind"`
}

type ccMessage struct {
	Model   string    `json:"model"`
	Content ccContent `json:"content"`
}

// ccContent is either a plain string or a list of blocks.
type ccContent struct {
	Text   string
	Blocks []ccBlock
}

func (c *ccContent) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &c.Text)
	}
	if len(b) > 0 && b[0] == '[' {
		return json.Unmarshal(b, &c.Blocks)
	}
	return nil
}

type ccBlock struct {
	Type  string  `json:"type"`
	Text  string  `json:"text"`
	Name  string  `json:"name"`
	Input ccInput `json:"input"`
}

type ccInput struct {
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
	Path         string `json:"path"`
	Command      any    `json:"command"` // usually a string
}

// humanText returns the prompt text if this user line was typed by a person.
func humanText(m *ccLine) (string, bool) {
	if m.IsMeta || m.IsCompactSummary || m.IsSidechain {
		return "", false
	}
	if m.Origin != nil && m.Origin.Kind != "human" {
		return "", false
	}
	if m.Message == nil {
		return "", false
	}
	text := m.Message.Content.Text
	for _, b := range m.Message.Content.Blocks {
		switch b.Type {
		case "tool_result":
			return "", false
		case "text":
			text += b.Text
		}
	}
	if text == "" {
		return "", false
	}
	if strings.HasPrefix(text, "<local-command-") || strings.HasPrefix(text, "Caveat: ") {
		return "", false
	}
	if sm := slashCmd.FindStringSubmatch(text); sm != nil {
		return sm[1], true
	}
	return text, true
}

func (c ClaudeCode) Parse(path string) ([]model.Session, error) { return parseAll(path, c.Start(path)) }

// Start returns line-at-a-time parse state for one transcript.
func (ClaudeCode) Start(path string) LineState {
	return &claudeState{s: model.Session{Harness: "claude-code", Source: path}}
}

type claudeState struct{ s model.Session }

func (st *claudeState) Line(line []byte) {
	s := &st.s
	var m ccLine
	if json.Unmarshal(line, &m) != nil {
		return
	}
	at, ok := parseTime(m.Timestamp)
	if !ok {
		return
	}
	if s.ID == "" {
		s.ID = m.SessionID
	}
	if m.Cwd != "" {
		s.Cwd = m.Cwd
	}
	if m.GitBranch != "" {
		s.GitBranch = m.GitBranch
	}
	switch m.Type {
	case "user":
		if text, ok := humanText(&m); ok {
			addEvent(s, model.Event{At: at, Kind: model.Prompt, Text: text})
		} else {
			addEvent(s, model.Event{At: at, Kind: model.Agent})
		}
	case "assistant":
		if m.Message == nil {
			addEvent(s, model.Event{At: at, Kind: model.Agent})
			return
		}
		if mod := m.Message.Model; mod != "" && !strings.HasPrefix(mod, "<") {
			s.Model = mod
		}
		addEvent(s, model.Event{At: at, Kind: model.Agent})
		for _, b := range m.Message.Content.Blocks {
			if b.Type != "tool_use" {
				continue
			}
			e := model.Event{At: at, Kind: model.Tool, Tool: b.Name}
			for _, f := range []string{b.Input.FilePath, b.Input.NotebookPath, b.Input.Path} {
				if f != "" {
					e.File = f
					break
				}
			}
			e.Command, _ = b.Input.Command.(string)
			addEvent(s, e)
		}
	}
}

func (st *claudeState) Sessions() []model.Session {
	if len(st.s.Events) == 0 {
		return nil
	}
	s := st.s
	if s.ID == "" {
		s.ID = strings.TrimSuffix(filepath.Base(s.Source), ".jsonl")
	}
	return []model.Session{s}
}
