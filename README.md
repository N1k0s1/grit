# grit

Time tracking for building with AI. grit counts the time you spend steering an agent, not the time the agent runs.

Built for the Hack Club AI time tracking bounty. Licensed under AGPLv3.

## Why

An agent can run for an hour while you make dinner. Wall-clock time and agent time say nothing about how hard someone worked. What every harness does record is when the human spoke and when the agent stopped. grit credits time only where those timestamps show a person engaged, and writes down the rule behind every credited minute so a reviewer can check it.

## How it works

1. **Collect, passively.** Claude Code (`~/.claude/projects/*/*.jsonl`) and Codex (`~/.codex/sessions/**/rollout-*.jsonl`) already save full session transcripts. grit reads them. Nothing is installed into the harness. Tools without local logs (Cursor, Windsurf, Copilot, browser chat) pipe their hook payload into `grit hook`, which appends to `~/.grit/events.jsonl`. A new harness is one `Collector` in `internal/collect`.
2. **Score.** `internal/score` splits a session into turns (prompt, then agent run) and credits:
   - the gap between the agent finishing and the next prompt, if under 10 minutes (reading, testing, deciding); after a longer gap, only a typing estimate for the prompt;
   - at most 90 seconds before bare go-aheads like "continue" or "yes";
   - the agent run itself (up to 8 minutes) only if the human came back within 2 minutes of it finishing, otherwise just the first minute;
   - a 2 minute review tail after the last turn.
   Sessions running at the same moment are deduplicated, so five agents in parallel still count as one person. Sessions are flagged `autopilot`, `mostly-agent` or `long-idle` for reviewers.
3. **Link to git.** Each session's working directory resolves to a repo, remote and the commits made during it.
4. **Sync numbers, submit evidence.** `grit sync` sends only numbers: start, end, credited seconds, prompt and tool counts. No prompt text, file names or commands. `grit submit PROJECT` uploads that project's sessions with prompt text, with API keys, tokens, emails and passwords scrubbed, so reviewers can audit the hours. `grit submit -dry` prints exactly what would be sent.
5. **Server.** `gritd` is one Go binary with SQLite: accounts, public profiles, projects, git links, a leaderboard and profiles that update live over server-sent events, plus a review queue where a reviewer reads every prompt and approves, deflates or rejects.

## Run it

Needs Go 1.24. Both binaries are pure Go (no cgo), so they cross-compile to Windows, macOS and Linux.

```bash
go test ./...
go build -o bin/ ./cmd/...

./bin/gritd -demo                       # http://localhost:8080 with generated example builders
./bin/grit login -server http://localhost:8080 -user yourname
./bin/grit                              # today and this week, offline
./bin/grit sessions                     # every session with credited time
./bin/grit explain <id>                 # how each minute was counted
./bin/grit sync                         # send numbers
./bin/grit watch                        # keep syncing in the background
./bin/grit submit <project> -dry        # see the review bundle
./bin/grit submit <project> -note "..."
```

Set `GRIT_REVIEWER_KEY` on the server to lock the review pages (reviewers sign in at `/review/login?key=...`). Without it the server runs in demo mode and review pages are public.

To watch the dashboard move, run `grit watch -every 2s` and, in another terminal, `grit demo-live`, which replays a fake Claude Code session into the transcript folder a turn at a time.

### Hook recipe (Cursor and others)

Point the harness's prompt hook at `grit hook -harness cursor -kind prompt` and its stop hook at `grit hook -harness cursor -kind stop`. grit reads the hook's JSON from stdin and understands Claude Code, Cursor and generic field names.

## Hosting cost

Hackatime's last 30 days: 15,488 users, 109,759 hours. If a typical session is 30 to 60 minutes, that is roughly 150,000 to 250,000 sessions a month. A synced session is a few kilobytes of JSON (turn timings, no text), so about 1 GB of new data a month, and submission bundles add text only for submitted projects. That fits on one small VPS with SQLite in WAL mode; the bounty ceiling is $500 a month and this should run for well under $50. To be proven with a load test before submission.

## Layout

| Path | What |
|---|---|
| `cmd/grit` | Client CLI |
| `cmd/gritd` | Server |
| `internal/collect` | Harness collectors: Claude Code, Codex, hooks |
| `internal/score` | Credited time rules and cross-session dedupe |
| `internal/scan` | Collect, link git, score, dedupe (with a parse cache for `watch`) |
| `internal/gitinfo` | Repo, remote and commits for a working directory |
| `internal/redact` | Secret scrubbing for submissions |
| `internal/store` | SQLite schema and queries |
| `internal/server` | HTTP API, dashboard, review pages, live updates |
| `internal/demo` | Generated example builders for the demo server |

## Known gaps

- **Scripted prompts.** Someone can still script a prompt every few minutes. Flags and the review view make that visible, and v2 adds an OS idle signal (keyboard and mouse activity, counts only, never keystrokes) to close most of it.
- **Accounts.** Signup is open with an API key. Real deployment needs Hack Club sign-in.
- **Autostart.** `grit watch` has to be started by hand; `grit install` (Task Scheduler, launchd, systemd user unit) is next.
- **Codex parser** is tested against the documented rollout format, not yet against a live install.
