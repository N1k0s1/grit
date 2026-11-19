# grit roadmap

The goal: the most polished, trustworthy entry for Hack Club's AI time tracking bounty. A working MVP exists (see README.md). This file is the plan from MVP to submission.

Tags:
- **[core]** is the trust core. Judges will poke at it, and a wrong answer here can still pass tests. Written or reviewed line by line by the lead.
- **[ticket]** is well specified and checkable by its tests. Fine to hand to any contributor or model as one PR.

Every item lists "done when". An item is not done until that is true and `go vet ./...` and `go test ./...` pass.

---

## Phase 0: Repo

| # | Item | Tag | Done when |
|---|---|---|---|
| 0.1 | Repo split out with history, pushed to `github.com/N1k0s1/grit`. | | `git log` shows the MVP commit on origin/main. |
| 0.2 | Read the whole codebase and run `go test ./...`. | | Baseline test result recorded in PROGRESS.md. |
| 0.3 | GitHub Actions: `go vet`, `go test -race` on ubuntu, macos and windows; `CGO_ENABLED=0` builds for windows, darwin, linux on amd64 and arm64. On a `v*` tag, publish a release with the six binaries and a `checksums.txt` (sha256). | [ticket] | A push runs green on all three runners; a test tag produces a release with seven files. |
| 0.4 | CONTRIBUTING.md: how to add a collector (interface, fixture, test, README row). | [ticket] | A new contributor could add a collector from it alone. |

## Phase 1: Trust and accuracy core

| # | Item | Tag | Done when |
|---|---|---|---|
| 1.1 | `internal/activity`: seconds since last keyboard or mouse input, never content. One file per OS behind build tags, pure Go. Windows: `GetLastInputInfo` via `windows.NewLazySystemDLL("user32.dll")` minus `GetTickCount`. macOS: `CGEventSourceSecondsSinceLastEventType` via `github.com/ebitengine/purego`, falling back to `ioreg -c IOHIDSystem` `HIDIdleTime`. Linux: X11 via `github.com/jezek/xgb/screensaver` QueryInfo, GNOME Wayland via `org.gnome.Mutter.IdleMonitor.GetIdletime` over `github.com/godbus/dbus`, else "unknown". | [core] | Works on this Mac; README records whether macOS shows any permission prompt (Input Monitoring or Accessibility). Windows and Linux compile under `CGO_ENABLED=0`. |
| 1.2 | Sampler: every 15 seconds into a per-day bitmap of active minutes at `~/.grit/activity/YYYY-MM-DD` (1440 bits). | [core] | Unit tests for the bitmap; a day file is 180 bytes. |
| 1.3 | Scoring v2 in `internal/score`: with activity data, gaps between turns count only for minutes with input; supervised agent time needs input during or just after the run. Without activity data, v1 rules apply and the session is marked lower trust. | [core] | Every existing test passes unchanged; new table tests cover input present, absent, partial, and no data. Every credited second has a rule string. |
| 1.4 | Manual coding time: fsnotify on working trees of recently active projects (ignore `.git`, `node_modules`, build output). Changes with no matching agent tool-call file nearby in time, during active-input minutes, count as hand-written. Human and AI edits shown separately. | [core] | Tests with synthetic tool calls and file events; watcher stays under the RAM budget on a large repo. |
| 1.5 | Live attestation (`internal/attest`): each sync sends a hash chain over turns (timestamp, kind, sha256 of prompt text). Server records when it first saw each link. On submission it recomputes from the bundle and compares. Turns first seen within minutes of happening are "witnessed", others "backfilled". | [core] | Tests: tampered prompt fails, reordered turn fails, late sync is backfilled, live sync is witnessed. |
| 1.6 | Anomaly signals for reviewers: suspiciously regular prompt timing, duplicate or templated prompts, prompts typed faster than the activity signal allows, long sessions with no file changes or commits. Flags only, never removes time. | [core] | One table test per signal, plus a test that flags change no credited seconds. |
| 1.7 | Trust tier per session: witnessed and activity signal is high, one of them is medium, neither is low. Shown on profiles and in review. | [core] | Tier stored per session and visible on both pages. |

## Phase 2: Install and the local app

| # | Item | Tag | Done when |
|---|---|---|---|
| 2.1 | `scripts/install.sh` and `scripts/install.ps1`: detect OS and arch, download the release binary, verify the checksum, run `grit install`. | [ticket] | Works against a real tagged release on macOS; ps1 passes PSScriptAnalyzer. |
| 2.2 | `grit install` and `grit uninstall`, by hand, no autostart library. Windows: `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` via `golang.org/x/sys/windows/registry`. macOS: LaunchAgent plist in `~/Library/LaunchAgents`, loaded with `launchctl`. Linux: XDG autostart `.desktop`. | [ticket] | macOS tested for real, including after log out and in, then uninstalled cleanly with nothing left behind. |
| 2.3 | `grit daemon`: sampler, file watcher, collectors, sync loop and local dashboard in one process. Incremental parsing (byte offset per file, read only new lines). | [core] | With a 200 MB synthetic transcript: under 30 MB RAM and under 1% CPU idle. Numbers recorded in README. |
| 2.4 | `grit login`: opens the browser, server returns the API key to a localhost callback. | [core] | Callback only on 127.0.0.1, one-time state parameter checked. |
| 2.5 | Local dashboard on 127.0.0.1 with a random token: today live, sessions with own prompts and full replay (never uploaded), weekly recap, goals, privacy controls (pause, exclude project, see exactly what each sync sent), submit flow with redacted preview. | [ticket] | Not reachable from another machine; every request without the token is refused. |
| 2.6 | `grit doctor`: tools found, session counts per tool, activity signal status, daemon and autostart status. | [ticket] | Clear output on this Mac. |

## Phase 3: Collectors

One collector per tool in `internal/collect`, each with a synthetic fixture test and a README row marked "verified on a real install" or "fixture only". If a format cannot be confirmed, ship a hook recipe instead of a guessed parser. All are **[ticket]**.

| # | Tool | Source |
|---|---|---|
| 3.1 | Cursor | Hooks first: `~/.cursor/hooks.json` with `beforeSubmitPrompt`, `afterAgentResponse`, `afterFileEdit`, `preToolUse`, `postToolUse`, `stop`. Stdin JSON has `conversation_id`, `generation_id`, `model`, `workspace_roots`, `transcript_path`, no timestamp, so stamp time when the hook runs. `grit install` offers to add hooks without clobbering existing ones. Backfill: `globalStorage/state.vscdb`, table `cursorDiskKV`, keys `composerData:<id>` and `bubbleId:<composerId>:<bubbleId>`; bubbles have `createdAt`, type 1 user, 2 assistant, `toolFormerData` for tool calls. Open read-only with modernc SQLite. |
| 3.2 | Copilot Chat (VS Code) | `<UserData>/User/workspaceStorage/<hash>/chatSessions/<id>.jsonl` mutation log: kind 0 initial state, 1 set path, 2 append to array, 3 delete. Replay to rebuild. `requests[]` have `timestamp` (epoch ms), `message.text`, `modelId`, response parts (kind `toolInvocationSerialized`), `modelState.completedAt`. Older versions: plain `.json`. `workspace.json` in the same folder gives the project folder. |
| 3.3 | Gemini CLI | `~/.gemini/tmp/<projectId>/chats/session-*.jsonl` (older `.json`). Metadata record has `directories[]`; messages have `timestamp` and type `user` or `gemini`; gemini messages have `model` and `toolCalls[]` with timestamps. `~/.gemini/projects.json` maps id to folder. Hooks also exist (`BeforeAgent`, `AfterAgent`). |
| 3.4 | Cline and Roo Code | `<UserData>/User/globalStorage/saoudrizwan.claude-dev/tasks/<taskId>/ui_messages.json` and `rooveterinaryinc.roo-cline`. Entries have `ts` (epoch ms); say `task` and say `user_feedback` are human, everything else is agent. Check every VS Code fork's user data folder (Code, Cursor, Windsurf, VSCodium). |
| 3.5 | Aider | `.aider.input.history` in a project root, a `# <timestamp>` line before each prompt. Scan roots of known projects. |
| 3.6 | OpenCode | Inspect tables in `~/.local/share/opencode/opencode.db`, plus the older `storage/` JSON layout. Fixture only unless installed. |
| 3.7 | Windsurf | Hooks only: `~/.codeium/windsurf/hooks.json` with `pre_user_prompt` and `post_cascade_response`. |

Skipped for now: Continue and Zed (no per-message timestamps).

## Phase 4: Server and public site

| # | Item | Tag | Done when |
|---|---|---|---|
| 4.1 | Sign-in is Hack Club Account only: OIDC at `https://auth.hackclub.com` (discovery at `/.well-known/openid-configuration`), scopes `openid profile email slack_id verification_status`, via `github.com/coreos/go-oidc/v3` and `golang.org/x/oauth2`. Store `sub`, name, `slack_id`, `ysws_eligible`; show a "verified Hack Clubber" mark. Never request or store a birthdate. Remove open signup. `-dev-login` flag fakes sign-in for local dev and the demo. Client ID and secret from `.env`. | [core] | State and nonce checked; dev login refuses to start without `-demo` or a localhost bind. |
| 4.2 | Optional "link GitHub" in settings, only to verify a project's repo belongs to the user. Never a login. | [ticket] | Linked repos show a verified mark. |
| 4.3 | Profiles: public by default with a private toggle, year heatmap, streak, weekly goal, top tools, trust-tier mix. | [ticket] | Private profiles return 404 to others. |
| 4.4 | Projects: description, verified repo link, demo link, shipped status, commits, SVG badge at `/badge/<user>/<project>.svg`. | [ticket] | Badge renders in a GitHub README. |
| 4.5 | Review: reviewer and admin roles, queue filters (unreviewed, flagged, low trust), session replay with a scrubber, attestation and anomaly panels, decision log (who changed what, when), JSON export of approved hours per project. | [core] for roles and log, [ticket] for UI | Every decision is logged and cannot be edited after the fact. |
| 4.6 | Program API: `GET /api/v1/projects/<id>/hours` returning credited time, approved time and trust mix. | [ticket] | Documented with an example response. |
| 4.7 | Pages: landing, install (detects OS), how it counts, privacy, FAQ, collector docs, settings (API keys, export everything, delete account). Light and dark, no horizontal scroll at phone width. | [ticket] | Checked at 375 px and desktop in both themes. |
| 4.8 | Last, off by default: Hackatime bridge sending credited intervals as heartbeats with AI fields (`ai_model`, `ai_session`, `ai_prompt_length`, `ai_line_changes`, `human_line_changes`, category "ai coding"), key from `~/.wakatime.cfg`. | [ticket] | Disabled unless a flag is set. TODO-NIKO.md says check with the Hackatime team first. |

## Phase 5: Scale and polish

| # | Item | Tag | Done when |
|---|---|---|---|
| 5.1 | Load test at Hackatime's numbers (15,488 monthly users, 109,759 hours a month, active users syncing every 30 seconds) against the server on this Mac. | [core] | README has CPU, RAM, disk and projected monthly cost on a small VPS. Ceiling is US$500 a month. |
| 5.2 | `deploy/`: systemd unit, Caddyfile, nightly SQLite backup script. Not deployed. | [ticket] | Files lint; backup script tested locally. |
| 5.3 | Polish: Playwright screenshots of every page at desktop and phone width, fix what looks off, README gets screenshots, install steps and an honest "known gaps" section. | [ticket] | Screenshots in `docs/`, no layout bugs left. |
