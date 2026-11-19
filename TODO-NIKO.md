# Things only Niko can do

- [ ] Create the GitHub repo `N1k0s1/grit` (public, empty, no README) so main can be pushed.
- [ ] Fix the stale `GITHUB_TOKEN` in your shell profile. It overrides the working `gh` keyring login, so git and gh fail with "Bad credentials".
- [ ] Register the grit app at https://auth.hackclub.com/developer/apps and put the client ID and secret in `.env` as `HCA_CLIENT_ID` and `HCA_CLIENT_SECRET`. Redirect URL: `https://<domain>/auth/callback` (plus `http://localhost:8080/auth/callback` for local testing).
- [ ] Provide a VPS and a domain for deployment.
- [ ] Check with the Hackatime team before the Hackatime bridge is ever enabled.
