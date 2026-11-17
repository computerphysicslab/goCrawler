# goCrawler roadmap

**Current version:** v1.5.0 (2026-11-17). **Everything below "Pending" is not implemented yet.**

This roadmap is reorganised so the only things you need to plan next work are in the first section,
sorted by increasing difficulty (S → M → L → XL). Completed work is kept as a compact per-version
table; full details of the senior audit and the GUI proposal are in `docs/`.

---

## 1. Pending work — ordered by increasing difficulty

Source: `docs/SENIOR_AUDIT.md` (2026-11-17) and `docs/GUI_ANALYSIS.md`.

### S — small (minutes to hours)

| ID | Item | Why |
|---|---|---|
| P01 | Replace `log.Fatal` in `writeDocument` with an error propagated to `run()` | Avoid skipping deferred session/DB closes on CSV failure |
| P02 | Check HTTP status codes in `proxyGet` and skip binary `Content-Type` | Error pages and .deb/.zip files currently enter the pipeline |
| P03 | Fix `getSecondLevelDomain` for multi-label TLDs (`example.co.uk`) | Internal-link detection is wrong for many sites |
| P04 | Sanitise FTS5 `MATCH` queries and map errors to friendly messages | `-query` should not fail on quotes or operators |
| P05 | Fail fast when `initFileLoggers` cannot create log files | Silent `io.Discard` hides disk/mount problems |
| P06 | Support `-flag value` and add `-config=PATH` | CLI ergonomics and alternate configuration files |
| P07 | Add `_pragma=busy_timeout(5000)` to the `searchDB` DSN | Avoid `SQLITE_BUSY` with concurrent workers |
| P08 | Reserve the per-host slot before sleeping in `robotsPolicy.Wait` | Multiple workers can otherwise bunch requests |
| P09 | Ship a prebuilt compressed frequency index | Remove the 700k-line cold-start parse |
| P10 | Handle `SIGINT`/`SIGTERM`: save state and flush outputs | Ctrl-C currently loses up to 50 links of progress |

### M — medium (days)

| ID | Item | Why |
|---|---|---|
| P11 | Bound session-output copier and add per-domain generic `crawlDelay` | Predictable memory and politeness without robots.txt |
| P12 | Prune terminal links from `lPool`; cap `uniqueSignature` and rejection sets | Long crawls grow memory without bound |
| P13 | Replace full frequency sort with a top-K heap | `rSortFreq` sorts tens of thousands of tokens every 50 links |
| P14 | Detect language once per document instead of per paragraph | `whatlanggo` dominates CPU in warm-cache runs |
| P15 | Incremental `crawlState` persistence | Whole-state serialization every 50 links is O(pool size) |
| P16 | Split `run()` into `setup()`, `crawl()`, `report()` | ~180-line function mixes five concerns |
| P17 | Single validated config loader shared by CLI and profile | Duplicated config paths, no schema validation |
| P18 | CLI integration tests (`-query`, `-explore`, `-generate-profile`, `-workers=4`) | Current tests cover pure functions only |

### L — large (weeks)

| ID | Item | Why |
|---|---|---|
| P19 | Introduce a `Crawler` struct replacing ~30 globals | Removes global mutexes, makes tests independent, enables embedding |
| P20 | Real semantic ranking with local embeddings (configurable) | TF-IDF + synonyms helps, but true synonymy needs vectors |

### XL — extra large (multi-month product work)

| ID | Item | Why |
|---|---|---|
| P21 | **goCrawler Workbench GUI** — local web UI to edit profiles, monitor runs, browse curated text, extract keywords and feed them back into the profile | Full design in `docs/GUI_ANALYSIS.md`; phases G1–G6 (dashboard → profile editor → run monitor → corpus viewer → keyword lab → packaging) |

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
| v1.5.0 | 2026-11-17 | Roadmap refactor, senior audit, GUI design analysis, changelog and version tags, cross-platform release (this version) |

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

---

## 4. How to use this file

- Work only from section 1, top to bottom.
- When an item is finished, move it to the version table in section 2 with the release version that
  contains it and delete it from section 1.
- Never re-add a completed item; add new findings to section 1 at the position matching their effort.
