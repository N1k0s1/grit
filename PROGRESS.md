# Progress

## Done

- Repo split out of the Personal workspace with its history and pushed to https://github.com/N1k0s1/grit (public).
- Workspace set up: CLAUDE.md, PLAN.md, TODO-NIKO.md, this file.
- Baseline on 19 Nov 2026 (Go 1.27.4, darwin/arm64): `go vet` clean; `go test` and `go test -race` pass, 9 tests across collect, redact and score. `CGO_ENABLED=0` builds succeed for windows, darwin and linux on amd64 and arm64. No tests yet for scan, store, server, gitinfo or the CLIs.

## Verified on a real machine

Nothing yet beyond the MVP's own claims in README.md.

## Fixture only

Codex collector (from the MVP).

## Blocked

- The shell's `GITHUB_TOKEN` is stale. Pushing works with `env -u GITHUB_TOKEN git push`. See TODO-NIKO.md.

## Next

Phase 0: CI workflow (0.3) and CONTRIBUTING.md (0.4). Then Phase 1.

## Decisions

- The repo split ran inside the new repo (fetching the branch from Personal) because `git subtree split` refuses a prefix that is not in the current working tree, and checking out in Personal was off limits.
- Removed `docs/1-leaderboard-live.png` from all history before the first push. It showed a real synced account and a private project name. Screenshots must come from the demo seed only (Phase 5.3 retakes them).
