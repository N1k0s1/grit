package collect

import (
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

// humanText returns the prompt text if this user line was typed by a person.
func humanText(m map[string]any) (string, bool) {
	if m["isMeta"] == true || m["isCompactSummary"] == true || m["isSidechain"] == true {
		return "", false
	}
	if o := obj(m, "origin"); o != nil {
		if str(o, "kind") != "human" {
			return "", false
		}
	}
	msg := obj(m, "message")
	if msg == nil {
		return "", false
	}
	var text string
	switch c := msg["content"].(type) {
	case string:
		text = c
	case []any:
		for _, b := range c {
			bm, _ := b.(map[string]any)
			switch str(bm, "type") {
			case "tool_result":
				return "", false
			case "text":
				text += str(bm, "text")
			}
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

func (ClaudeCode) Parse(path string) ([]model.Session, error) {
	s := model.Session{Harness: "claude-code", Source: path}
	err := eachLine(path, func(line []byte) {
		m := decode(line)
		if m == nil {
			return
		}
		at, ok := parseTime(str(m, "timestamp"))
		if !ok {
			return
		}
		if s.ID == "" {
			s.ID = str(m, "sessionId")
		}
		if c := str(m, "cwd"); c != "" {
			s.Cwd = c
		}
		if b := str(m, "gitBranch"); b != "" {
			s.GitBranch = b
		}
		switch str(m, "type") {
		case "user":
			if text, ok := humanText(m); ok {
				s.Events = append(s.Events, model.Event{At: at, Kind: model.Prompt, Text: text})
			} else {
				s.Events = append(s.Events, model.Event{At: at, Kind: model.Agent})
			}
		case "assistant":
			msg := obj(m, "message")
			if mod := str(msg, "model"); mod != "" && !strings.HasPrefix(mod, "<") {
				s.Model = mod
			}
			s.Events = append(s.Events, model.Event{At: at, Kind: model.Agent})
			blocks, _ := msg["content"].([]any)
			for _, b := range blocks {
				bm, _ := b.(map[string]any)
				if str(bm, "type") != "tool_use" {
					continue
				}
				in := obj(bm, "input")
				e := model.Event{At: at, Kind: model.Tool, Tool: str(bm, "name")}
				for _, k := range []string{"file_path", "notebook_path", "path"} {
					if f := str(in, k); f != "" {
						e.File = f
						break
					}
				}
				e.Command = str(in, "command")
				s.Events = append(s.Events, e)
			}
		}
	})
	if s.ID == "" {
		s.ID = strings.TrimSuffix(filepath.Base(path), ".jsonl")
	}
	if len(s.Events) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
}
