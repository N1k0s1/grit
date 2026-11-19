# Things only Niko can do

- [x] Create the GitHub repo `N1k0s1/grit` (done 19 Nov 2026).
- [ ] Fix the stale `GITHUB_TOKEN` in your shell profile. It overrides the working `gh` keyring login, so git and gh fail with "Bad credentials".
- [ ] Register the grit app at https://auth.hackclub.com/developer/apps and put the client ID and secret in `.env` as `HCA_CLIENT_ID` and `HCA_CLIENT_SECRET`. Redirect URL: `https://<domain>/auth/callback` (plus `http://localhost:8080/auth/callback` for local testing).
- [ ] Provide a VPS and a domain for deployment.
- [ ] Check with the Hackatime team before the Hackatime bridge is ever enabled.
- [ ] On this Mac, run `grit activity` from Terminal and confirm no macOS permission dialog (Input Monitoring or Accessibility) appears. The agent could not see the screen.
- [ ] Once, with `grit watch` running, edit a file in a project by hand (no agent running) and check `grit sessions` shows a manual session with 1 hand edit. That is the one path of 1.4 not yet seen on a real machine.
- [ ] If you have access to a Windows laptop or a Linux desktop, run `grit activity` there and tell me what it prints.
- [ ] Decide which projects the edits watcher may walk. Today it walks every git repo with an agent session in the last week, including Personal and KiwiHacks. An exclude list is planned for Phase 2.5.
