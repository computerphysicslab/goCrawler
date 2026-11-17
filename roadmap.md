# goCrawler roadmap

**Current version:** v1.6.0 (2026-11-17). Section 1 lists only deliberately deferred items; everything else is complete.

This roadmap is reorganised so the only things you need to plan next work are in the first section,
sorted by increasing difficulty (S → M → L → XL). Completed work is kept as a compact per-version
table; full details of the senior audit and the GUI proposal are in `docs/`.

---

## 1. Pending work — ordered by increasing difficulty

All actionable S/M items from the 2026-11-17 senior audit were implemented in **v1.6.0** (P01–P14
and P18). The items below remain **deliberately deferred after evaluation**; each has a recorded
reason so no decision is lost.

| ID | Effort | Item | Status / reason |
|---|---|---|---|
| P15 | M | Incremental `crawlState` persistence | Deferred: full-state writes every 50 links are acceptable at current scale; a correct delta design depends on P19 |
| P16 | M | Split `run()` into `setup()/crawl()/report()` | Deferred: high regression risk for low user value; superseded by P19 |
| P17 | M | Single validated config loader | Deferred: needs the ownership model of P19 |
| P19 | L | `Crawler` struct replacing ~30 globals | Deferred: large refactor with no functional gain for current users |
| P20 | L | Local embeddings for semantic ranking | Deferred: requires a model dependency and a labelled evaluation set |
| P21 | XL | goCrawler Workbench GUI | Design complete (`docs/GUI_ANALYSIS.md`); implementation is a separate v2.x product milestone |

**Completed from this audit:** P01–P14 and P18 (see v1.6.0 below).

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
| v1.6.0 | 2026-11-17 | Senior-audit fixes P01–P14 and CLI tests P18: HTTP status/content checks, public-suffix fix, FTS5 query sanitisation, fail-fast loggers, space-separated CLI flags and `-config`, SQLite busy timeout, robots slot reservation, prebuilt gzip frequency index, graceful Ctrl-C, generic per-domain delay, pool pruning, bounded dedup sets, top-K frequencies, document-level language detection, CLI/top-K tests; Windows CI CRLF fix |

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

- Section 1 now contains only deferred items; new work should be added there with its effort.
- When an item is finished, move it to the version table in section 2 with the release version that
  contains it and delete it from section 1.
- Never re-add a completed item; add new findings to section 1 at the position matching their effort.
