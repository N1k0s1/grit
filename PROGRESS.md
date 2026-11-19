# Progress

## Done

- Repo split out of the Personal workspace with its history and pushed to https://github.com/N1k0s1/grit (public).
- Workspace set up: CLAUDE.md, PLAN.md, TODO-NIKO.md, this file.

## Verified on a real machine

Nothing yet beyond the MVP's own claims in README.md.

## Fixture only

Codex collector (from the MVP).

## Blocked

- The shell's `GITHUB_TOKEN` is stale. Pushing works with `env -u GITHUB_TOKEN git push`. See TODO-NIKO.md.
- Go is not installed on the dev Mac yet.

## Next

Phase 0: install Go, baseline `go test ./...`, CI workflow, CONTRIBUTING.md. Then Phase 1.

## Decisions

- The repo split ran inside the new repo (fetching the branch from Personal) because `git subtree split` refuses a prefix that is not in the current working tree, and checking out in Personal was off limits.
- Removed `docs/1-leaderboard-live.png` from all history before the first push. It showed a real synced account and a private project name. Screenshots must come from the demo seed only (Phase 5.3 retakes them).
