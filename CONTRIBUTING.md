# Contributing to grit

Thanks for helping. grit credits only human-steered time, so the bar is: every number must be explainable, and nothing private leaves the machine by accident. Read `CLAUDE.md` for the hard rules (no cgo, no em or en dashes, synthetic fixtures only, no secrets). They apply to people too.

## Setup

You need Go (the version in `go.mod` or newer) and git. Nothing else.

```bash
go vet ./...
go test ./...
go test -race ./...
CGO_ENABLED=0 go build -o bin/ ./cmd/...
./bin/gritd -demo        # http://localhost:8080 with generated data
```

Every commit must pass `go vet ./...` and `go test ./...`. CI also runs the race detector on Linux, macOS and Windows, cross-compiles for six targets without cgo, and fails on any em or en dash. Check dashes before you commit:

```bash
git grep -nI --untracked -e "$(printf '\342\200\223')" -e "$(printf '\342\200\224')" && echo "FIX DASHES" || echo ok
```

## Adding a collector

A collector teaches grit to read one AI tool's session logs. It turns that tool's format into a list of `model.Session`, each holding a time-ordered list of `model.Event`. Scoring, git linking, dedupe and sync are shared, so a collector only parses.

### 1. Learn the format

Find where the tool writes its logs and what one session looks like. You need, at minimum:

- a timestamp on every human prompt,
- a timestamp on agent activity (replies, tool calls, tool results), so grit knows when a run ended,
- some way to tell a human prompt from text the tool injected (system context, tool results, summaries).

If the tool has no per-message timestamps, it cannot be scored honestly. If the format is undocumented and you cannot confirm it on a real install, write a hook recipe instead (see "Tools without logs" below) rather than a guessed parser.

You may read your own real logs locally to check the parser works. Never copy their contents into the repo, a fixture, a test, a commit or a screenshot.

### 2. Implement the interface

The interface lives in `internal/collect/collect.go`:

```go
type Collector interface {
	Name() string                              // short id, e.g. "gemini-cli"
	Paths(home string) []string                // log files under the user's home
	Parse(path string) ([]model.Session, error) // one file to zero or more sessions
}
```

Add a file `internal/collect/<tool>.go`:

```go
// GeminiCLI reads ~/.gemini/tmp/<project>/chats/session-*.jsonl.
type GeminiCLI struct{}

func (GeminiCLI) Name() string { return "gemini-cli" }

func (GeminiCLI) Paths(home string) []string {
	return glob(filepath.Join(home, ".gemini", "tmp", "*", "chats", "session-*.jsonl"))
}

func (GeminiCLI) Parse(path string) ([]model.Session, error) {
	s := model.Session{Harness: "gemini-cli", Source: path}
	err := eachLine(path, func(line []byte) {
		m := decode(line)
		// read the timestamp with parseTime, then append Prompt, Agent or Tool events
	})
	if len(s.Events) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
}
```

Rules for `Parse`:

- **Prompt** events are text a person typed. Nothing else. Slash commands count; injected context, tool results, compaction summaries and notifications do not.
- **Agent** events are anything the agent produced or received while running: replies, thinking, tool results. They only need a timestamp.
- **Tool** events are tool calls. Fill `Tool`, and `File` or `Command` when the log says which file or shell command it touched. These stay on the machine unless the user submits.
- Set `ID` (stable across re-reads), `Cwd` (so git linking works), and `Model` and `GitBranch` if the log has them.
- Be forgiving. Skip lines you do not understand, never panic on bad input, and return what you parsed alongside any error.
- Use the helpers in `collect.go`: `glob`, `eachLine` (handles very long lines), `decode`, `parseTime`, `str`, `obj`.
- Logs can be large. Stream them line by line; do not read whole files into memory.

Then register it in `All()` in `collect.go`.

### 3. Write a synthetic fixture and test

Add a test to `internal/collect/collect_test.go` (or `<tool>_test.go`). Write the fixture by hand inline, as the existing tests do: invented prompts, invented paths like `/tmp/game`, no real usernames. Cover:

- a human prompt is counted as a prompt,
- injected or system text is not,
- tool calls are counted, with file and command when present,
- `ID`, `Cwd` and `Model` come out right,
- `Paths` finds the file in a temp home.

See `TestClaudeCode` and `TestCodex` for the pattern.

### 4. Add a README row

Add the tool to the collector table in `README.md` and say honestly which it is:

- **verified on a real install**: you ran `grit sessions` against a real install of that tool and the numbers made sense, or
- **fixture only**: it passes the synthetic test, but nobody has run it against the real tool yet.

Never mark a collector verified from a fixture alone.

### Tools without logs

If a tool keeps no usable local log but has hooks, there is no collector to write. Point its hooks at `grit hook -harness <tool> -kind prompt|agent|tool|stop`. `FromHookInput` in `internal/collect/hooks.go` already reads common field names; if your tool uses different ones, add them there with a test in `TestHooks`. Document the recipe in the README.

## Scoring changes

Scoring is the trust core. Changes to `internal/score` need table tests, every existing test must keep passing unchanged, and a changed expectation needs a reason in the commit message. Every credited second must carry a rule string. Flags mark sessions for reviewers; they never silently remove time.

## Commits and pull requests

Small commits, one idea each, with tests in the same commit. Commit messages, docs and UI text use full sentences and normal capitalisation. Open a pull request against `main`; CI must be green.

By contributing you agree your work is licensed under the AGPLv3, like the rest of grit.
