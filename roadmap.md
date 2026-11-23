# goCrawler roadmap

**Current version:** v1.8.19 (2026-11-18); the 2026-11-23 changes are recorded as `Unreleased` in section 2. Section 1 lists the remaining GUI backlog (P24–P47), the accepted P52–P67 items and the new proposals N01–N12.

This roadmap is reorganised so the only things you need to plan next work are in the first section,
sorted by increasing difficulty (S → M → L → XL). Completed work is kept as a compact per-version
table; full details of the senior audit and the GUI proposal are in `docs/`.

---

## 1. Pending work — ordered by increasing difficulty

Sources: `docs/GUI_STUDIO_ANALYSIS.md`, `docs/GUI_MOCKUP_ANALYSIS.md` and
`docs/REVIEW_RECOMMENDATIONS_2026-11-18.md`. Items are sorted by increasing effort; React/Vue
frontends are explicitly rejected (they would break the zero-dependency single-binary design).
The review document contains the reasoning, the rejected suggestions and the fact-checks.

| ID | Effort | Item |
|---|---|---|
| N01 | S | Expose `minStopwordRatio` / `maxStopwordRatio` in `crawler.yaml` and add `-min-stopword-ratio=` / `-max-stopword-ratio=` flags |
| N02 | S | `-lint-profile=<name>`: validate regexes, seed URLs, ratio range and unknown keys |
| N03 | S | Regression tests running the compiled binary from a foreign working directory (path robustness) |
| N04 | S | Add `go test -race` and `govulncheck` to CI (currently only gofmt/build/vet/test/selftest) |
| N05 | S | Windows equivalents of `run.sh` (`run.bat` / `run.ps1`), since CI already covers Windows |
| N10 | S–M | Pack of tuned technical profiles (astrophysics, ML, maths) based on `profiles/examples/technical.yaml` |
| N06 | M | Rejection report: breakdown by reason, paragraph sampling and JSONL export |
| N07 | M | Trace mode «why was this URL rejected?» per profile/seed |
| N08 | M | Dependency triage: upgrade `viper v1.7.1` / `modernc.org/sqlite v1.28.0` or document Dependabot false positives |
| N09 | M | Rotate and gzip `logs/` (2.2 GB in the workspace) |
| N11 | M | Verify state checksum when restoring with `--resume` |
| P24 | M | Filterable event console (INFO/WARN/ERR) with JSON/CSV/Sitemap XML export |
| P25 | M | Runtime controls: Pause and Drain in addition to Start/Stop |
| P26 | M | Live rule configurator: workers, per-domain rate limit, User-Agent, include/exclude patterns |
| P27 | M | Replace 1.5 s polling with Server-Sent Events (`/api/events`) |
| P32 | M | Fixed sidebar navigation with active profile selector and crawler status indicator |
| P34 | M | Live activity feed: last 20 processed URLs with status colours (`/api/activity`) |
| P36 | M | Progress ring and ETA estimate for the current crawl |
| P38 | M | Visual diff of profile changes before saving |
| P40 | M | Keyword Lab 2.0: sortable/filterable table, score-vs-frequency scatter, multi-select, diff preview |
| P43 | M | Domain heatmap (accepted pages per domain) |
| P45 | M | Command palette (Cmd/Ctrl+K) for quick actions |
| P46 | M | Report export to Markdown and print-to-PDF |
| P52 | M | Adaptive politeness: 429/503 + `Retry-After`, per-domain exponential back-off, max req/s and connections per host |
| P54 | M | Corpus export pack: Markdown bundle, JSONL, CSV and Hugging Face-style dataset folder with metadata |
| P56 | M | Profile inheritance/composition: base profiles + overlays (English + PDF + strict robots) |
| P64 | M | GUI/API contract: JSON Schema or generated OpenAPI for the `/api/*` handlers |
| P65 | M | Fuzz/property tests for URL parsing, PDF extraction and FTS5 sanitisation; expand multi-worker `httptest` coverage |
| N12 | L | Technical-corpus quality metrics: acronym density and numeric-token ratio (extends P53) |
| P28 | L | Structured per-URL audit records (HTTP status, Content-Type, latency) in SQLite |
| P29 | L | Audit table with instant search and filters by HTTP status and Content-Type |
| P37 | L | Structured profile editor: chips, live regex validation, YAML advanced view |
| P39 | L | Profile version history (snapshot per run with diff) |
| P42 | L | Corpus Explorer: virtualised list, keyword highlighting, filters, export MD/JSON/CSV |
| P44 | L | Session comparison between two runs of the same profile |
| P53 | L | Corpus quality dashboard: readability, lexical diversity, domain coverage, language-confidence histogram, topic purity |
| P55 | L | Optional JS rendering behind a build tag/runtime flag; default stays pure HTTP |
| P58 | L | Incremental/delta crawls via ETag/Last-Modified/content hash with new-vs-updated reporting |
| P66 | L | Staged modularisation: extract `gui/`, then `pipeline/`, then `download/` from `crawler.go` |
| P67 | L | Streaming/memory bounds for large corpora: stream paragraphs and bound in-memory frequency/dedup structures |
| P30 | XL | Interactive topology graph and URL inspection drawer |
| P47 | XL | Keyword impact analysis (simulate how many more documents new keywords would accept) |


**Recommended next step:** N01–N05 (S) are low-risk quick wins that also close the CI/security and
cross-platform gaps; do those first. P27 (SSE, **M**) remains the highest-leverage GUI item: it makes
P24, P25, P34 and P36 much cheaper. Ship telemetry/control next, then N06/N08/N09 and the remaining
P52/P54/P56/P64/P65 items.

Items P01–P21 of the senior audit and GUI MVP are **complete**, as are P22, P23, P31, P33, P35 and P41 (v1.8.6), plus the URL Seeds counter and About tab (v1.8.5–v1.8.8), PDF support P48–P51 (v1.8.14) and onboarding/notifications/accessibility/large-file hygiene P61–P63/P59 (v1.8.15).

---

## 2. Completed milestones by version

| Version | Date | Delivered |
|---|---|---|
| v0.9.0 | 2023-05-10 | Original upstream code: single-file Go crawler, Redis-only cache, keyword profiles |
| v1.0.0 | 2026-11-17 | SQLite cache backend, Go toolchain migration, Makefile, CLI validation, regex-injection fix, download cap, dead-code cleanup, cross-run log dedup (items 1–6) |
| v1.1.0 | 2026-11-17 | `robots.txt` policy, profile loading from `profiles/`, English doc comments, session output files, self-test mode (items 7–12, 19 partially) |
| v1.2.0 | 2026-11-17 | Structured `slog` logging, rejection counters, SQLite `--resume`, MIT license, Ubuntu readiness plan (items 10–12, 19) |
| v1.3.0 | 2026-11-17 | Go 1.26.11 + dependency upgrades (0 vulnerabilities), function-by-function audit, expanded tests, configurable TF-IDF/hybrid ranking, precompiled regexes, decomposed `doNextLink`, versioned state, O(1) link index (items 15, 21, 28–35) |
| v1.4.0 | 2026-11-17 | Full CLI flags, SQLite FTS5 search, multi-worker crawling with mutexes, multi-language profiles, profile generator/explorer, Snap/Debian/AppStream drafts, CI/CD and release (items 7, 13, 14, 16, 17, 20, 22–25) |
| v1.5.0 | 2026-11-17 | Roadmap refactor, senior audit, GUI design analysis, changelog and version tags, cross-platform release |
| v1.7.0 | 2026-11-17 | All deferred items closed: `Crawler` struct replacing globals (P19), `setup/crawl/report` split (P16), validated config loader (P17), dirty-tracked incremental state (P15), local hashing-embedding semantic ranking (P20) and the embedded web workbench GUI with profile editor, run monitor, corpus viewer, FTS5 search, keyword extraction and feedback loop (P21) |
| v1.6.0 | 2026-11-17 | Senior-audit fixes P01–P14 and CLI tests P18: HTTP status/content checks, public-suffix fix, FTS5 query sanitisation, fail-fast loggers, space-separated CLI flags and `-config`, SQLite busy timeout, robots slot reservation, prebuilt gzip frequency index, graceful Ctrl-C, generic per-domain delay, pool pruning, bounded dedup sets, top-K frequencies, document-level language detection, CLI/top-K tests; Windows CI CRLF fix |
| v1.8.14 | 2026-11-18 | PDF support (P48–P51): opt-in `process_pdf` profile flags, raw fetch split before the HTML Content-Type gate, pure-Go `pdftextlib` extraction with page/size limits, PDF cache prefix, new rejection reasons and tests |
| v1.8.15 | 2026-11-18 | Onboarding pack, notification hooks, Workbench accessibility and large-file hygiene (P61–P63, P59): badges/CONTRIBUTING/templates/examples, webhook + desktop notices, high-contrast/reduced-motion/keyboard navigation, generated frequency index with small offline fixtures |
| v1.8.16 | 2026-11-18 | Workbench Corpus tab: profile-matched TXT corpus export with download link |
| v1.8.17 | 2026-11-18 | Single-instance Workbench lock with stale recovery, graceful SIGINT/SIGTERM shutdown and no silent port fallback |
| v1.8.18 | 2026-11-18 | Interactive termination of previous Workbench instances, inline URL capture in URL Seeds and clear `pdf_disabled` rejection |
| v1.8.19 | 2026-11-18 | Default-yes instance prompt and scoped corpus export (all / last crawl / today) with session markers |
| Unreleased | 2026-11-23 | Module-root autodetection + `./run.sh`; configurable stopword ratio with `technical` example profile; Wikipedia unblocked in default config |

### Historically completed items (1–35)

1 Makefile · 2 CLI validation · 3 domain regex bug · 4 download cap · 5 dead code · 6 log dedup ·
7 CLI flags · 8 profile loading · 9 unit tests · 10 structured logging · 11 resume · 12 robots.txt ·
13 FTS5 search · 14 workers · 15 improved ranking · 16 multi-language · 17 profile generator/explorer ·
18 frequency dump fix · 19 MIT license · 20 version metadata · 21 no vulnerabilities · 22 Debian
dependency audit · 23 Snapcraft draft · 24 Debian skeleton · 25 AppStream · 28 precompiled regexes ·
29 `getNextLink` bug · 30 failed-domain counter · 31 shared HTTP client · 32 `doNextLink` split ·
33 error returns · 34 link index · 35 tests.

---

## 3. Deferred / out of scope — decisions already taken

| Item | Decision | Reason |
|---|---|---|
| 26 — Universe / Debian Mentors | **Deferred** | Needs Debian-packaged versions of unported Go modules and a sponsor; see `docs/UBUNTU_SUBMISSION.md` |
| 27 — Main / preinstalled ISO | **Out of scope** | Requires MIR, Canonical security audit, mass adoption and 5-year LTS support |
| 60 — Plugin interface / WASM filters | **Deferred** | ABI and sandboxing surface too large before the core API stabilises |
| Wails/Fyne desktop wrapper | **Deferred** | Loopback web GUI already works; packaging burden without new capability |
| Docker image | **Deferred (optional)** | Native single binary is the primary distribution; add on demand |
| Per-request HTTP client fix | **Rejected** | Already solved by `initHTTPClient()` with a shared tuned `Transport` |

---

## 4. How to use this file

- Section 1 contains all pending work (backlog plus new proposals); add new work there with its effort.
- When an item is finished, move it to the version table in section 2 with the release version that
  contains it and delete it from section 1.
- Never re-add a completed item; add new findings to section 1 at the position matching their effort.
