## [v1.8.8] - 2026-11-18

### Added
- Final "About" tab in the Workbench: name, version, release date, copyright, MIT license, author
  and contact, repository/issue/changelog/roadmap links, Go version, platform, bundled components,
  and a locally generated QR code linking to the repository (`/api/about`, `github.com/skip2/go-qrcode`).

# Changelog

All notable changes to goCrawler are documented here. Versions follow semantic versioning and
correspond to Git tags. Dates are 2026-11-17 unless stated otherwise.

## [v1.8.7] - 2026-11-18

### Fixed
- URL Seeds accepts pasted Markdown links `[text](url)` in addition to plain URLs (GUI and API).
- Seed rewriting now removes all old seed lines before writing, preventing duplicate accumulation
  (profile had 105 lines for 73 unique URLs).

 - 2026-11-18

### Added
- Roadmap S items: CSS design tokens with dark/light toggle and responsive layout (P22/P31/P33),
  live KPIs URLs/s, average latency, queue depth and goroutines (P23), rejection bar chart (P35),
  and "Apply & Re-run" with confirmation in Keyword Lab (P41).

 - 2026-11-18

### Changed
- Renamed the `hazURL` tab to "URL Seeds" and added a live "Seeds in profile: N URLs" counter that
  refreshes after add/replace/remove. Duplicates and invalid URLs are ignored by the backend.

 - 2026-11-18

### Added
- `docs/GUI_MOCKUP_ANALYSIS.md`: assessment of the second GUI proposal; React/Vue explicitly rejected.
- Roadmap P31–P47 (design tokens, responsive layout, activity feed, charts, sidebar, structured
  editor, version history, corpus explorer, session comparison, command palette, exports and
  keyword impact analysis), merged into the effort-ordered backlog.

 - 2026-11-18

### Added
- `docs/GUI_STUDIO_ANALYSIS.md`: assessment of nine GUI proposals against the real embedded interface.
- Roadmap backlog P22–P30 (GUI Studio), sorted by increasing effort.

 - 2026-11-18

### Added
- Workbench metrics "Net reqs" (HTTP requests including robots.txt) and "Cache I/O" (cache
  read/write operations), with reset on each crawl and tests.

 - 2026-11-18

### Added
- Persistent `robots.txt` cache on disk (`cache/robots-cache.json`) with a seven-day TTL; rules and
  `Crawl-delay` survive process restarts. Stale entries are refetched automatically.

 - 2026-11-18

### Fixed
- Keywords removed in the Keyword Lab are now persisted as an `excludedKeywords` block in the
  profile and filtered out on every subsequent extraction, so they never reappear.
- Rejection reason rows in the dashboard now carry the click handler that opens the last 10 cases.

### Added
- Integration tests covering the full remove → apply → re-extract cycle and clickable rejection rows.

 - 2026-11-18

### Changed
- Prompt generator includes complete curated documents (no truncation) and explicitly requires that
  the entire report and all provided pages be written in English.
- Rejection counters now record the last 10 cases per reason (URL, reason detail, text excerpt).
  Dashboard rows are clickable and expand to show those cases with links opening in a new tab.

 - 2026-11-18

### Changed
- Keyword Lab: removing a keyword now marks it for deletion and `/api/keywords/apply` removes it
  (and its morphological family) from the profile.
- Applying keywords merges existing + selected terms, expands singular/plural/verb forms and writes
  both `linkOk` and `regexRankingKeywords`; stopwords are filtered out.

 - 2026-11-18

### Fixed
- Restored the missing closing brace in the Workbench `loadProfile()` JavaScript, which prevented
  tabs and dropdowns from working.

### Added
- Regression tests validating GUI JavaScript delimiter balance, all handler functions, and that
  profile selectors are dropdowns with case-insensitive last-profile selection.

 - 2026-11-18

### Fixed
- GUI crawl of `skystacker-research` crashed in `scoreWithRegex` when the profile regex has no capturing group.
- Profile resolution is now case/space-insensitive (session logs capitalise names).
- GUI no longer replaces the global crawler pointer while serving requests; state is reset in place.
- Prompt generator profile is a dropdown defaulting to the last executed profile.

### Added
- Regression tests for group-less regex scoring, profile resolution and state reset.

 - 2026-11-18

### Changed
- Keyword lab: remove button placed next to the word; applying switches back to the Profiles tab.
- Morphological grouping (Snowball stems): star/stars and image/images are one keyword with variants.

### Added
- Fifth tab "Prompt generator": builds an English research prompt from profile keywords, seed URLs
  and curated text excerpts (`/api/prompt`).

 - 2026-11-18

### Changed
- Workbench tabs reordered: Profiles (default), Crawler launcher, Keyword lab, Corpus search.
- On load, the last executed profile is selected automatically from session log filenames.
- Keyword lab: expanded English stopword list, significance-based ranking (corpus frequency vs BNC)
  with Count/Docs/Score columns and per-keyword removal before applying to the profile.

 - 2026-11-18

### Added
- Workbench dashboard: Start/Stop buttons now reflect run state, active crawl count, not-accepted
  pages and a ranked breakdown of rejection reasons.

 - 2026-11-18

### Fixed
- The Workbench now tries the next 20 ports when the default 8765 is already in use, and prints the actual URL.

 - 2026-11-18

### Added
- 65 tests including HTTP tests for every GUI endpoint and an end-to-end crawl with a local server.
- `corpusfreqlib.Init` now returns an error instead of calling `os.Exit(1)`; embeddable and testable.
- `guiHandler()` exposed for `httptest`; GUI crawls run in-process; profile POST and keyword apply fixed.
- Regression tests for configuration keys and the default GUI port.

### Changed
- `log/slog` grouped in a single import block; dead `GUIProcess`/`os/exec` removed.
- `go test -race ./...` passes across all packages.

## [v1.7.0] - 2026-11-17

### Added
- `Crawler` struct owning configuration, state, stores and mutexes; package-level mutable globals removed (P19).
- `run()` split into `prepareCLI`, `setupRuntime`, `crawlLoop` and `reportResults` (P16).
- `validateConfig()` with aggregated errors for lengths, thresholds, regexes, backend and strategy (P17).
- Dirty-tracked incremental crawl-state persistence: saves only when state changed (P15).
- `semantic` ranking strategy using local character-trigram hashing embeddings, no model dependency (P20).
- Embedded web Workbench (`-gui`, `-gui-port=N`): dashboard, profile editor, corpus viewer with
  curated text and copy button, FTS5 search, keyword extraction and one-click feedback to the profile,
  and crawl start/stop (P21).

### Changed
- `hybrid` ranking now combines keyword, TF-IDF and semantic scores.
- Version remains reported as v1.7.0 by `-version`.

## [v1.6.0] - 2026-11-17

### Added
- Prebuilt gzip frequency index (`corpusfreqlib/freq-index.json.gz`) for O(1) cold start.
- Graceful Ctrl-C shutdown that saves crawl state; generic per-domain `crawlDelayMs`.
- `-config=PATH`, space-separated CLI flags, `-flag value`, and CLI/top-K tests.

### Fixed
- Windows CI format check (CRLF via `.gitattributes`; format check runs only on Linux).
- Non-2xx HTTP responses and binary content types no longer enter the curated pipeline.
- Multi-label TLDs (`example.co.uk`) in `getSecondLevelDomain`.
- FTS5 query sanitisation; `SQLITE_BUSY` via busy_timeout; robots multi-worker slot reservation.
- `writeDocument` no longer calls `log.Fatal`; loggers fail fast when files cannot be created.
- File handles closed in `iolib` (`String2file`), fixing Windows CI TempDir cleanup; release assets rebuilt with this fix.

### Changed
- Pool pruning and bounded dedup sets for long crawls; top-K heap for frequency display.
- Language detection once per document instead of per paragraph.

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
