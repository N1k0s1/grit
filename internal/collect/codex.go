package collect

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/n1k0s1/grit/internal/model"
)

// Codex reads OpenAI Codex CLI rollouts from ~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl.
// Each line is {"timestamp", "type", "payload"}. Human prompts are the
// event_msg/user_message events; response_item user messages also carry
// injected context (AGENTS.md, environment), so those are not used.
type Codex struct{}

func (Codex) Name() string { return "codex" }

func (Codex) Paths(home string) []string {
	root := filepath.Join(home, ".codex", "sessions")
	return glob(
		filepath.Join(root, "*", "*", "*", "rollout-*.jsonl"),
		filepath.Join(root, "rollout-*.jsonl"),
	)
}

var patchFile = regexp.MustCompile(`\*\*\* (?:Update|Add|Delete) File: (\S+)`)

func (Codex) Parse(path string) ([]model.Session, error) {
	s := model.Session{Harness: "codex", Source: path}
	err := eachLine(path, func(line []byte) {
		m := decode(line)
		if m == nil {
			return
		}
		at, ok := parseTime(str(m, "timestamp"))
		if !ok {
			return
		}
		p := obj(m, "payload")
		if p == nil {
			return
		}
		switch str(m, "type") {
		case "session_meta":
			s.ID = str(p, "id")
			s.Cwd = str(p, "cwd")
			if g := obj(p, "git"); g != nil {
				s.GitBranch = str(g, "branch")
				s.GitRemote = str(g, "repository_url")
			}
		case "turn_context":
			if mod := str(p, "model"); mod != "" {
				s.Model = mod
			}
			if c := str(p, "cwd"); c != "" {
				s.Cwd = c
			}
		case "event_msg":
			switch str(p, "type") {
			case "user_message":
				if text := str(p, "message"); text != "" {
					s.Events = append(s.Events, model.Event{At: at, Kind: model.Prompt, Text: text})
				}
			case "token_count":
				// bookkeeping, not activity
			default:
				s.Events = append(s.Events, model.Event{At: at, Kind: model.Agent})
			}
		case "response_item":
			switch str(p, "type") {
			case "function_call", "custom_tool_call", "local_shell_call":
				e := model.Event{At: at, Kind: model.Tool, Tool: str(p, "name")}
				args := str(p, "arguments") + str(p, "input")
				var a map[string]any
				if json.Unmarshal([]byte(args), &a) == nil {
					if cmd, ok := a["command"].([]any); ok {
						parts := make([]string, 0, len(cmd))
						for _, c := range cmd {
							if cs, ok := c.(string); ok {
								parts = append(parts, cs)
							}
						}
						e.Command = strings.Join(parts, " ")
					}
				}
				if pm := patchFile.FindStringSubmatch(args); pm != nil {
					e.File = pm[1]
				}
				s.Events = append(s.Events, e)
			case "message":
				if str(p, "role") == "assistant" {
					s.Events = append(s.Events, model.Event{At: at, Kind: model.Agent})
				}
			default:
				s.Events = append(s.Events, model.Event{At: at, Kind: model.Agent})
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
