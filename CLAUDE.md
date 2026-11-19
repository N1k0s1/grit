# CLAUDE.md

Instructions for AI coding agents working on grit. Humans: see README.md and CONTRIBUTING.md.

If `CLAUDE.local.md` exists, read it too. It holds machine-specific rules that override this file.

## What grit is

An open source (AGPLv3) time tracker for teenagers who build with AI agents, written for Hack Club's AI time tracking bounty. It credits only human-steered time and records a rule string for every credited minute, so a reviewer can check every number.

- `grit` is the client: reads AI tool transcripts, scores sessions, syncs numbers, submits redacted prompt bundles.
- `gritd` is the server: Go plus SQLite, public profiles, projects, a live leaderboard over server-sent events, a reviewer queue.

The roadmap is `PLAN.md`. Status is `PROGRESS.md`. Things only the maintainer can do go in `TODO-NIKO.md`.

## Build and test

```bash
go vet ./...
go test ./...
go test -race ./...
CGO_ENABLED=0 go build -o bin/ ./cmd/...
./bin/gritd -demo        # http://localhost:8080 with generated data
```

Every commit must pass `go vet ./...` and `go test ./...`.

## Hard rules

1. **No cgo, ever.** `CGO_ENABLED=0` must build for windows, darwin and linux on amd64 and arm64. SQLite is `modernc.org/sqlite`. OS calls go through `golang.org/x/sys`, `purego` or plain syscalls behind build tags. The target user has a ten year old Windows laptop.
2. **No em dashes or en dashes anywhere.** Code, comments, docs, commit messages, UI text, test names. Use a comma, colon, full stop or brackets. Before every commit run:
   ```bash
   git grep -nI --untracked -e "$(printf '\342\200\223')" -e "$(printf '\342\200\224')" && echo "FIX DASHES" || echo ok
   ```
3. **Fixtures are synthetic.** Never copy real transcripts, prompts, file paths or usernames from a real AI tool log into the repo, a test, a commit or a screenshot. Real logs may be read locally to check a parser works, nothing more.
4. **Never commit secrets.** Credentials live in `.env` (gitignored).
5. **Proper writing.** Commit messages, docs and UI copy use full sentences and normal capitalisation.
6. **Scoring is explainable.** Every credited second carries a rule string. Never add a rule that credits time without one. Flags and anomaly signals mark sessions for reviewers; they never silently remove time.
7. **Privacy defaults.** `grit sync` sends numbers only. Prompt text leaves the machine only through `grit submit`, redacted, after a preview. Activity tracking records seconds since last input, never keystroke content.

## Layout

| Path | What |
|---|---|
| `cmd/grit` | Client CLI |
| `cmd/gritd` | Server |
| `internal/collect` | One collector per AI tool, plus the hook receiver |
| `internal/activity` | Seconds since last input per OS, and the per-minute sampler |
| `internal/edits` | Working tree polling and agent versus hand attribution |
| `internal/attest` | Hash chain attestation of turns |
| `internal/autostart` | Start at login per OS (LaunchAgent, XDG autostart, HKCU Run) |
| `internal/webstatic` | Stylesheet and icon shared by the server and the local dashboard |
| `internal/score` | Credited time rules, anomaly flags and cross-session dedupe |
| `internal/scan` | Collect, link git, score, dedupe |
| `internal/gitinfo` | Repo, remote and commits for a working directory |
| `internal/redact` | Secret scrubbing for submissions |
| `internal/store` | SQLite schema and queries |
| `internal/server` | HTTP API, pages, review, live updates |
| `internal/demo` | Generated example builders for the demo server |

## How to work

- Small commits, one idea each. Tests with the change, not after it.
- Table tests for scoring. Keep every existing scoring test passing; a changed expectation needs a reason in the commit message.
- When something is ambiguous, pick the simpler option and write the decision in `PROGRESS.md`.
- After each phase in `PLAN.md`, update `README.md` and `PROGRESS.md`: what is done, what is verified on a real machine, what is fixture only, what is next.
- A collector is "verified on a real install" only if it was run against a real install of that tool. Otherwise it is "fixture only". Say which in the README.
