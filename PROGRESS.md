# Progress

## Done

- Repo split out of the Personal workspace with its history (one commit: the MVP).
- Workspace set up: CLAUDE.md, PLAN.md, TODO-NIKO.md, this file.

## Verified on a real machine

Nothing yet beyond the MVP's own claims in README.md.

## Fixture only

Codex collector (from the MVP).

## Blocked

- Push: the GitHub repo does not exist yet and the shell's `GITHUB_TOKEN` is stale. See TODO-NIKO.md.
- Go is not installed on the dev Mac yet.

## Next

Phase 0: install Go, baseline `go test ./...`, CI workflow, CONTRIBUTING.md. Then Phase 1.

## Decisions

- The repo split ran inside the new repo (fetching the branch from Personal) because `git subtree split` refuses a prefix that is not in the current working tree, and checking out in Personal was off limits.
