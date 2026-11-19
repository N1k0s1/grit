# Progress

## Done

- Repo split out of the Personal workspace with its history and pushed to https://github.com/N1k0s1/grit (public).
- Workspace set up: CLAUDE.md, PLAN.md, TODO-NIKO.md, this file.
- Baseline on 19 Nov 2026 (Go 1.27.4, darwin/arm64): `go vet` clean; `go test` and `go test -race` pass, 9 tests across collect, redact and score. `CGO_ENABLED=0` builds succeed for windows, darwin and linux on amd64 and arm64.
- **Phase 0.3, CI.** `.github/workflows/ci.yml`: an em and en dash check, `go vet` and `go test -race` on ubuntu, macos and windows, `CGO_ENABLED=0` builds of both commands for six targets, and on a `v*` tag a release with six `grit` binaries and `checksums.txt`. Green on all three runners. The test tag `v0.1.0-rc.2` produced a prerelease with the seven files. Four Phase 1 commits failed on windows-latest before the fix in `fbfe435` and `b5b2479` (git's forward slash roots and unclosed test databases); everything after is green.
- **Phase 0.4.** CONTRIBUTING.md with a step by step guide to adding a collector.
- **Phase 1.1, activity signal.** `internal/activity`: macOS through CoreGraphics and purego (ioreg fallback), Windows through `GetLastInputInfo`, Linux through X11 MIT-SCREEN-SAVER or GNOME Mutter over D-Bus, anything else unknown. `grit activity` shows the signal.
- **Phase 1.2, sampler.** Every 15 seconds, the minute of the last input goes into a 180 byte bitmap per UTC day in `~/.grit/activity`, with a second 180 byte bitmap of minutes the sampler ran. `grit watch` runs it.
- **Phase 1.3, scoring v2.** `score.ScoreWith` credits only minutes with input inside the v1 windows when the sampler saw at least half the session. Every interval carries its rule. All original scoring tests pass unchanged.
- **Phase 1.4, hand-written code.** `internal/edits` polls recently active git roots, attributes each change to the agent, a person or unknown, and turns hand edits into `manual` sessions. 20,000 files cost about 0.6 MB of heap.
- **Phase 1.5, attestation.** `internal/attest`: a salted hash chain over turns, recorded with first-seen times by the server, verified on review. Tampered, reordered, moved, dropped and rewritten turns fail; late syncs are backfilled; live syncs are witnessed.
- **Phase 1.6, anomaly flags.** `regular-timing`, `repeated-prompts`, `no-input-prompts`, `no-output`. Flags only; a test checks credited time is identical with and without them. The review page recomputes them server side.
- **Phase 1.7, trust tiers.** high, medium or low per session, stored at sync, shown on profiles, session pages and review.
- Tests now cover activity, attest, collect, edits, redact, scan, score, server and store.

## Verified on a real machine

This Mac (Apple Silicon, macOS), 19 Nov 2026:

- Activity signal: CoreGraphics through purego works with `CGO_ENABLED=0` and returned real idle times at once. Nothing blocked on a permission prompt. A person still has to look at the screen once to confirm no dialog appears (see TODO-NIKO.md). The ioreg fallback prints `HIDIdleTime` here.
- Sampler: ran for about 45 minutes with `grit activity -sample` and then `grit watch`, writing 180 byte day files.
- Scoring v2 on real sessions: this agent session, started by another session with nobody at the keyboard, was credited 29 seconds under v2 (its first prompt earned nothing because nobody typed it) and flagged `mostly-agent`.
- Edits: `grit watch` logged a README change made by this agent and attributed it to the agent session (1 agent edit, 0 hand).
- Collectors: Claude Code and Codex both read real logs on this Mac with plausible numbers. Codex is no longer fixture only.

## Fixture only

- Hook collector.
- Activity signal on Windows and Linux (compiles under `CGO_ENABLED=0` in CI, never run on those systems).
- The hand-written path of the edits classifier (no person typed during this session).

## Blocked

- The shell's `GITHUB_TOKEN` is stale. Pushing works with `env -u GITHUB_TOKEN git push`. See TODO-NIKO.md.

## Next

Phase 2, starting with the daemon (2.3) and an exclude list for the edits watcher (see the decision below about which folders it walks).

## Decisions

- The repo split ran inside the new repo (fetching the branch from Personal) because `git subtree split` refuses a prefix that is not in the current working tree, and checking out in Personal was off limits.
- Removed `docs/1-leaderboard-live.png` from all history before the first push. It showed a real synced account and a private project name. Screenshots must come from the demo seed only (Phase 5.3 retakes them).
- Release assets are the six `grit` client binaries plus `checksums.txt`, the seven files PLAN.md asks for. `gritd` is cross-compiled in CI but deployed from source, so it is not a release asset.
- The go directive is 1.26 because current `golang.org/x/sys` needs it.
- Activity days are UTC, so a minute means the same on client and server and daylight saving never bends a day.
- The sampler marks only the minute of the last input it sees, so it can under-count input between two samples but never invents any.
- "Sampler running" is a second 180 byte file (`YYYY-MM-DD.seen`) rather than a wider day file, which keeps the day file at the 180 bytes PLAN.md asks for and keeps "no input" separate from "no data".
- v2 applies when the sampler saw at least half of a session's minutes. Inside a v2 session, minutes it did not see count as no input (a closed lid is not supervision). Below half, v1 applies and the session has no activity signal.
- A supervised run under v2 counts in full if there was input anywhere from its prompt to two minutes after it finished; otherwise nothing. An unsupervised run's first minute counts only where there was input.
- Polling modification times replaces fsnotify for 1.4. On macOS fsnotify uses kqueue, which needs an open file descriptor per watched file and fails on large repos; FSEvents needs cgo. Polling is the same on every OS and costs about 30 bytes of heap per file.
- Hand edits: a change is the agent's during a run, up to a minute after it, or within three minutes after the agent named that file. Otherwise hand-written only with input that minute, else unknown. Each hand edit credits input minutes in the five minutes before it; edits over 30 minutes apart start a new manual session. Rule strings never name files, because they are synced.
- The edits watcher walks only real git roots and never the home folder. It walks every project with an agent session in the last week, which on this Mac included repos this agent must not list (Personal and KiwiHacks): the local check stat'ed files there for a few minutes before it was stopped. No contents were read and nothing from them was logged. An exclude list belongs in Phase 2.5.
- Attestation hashes the redacted prompt, so the server can recompute the chain from a submission without ever seeing unredacted text. If redaction rules change between sync and submission, affected turns fail and a reviewer decides.
- Witness window 15 minutes, clock skew allowance 5 minutes.
- A turn verifies only if it matches the first link the server ever saw at that index, so a rewritten session cannot launder itself by bringing new links inside the submission.
- repeated-prompts needs the repeat to be at least 30% of prompts as well as three times, after the demo showed a normal session tripping it.
- Hand-written sessions have no prompts to attest, so at best they are medium trust.
