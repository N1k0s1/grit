# grit

Time tracking for building with AI. grit counts the time you spend steering an agent, not the time the agent runs.

Built for the Hack Club AI time tracking bounty. Licensed under AGPLv3.

## Why

An agent can run for an hour while you make dinner. Wall-clock time and agent time say nothing about how hard someone worked. What every harness does record is when the human spoke and when the agent stopped. grit credits time only where those timestamps show a person engaged, and writes down the rule behind every credited minute so a reviewer can check it.

## How it works

1. **Collect, passively.** Claude Code (`~/.claude/projects/*/*.jsonl`) and Codex (`~/.codex/sessions/**/rollout-*.jsonl`) already save full session transcripts. grit reads them. Nothing is installed into the harness. Tools without local logs (Cursor, Windsurf, Copilot, browser chat) pipe their hook payload into `grit hook`, which appends to `~/.grit/events.jsonl`. A new harness is one `Collector` in `internal/collect` (see [CONTRIBUTING.md](CONTRIBUTING.md)).
2. **Measure presence, never content.** `grit watch` asks the OS every 15 seconds how long it has been since the last keyboard or mouse input. It never sees which keys or where the mouse went. The minute of the last input is marked in a 180 byte bitmap per UTC day under `~/.grit/activity`, next to a second bitmap of minutes the sampler was running, so "no input" and "no data" stay different.
3. **Score.** `internal/score` splits a session into turns (prompt, then agent run). The v1 windows decide what could count:
   - the gap between the agent finishing and the next prompt, if under 10 minutes (reading, testing, deciding); after a longer gap, only a typing estimate for the prompt;
   - at most 90 seconds before bare go-aheads like "continue" or "yes";
   - the agent run itself (up to 8 minutes) only if the human came back within 2 minutes of it finishing, otherwise just the first minute;
   - a 2 minute review tail after the last turn.

   With activity data for at least half the session (v2), only minutes with input inside those windows count, and a supervised run counts only if there was input during it or just after it. A prompt nobody typed earns nothing. Without activity data the v1 rules apply and the session is marked lower trust. Every credited interval carries the rule that produced it, and `grit explain` prints them. Sessions running at the same moment are deduplicated, so five agents in parallel still count as one person.
4. **Hand-written code.** `grit watch` also polls the working trees of projects with a session in the last week. A file written while an agent run was going, or soon after the agent named it in a tool call, is the agent's. Otherwise it is hand-written only if there was keyboard or mouse input that minute, and it credits the minutes with input in the 5 minutes before it as a `manual` session. Agent and hand edits are counted separately per session.
5. **Link to git.** Each session's working directory resolves to a repo, remote and the commits made during it.
6. **Sync numbers, prove timing.** `grit sync` sends only numbers: start, end, credited seconds, prompt and tool counts, rule strings. No prompt text, file names or commands. Each turn also carries one link of a hash chain over (prompt time, kind, sha256 of the redacted prompt), salted with a per-session secret so short prompts cannot be guessed from the hash. The server records when it first saw each link.
7. **Submit evidence.** `grit submit PROJECT` uploads that project's sessions with prompt text, with API keys, tokens, emails and passwords scrubbed, plus each session's salt. The server recomputes the chain: turns whose links it saw within 15 minutes of happening are **witnessed**, later ones **backfilled**, and a prompt that does not match the first link the server saw fails. `grit submit -dry` prints exactly what would be sent.
8. **Flags for reviewers.** Sessions are flagged `autopilot`, `mostly-agent`, `long-idle`, `regular-timing` (prompts spaced almost exactly evenly), `repeated-prompts` (one template sent over and over), `no-input-prompts` (prompts that arrived with no keyboard or mouse input) and `no-output` (an hour or more with no file touched or commit). Flags never change credited time. The review page recomputes them from the submitted prompts and shows any the client left out.
9. **Trust tier.** Each session is **high** (witnessed live and scored with activity data), **medium** (one of the two) or **low** (neither). Profiles, session pages and the review page show it.
10. **Server.** `gritd` is one Go binary with SQLite: accounts, public profiles, projects, git links, a leaderboard and profiles that update live over server-sent events, plus a review queue where a reviewer reads every prompt and approves, deflates or rejects.

## What is verified where

| Part | Status |
|---|---|
| Claude Code collector | Verified on a real install (macOS, 19 Nov 2026). |
| Codex collector | Verified on a real install: 53 real rollouts on this Mac gave plausible prompts, agent time and credit. |
| Hook collector | Fixture only. |
| Activity signal, macOS | Verified on this Mac (Apple Silicon): CoreGraphics `CGEventSourceSecondsSinceLastEventType` through purego, built with `CGO_ENABLED=0`. The call returned real idle times at once and nothing blocked waiting for permission. No Input Monitoring or Accessibility prompt is expected for this call; a person still has to confirm by eye that none appears. The `ioreg` fallback was checked to print `HIDIdleTime` on the same Mac. |
| Activity signal, Windows | Compiles under `CGO_ENABLED=0` (CI). Not yet run on a Windows machine. |
| Activity signal, Linux | Compiles under `CGO_ENABLED=0` (CI). X11 and GNOME Mutter paths not yet run on a Linux desktop. |
| Scoring v2 | Table tests. Run on this Mac against real sessions with the real sampler: a prompt sent by another program, with nobody at the keyboard, earned nothing. |
| Hand-written code | Table tests and an end-to-end scan test. On this Mac `grit watch` logged a README change made by an agent and attributed it to that agent session; the hand-written path is fixture only so far. |
| Attestation and trust tiers | Unit, store and HTTP tests, plus the demo server. |
| `install.sh` | Downloaded and verified `grit-darwin-arm64` from the real v0.1.0-rc.2 release on this Mac, and refused a binary with a wrong checksum. `grit install` itself not yet run on a real Mac (see below). |
| `install.ps1` | PSScriptAnalyzer clean in CI. Not yet run on Windows. |
| `grit install` and `uninstall` | Tests on each OS's CI runner (the macOS plist passes `plutil -lint`; the Windows test writes and removes a real HKCU value). Not yet run for real on a Mac, including log out and in. |
| `grit daemon` | Run on this Mac with real logs and on a synthetic 200 MB transcript (numbers above). |
| `grit login` | Run end to end on this Mac with a real browser against a local server. |
| Local dashboard | Run on this Mac with real data; every page checked in a browser. |
| `grit doctor` | Run on this Mac. |

## Install

macOS and Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/N1k0s1/grit/main/scripts/install.sh | sh
```

Windows (PowerShell):

```powershell
irm https://raw.githubusercontent.com/N1k0s1/grit/main/scripts/install.ps1 | iex
```

Both download the release binary for your machine, refuse to continue unless its sha256 matches the release's `checksums.txt`, and run `grit install`, which copies grit to `~/.grit/bin` and starts `grit daemon` at every login (a LaunchAgent on macOS, an XDG autostart entry on Linux, a `HKCU\...\Run` value on Windows). `grit uninstall` removes all of that; `grit uninstall -purge` also deletes `~/.grit`.

Then:

```bash
grit login -server https://your-grit-server   # opens your browser
grit doctor                                   # what grit can see on this machine
grit open                                     # the local dashboard
grit exclude add ~/private-stuff              # never read, walk or sync a folder
```

The daemon samples activity, watches recently active projects for edits, reads only new lines of your AI tools' logs, and syncs numbers when you are logged in. Without a login, everything stays on your machine.

The local dashboard runs on 127.0.0.1 only, behind a random token. It shows today live, a weekly goal, every session with a full replay of your own prompts (never uploaded), a weekly recap, privacy controls (pause, excluded folders, and every sync and submission byte for byte) and a submit flow with a redacted preview.

### Daemon footprint

`scripts/bench-daemon.sh` runs the daemon on a synthetic 200 MB Claude Code transcript in a throwaway home folder. On an Apple Silicon Mac (19 Nov 2026):

| | |
|---|---|
| First read of 200 MB | about 1 second |
| Physical footprint (Activity Monitor) | 20 MB |
| RSS reported by `ps` | 30 MB (it still counts memory already handed back to macOS) |
| Idle CPU | 0.03% |

## Develop

Needs Go 1.26 or newer. Both binaries are pure Go (no cgo), so they cross-compile to Windows, macOS and Linux. CI runs `go vet` and `go test -race` on Linux, macOS and Windows, lints the install scripts, and a `v*` tag publishes the six `grit` binaries with a `checksums.txt`.

```bash
go test ./...
go build -o bin/ ./cmd/...

./bin/gritd -demo                       # http://localhost:8080 with generated example builders
./bin/grit login -server http://localhost:8080
./bin/grit                              # today and this week, offline
./bin/grit activity                     # is the idle signal working here?
./bin/grit sessions                     # every session with credited time
./bin/grit explain <id>                 # how each minute was counted
./bin/grit sync                         # send numbers
./bin/grit daemon                       # what grit install starts at login
./bin/grit submit <project> -dry        # see the review bundle
./bin/grit submit <project> -note "..."
```

Set `GRIT_REVIEWER_KEY` on the server to lock the review pages (reviewers sign in at `/review/login?key=...`). Without it the server runs in demo mode and review pages are public.

To watch the dashboard move, run `grit daemon -every 2s` and, in another terminal, `grit demo-live`, which replays a fake Claude Code session into the transcript folder a turn at a time.

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
| `internal/activity` | Seconds since last input per OS, and the per-minute sampler |
| `internal/edits` | Working tree polling and agent versus hand attribution |
| `internal/attest` | Hash chain attestation of turns |
| `internal/autostart` | Start at login per OS (LaunchAgent, XDG autostart, HKCU Run) |
| `internal/webstatic` | Stylesheet and icon shared by the server and the local dashboard |
| `internal/score` | Credited time rules, anomaly flags and cross-session dedupe |
| `internal/scan` | Collect, link git, score, dedupe (with a parse cache for `watch`) |
| `internal/gitinfo` | Repo, remote and commits for a working directory |
| `internal/redact` | Secret scrubbing for submissions |
| `internal/store` | SQLite schema and queries |
| `internal/server` | HTTP API, dashboard, review pages, live updates |
| `internal/demo` | Generated example builders for the demo server |

## Known gaps

- **Scripted input.** The activity signal catches prompts sent with nobody at the keyboard. Someone who scripts prompts and also jiggles the mouse is caught only by the timing and repetition flags and by a reviewer reading the prompts.
- **Hand edits are judged by the minute.** A person typing in one file while an agent runs in the same project is counted as agent time, on purpose.
- **Witnessing needs the daemon running.** Sessions synced by hand later are backfilled.
- **The daemon watches every project you use an agent in.** Exclude private folders with `grit exclude add` or on the dashboard's privacy page.
- **Windows shows a console window for a moment at login** before the daemon hides it.
- **Accounts.** Signup is open with an API key. Real deployment needs Hack Club sign-in (Phase 4).
