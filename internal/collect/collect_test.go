package collect

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/n1k0s1/grit/internal/model"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func kinds(s model.Session) (p, a, tl int) {
	for _, e := range s.Events {
		switch e.Kind {
		case model.Prompt:
			p++
		case model.Agent:
			a++
		case model.Tool:
			tl++
		}
	}
	return
}

func TestClaudeCode(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude", "projects", "-x", "s1.jsonl")
	write(t, path, `{"type":"queue-operation","timestamp":"2026-11-19T01:00:00Z"}
{"type":"user","sessionId":"s1","cwd":"/tmp/game","gitBranch":"main","timestamp":"2026-11-19T01:00:01Z","origin":{"kind":"human"},"message":{"role":"user","content":"add a scoreboard"}}
{"type":"assistant","timestamp":"2026-11-19T01:00:05Z","message":{"model":"claude-opus-5-5","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/tmp/game/ui.js"}}]}}
{"type":"user","timestamp":"2026-11-19T01:00:06Z","message":{"content":[{"type":"tool_result","content":"ok"}]}}
{"type":"user","timestamp":"2026-11-19T01:00:07Z","isMeta":true,"message":{"content":"Caveat: ignore"}}
{"type":"user","timestamp":"2026-11-19T01:00:08Z","message":{"content":"<local-command-stdout>hi</local-command-stdout>"}}
{"type":"user","timestamp":"2026-11-19T01:00:09Z","origin":{"kind":"task-notification"},"message":{"content":"background job done"}}
{"type":"user","timestamp":"2026-11-19T01:01:00Z","message":{"content":"<command-name>/review</command-name>"}}
`)
	ss, err := ClaudeCode{}.Parse(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("err=%v n=%d", err, len(ss))
	}
	s := ss[0]
	p, _, tl := kinds(s)
	if p != 2 || tl != 1 || s.Model != "claude-opus-5-5" || s.Cwd != "/tmp/game" || s.GitBranch != "main" {
		t.Fatalf("prompts=%d tools=%d %+v", p, tl, s)
	}
	if got := (ClaudeCode{}).Paths(home); len(got) != 1 {
		t.Fatalf("paths %v", got)
	}
}

func TestCodex(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".codex", "sessions", "2026", "11", "19", "rollout-2026-11-19T01-00-00-abc.jsonl")
	write(t, path, `{"timestamp":"2026-11-19T01:00:00Z","type":"session_meta","payload":{"id":"abc","cwd":"/tmp/app","git":{"branch":"dev","repository_url":"https://github.com/x/app"}}}
{"timestamp":"2026-11-19T01:00:00Z","type":"turn_context","payload":{"model":"gpt-6-codex","cwd":"/tmp/app"}}
{"timestamp":"2026-11-19T01:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>...</environment_context>"}]}}
{"timestamp":"2026-11-19T01:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"why does login 500?"}}
{"timestamp":"2026-11-19T01:00:03Z","type":"response_item","payload":{"type":"function_call","name":"shell","arguments":"{\"command\":[\"bash\",\"-lc\",\"npm test\"]}"}}
{"timestamp":"2026-11-19T01:00:04Z","type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","input":"*** Begin Patch\n*** Update File: src/auth.ts\n"}}
{"timestamp":"2026-11-19T01:00:06Z","type":"event_msg","payload":{"type":"agent_message","message":"fixed"}}
{"timestamp":"2026-11-19T01:00:06Z","type":"event_msg","payload":{"type":"token_count"}}
`)
	ss, err := Codex{}.Parse(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("err=%v n=%d", err, len(ss))
	}
	s := ss[0]
	p, _, tl := kinds(s)
	if p != 1 || tl != 2 || s.ID != "abc" || s.GitRemote == "" || s.Model != "gpt-6-codex" {
		t.Fatalf("prompts=%d tools=%d %+v", p, tl, s)
	}
	var cmd, file string
	for _, e := range s.Events {
		if e.Command != "" {
			cmd = e.Command
		}
		if e.File != "" {
			file = e.File
		}
	}
	if cmd != "bash -lc npm test" || file != "src/auth.ts" {
		t.Fatalf("cmd=%q file=%q", cmd, file)
	}
}

func TestHooks(t *testing.T) {
	home := t.TempDir()
	h1 := FromHookInput("cursor", "prompt", []byte(`{"conversation_id":"c1","workspace_roots":["/w/site"],"prompt":"make the nav sticky"}`))
	h2 := FromHookInput("cursor", "stop", []byte(`{"conversation_id":"c1"}`))
	if h1.Session != "c1" || h1.Cwd != "/w/site" || h1.Text != "make the nav sticky" {
		t.Fatalf("%+v", h1)
	}
	if err := AppendHook(home, h1); err != nil {
		t.Fatal(err)
	}
	if err := AppendHook(home, h2); err != nil {
		t.Fatal(err)
	}
	ss, err := Hooks{}.Parse(EventsPath(home))
	if err != nil || len(ss) != 1 || ss[0].Harness != "hook:cursor" || len(ss[0].Events) != 2 {
		t.Fatalf("err=%v %+v", err, ss)
	}
}
