# Dependency triage — Dependabot alerts (2026-11-23)

## Symptom

Pushing to `master` reports **12 Dependabot alerts (5 high, 7 moderate)** on
`computerphysicslab/goCrawler`.

## Evidence gathered

Querying the alerts with the authenticated GitHub CLI:

```sh
gh api repos/computerphysicslab/goCrawler/dependabot/alerts \
  --jq '.[] | [.dependency.package.name, .security_advisory.severity,
               (.security_advisory.vulnerabilities[]?.first_patched_version.identifier // "n/a")] | @tsv'
```

All alerts point to `golang.org/x/net` (declared in `go.mod` and hashed in
`go.sum`), plus `golang.org/x/sys`, `golang.org/x/text` and `gopkg.in/yaml.v2`
entries that exist only in `go.sum`.

The **first patched versions** of every advisory are old (the newest is
`x/net v0.38.0`). The versions actually selected by the build are:

| Module | Selected | Newest patched advisory |
|---|---|---|
| `golang.org/x/net` | **v0.62.0** | v0.38.0 |
| `golang.org/x/sys` | **v0.49.0** | pre-module commit |
| `golang.org/x/text` | **v0.43.0** | v0.3.8 |
| `gopkg.in/yaml.v2` | v2.4.0 | v2.2.8 |

Additional checks:

- `govulncheck ./...` → **No vulnerabilities found** (no vulnerable code path is reachable).
- `go mod tidy` → **no changes** to `go.mod` or `go.sum`; the historical `/go.mod`
  hashes are required by transitive dependency metadata, not selected builds.
- CI now runs `govulncheck ./...` on Linux (N04), so a real reachable regression fails the build.

## Conclusion

The alerts are **false positives caused by stale `/go.mod` hash lines in `go.sum`**:
Dependabot matches advisory ranges against every hashed version, while Go only
builds the selected versions listed above, all of which are already patched.

**Action:** no dependency upgrade or alert dismissal was performed. Upgrading
`viper v1.7.1` or `modernc.org/sqlite v1.28.0` would not remove these alerts and
would add unrelated regression risk. Re-run the `gh api` query and `govulncheck`
after any future dependency change; treat `govulncheck` as the authoritative gate.

Manual review of the GitHub Security tab is still advisable because the API
response does not expose a per-alert severity in the simplified view used here.
