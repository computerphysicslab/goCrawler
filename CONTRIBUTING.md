# Contributing to goCrawler

Thanks for taking the time to improve goCrawler. This project keeps a deliberately small surface:
a single Go binary, an embedded loopback-only web Workbench, and zero required external services.

## Getting started

1. Install **Go 1.26 or newer** (`go.mod` currently pins `go 1.26.0` and toolchain `go1.26.11`).
2. Clone the repository and run:

   ```sh
   make build
   make test
   make selftest
   ```

3. For the Workbench, start `./crawler -gui` and open <http://127.0.0.1:8765>.
4. Run commands from the module root: `go run crawler.go` resolves the `goCrawler/...` imports
   from the current directory and fails elsewhere with `cannot find package goCrawler/iolib`.
   From any other directory use `./run.sh <args>`, which changes to the module root first. A
   compiled binary autodetects the root when `crawler.yaml` is missing from the working directory.

The default cache is local SQLite (`./cache/cache.db`); Redis is optional. `profiles/*.yaml` are
gitignored because they contain research-specific seeds — put shareable examples in
`profiles/examples/` instead.

## Before opening a pull request

- Keep code, comments and repository documentation in **English**.
- Run `gofmt -w .`, `go vet ./...`, `go test ./...` and `go test -race ./...`.
- Add or update tests for behaviour changes; the Workbench has HTTP-level tests in `gui_test.go`.
- Update `CHANGELOG.md` and, when work comes from the roadmap, move the item to the version table
  in `roadmap.md`.
- Do not commit secrets, local profiles, logs, cache databases or build artifacts.
- Keep changes focused. If a refactor is needed for a feature, mention it in the PR description.

## Commit and PR style

- Use short imperative commit messages, e.g. `Add PDF page-limit validation`.
- Reference the issue or roadmap ID when applicable (`P52`, `Fixes #12`).
- Describe **what changed and why**, how you tested it, and any behaviour that is intentionally
  opt-in (new flags default to off whenever possible).

## Good first issues

Items marked **S** in section 1 of `roadmap.md` are good starting points. They usually touch one
area (CLI config, GUI CSS/JS, docs or tests) and need no architectural changes.

## Reporting bugs

Use the bug report template and include the Go version, OS, profile (with seeds redacted if
private), the command or GUI action, and the relevant part of `logs/` or the Workbench event view.
Never paste API keys, webhook URLs or credentials.
