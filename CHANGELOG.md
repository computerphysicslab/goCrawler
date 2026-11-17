# Changelog

All notable changes to goCrawler are documented here. Versions follow semantic versioning and
correspond to Git tags. Dates are 2026-11-17 unless stated otherwise.

## [v1.5.0] - 2026-11-17

### Added
- Refactored `roadmap.md`: pending work ordered by increasing difficulty, completed work grouped by version.
- `docs/SENIOR_AUDIT.md`: senior-level audit with 21 new pending findings (P01–P21).
- `docs/GUI_ANALYSIS.md`: full design for the goCrawler Workbench GUI (profile editor, run monitor,
  curated-text viewer, keyword lab with feedback loop).
- This changelog and version tags.
- CI workflow for Linux, Windows and macOS; release workflow producing binaries and a Debian package.

### Changed
- Release assets now built for linux/amd64, linux/arm64, windows/amd64, darwin/amd64 and darwin/arm64.

## [v1.4.0] - 2026-11-17

### Added
- Full CLI flags: `-profile`, `-backend`, `-cache`, `-workers`, `-log-level`, `-max-links`,
  `-dry-run`, `-query`, `-explore`, `-generate-profile`, `-version`, `-license`.
- SQLite FTS5 full-text index over the curated corpus; `-query=TEXT` search with snippets.
- Multi-worker crawling (1–32) with per-area mutexes; race-detector tests pass.
- Profile keys `language`, `minLanguageConfidence`, `rankingStrategy`, `synonyms`, `maxLinksPerPage`.
- `-explore` corpus statistics and `-generate-profile=URL` starter profiles.
- Packaging drafts: `snap/snapcraft.yaml`, `debian/` skeleton, AppStream metadata, SVG icon and
  `.desktop` launcher; `docs/UBUNTU_SUBMISSION.md` records the distribution decisions.

## [v1.3.0] - 2026-11-17

### Added
- `docs/FUNCTION_ANALYSIS.md`: function-by-function review.
- Tests for `iolib`, `stringlib`, `corpusfreqlib`, n-grams, URL boundaries, cache state round-trip,
  session naming, fake cache store and a local HTTP server.

### Changed
- Toolchain upgraded to Go 1.26.11; `golang.org/x/net` v0.62.0 and `gopkg.in/yaml.v2` v2.4.0;
  `log/slog` from the standard library. `govulncheck` reports zero vulnerabilities.
- Configurable ranking strategies (`keywords`, `tfidf`, `hybrid`) with IDF weighting and synonym expansion.
- All regexes precompiled once; `doNextLink` split into seven focused helpers.
- Frequency dump now forced at the end of a run; English corpus loaded from a compact JSON index.
- `run() error` replaces panic/os.Exit paths; crawl state has a version field; corrupt cache JSON is
  discarded and re-downloaded; O(1) link index.

## [v1.2.0] - 2026-11-17

### Added
- MIT `LICENSE`.
- Structured logging with `slog`, levels configurable via `logLevel`, and rejection counters by reason.
- SQLite-backed `--resume` for link pool, counters, signatures and frequencies.
- Ubuntu readiness roadmap and vulnerability-remediation analysis.

## [v1.1.0] - 2026-11-17

### Added
- `robots.txt` enforcement with per-host fetch/cache, User-Agent, `Crawl-delay` and fail-open.
- Profile resolution from `profiles/` with fallback to the working directory.
- Session output files named `<Profile>-YYYY-MM-DD-ZHH-MM-SS.txt` (UTC).
- English doc comments on every function and a `--selftest` binary mode.

## [v1.0.0] - 2026-11-17

### Added
- Local SQLite cache backend (`storelib`) with Redis kept as an option.
- Makefile with build/run/test/fmt/vet/selftest/clean targets.
- CLI argument validation, download-size cap, cross-run log deduplication.
- Refreshed README and function documentation.

## [v0.9.0] - 2023-05-10

- Original upstream goCrawler: single-file Go crawler, Redis-only cache, YAML keyword profiles,
  curated corpus and token-frequency outputs.
