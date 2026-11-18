## Summary

<!-- What changed and why? Reference the issue or roadmap ID, e.g. P52 / Fixes #12. -->

## Checklist

- [ ] `gofmt -w .` and `go vet ./...`
- [ ] `go test ./...` and `go test -race ./...`
- [ ] `go run crawler.go --selftest`
- [ ] Docs / CHANGELOG / roadmap updated when needed
- [ ] No secrets, local profiles, logs, cache files or binaries committed
- [ ] New behaviour is opt-in where practical (defaults unchanged)
