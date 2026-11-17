# Senior engineering audit — goCrawler (2026-11-17)

Scope: full source review after all roadmap items were implemented. This document records issues a
senior engineer would raise in a code review, with concrete proposals ordered by effort. Every item
is included in the refactored `roadmap.md` as a pending item.

## 1. Correctness issues

| ID | Finding | Proposal | Effort |
|---|---|---|---|
| A1 | `writeDocument` still calls `log.Fatal` on a CSV write error. It terminates the process without running deferred session/DB closes, truncating the session log tail. | Return an error through `doNextLink` → `run`; log at ERROR and stop that document. | S |
| A2 | `getSecondLevelDomain` takes the last two labels, so `www.example.co.uk` yields `co.uk`. `addLinksOf` then treats every `.co.uk` site as "internal" and skips its links. | Use a public-suffix list (even a small static one) or compare registrable domains via `net/publicsuffix` if available. | S |
| A3 | `proxyGet` never checks `resp.StatusCode`. A 403/404/500 body is processed as curated content; this is a plausible cause of the 48 download errors and low-relevancy noise. | Reject non-2xx outside `download`, report with the status, and record the domain failure. | S |
| A4 | `download` does not check `Content-Type`, so binary URLs (.deb, .zip, images) can be fetched and parsed as text (observed in cycle 4: SourceForge .deb links). | Skip non-HTML/text content types early and before caching. | S |
| A5 | `runSearch` passes the user query directly to FTS5 `MATCH`; a malformed query returns an opaque error, and quotes can change semantics. | Sanitize/quote terms and map FTS5 errors to a friendly message. | S |
| A6 | `initFileLoggers` ignores `os.Create` errors and silently installs `io.Discard`; a full disk or missing `logs/` produces no diagnostics. | Return an error and fail fast, or log to stderr. | S |
| A7 | `parseCLI` only accepts `-flag=value`, not `-flag value`; it also ignores an explicit `--config`. | Keep the current form but add space-separated form via a small parser, plus `-config=PATH`. | S |

## 2. Robustness and resource management

| ID | Finding | Proposal | Effort |
|---|---|---|---|
| B1 | No signal handling: a Ctrl-C or `docker stop` loses progress since state is saved only every 50 links. Handle `SIGINT`/`SIGTERM`, set a cancel flag, save state and flush outputs. | S |
| B2 | `lPool` keeps terminal links (status 2/3) forever; after millions of links memory grows without bound. Archive/remove processed entries while keeping the URL index and counters. | M |
| B3 | `uniqueSignature` and `rejectedLinkSets` are unbounded (up to 1M per log). Persist them in SQLite with LRU eviction or capped size. | M |
| B4 | `saveCrawlState` serializes the whole pool every 50 links. With a large pool this is O(n) I/O. Save deltas or reduce frequency adaptively. | M |
| B5 | `searchDB` opens a second SQLite connection without a busy timeout; concurrent writers can hit `SQLITE_BUSY`. Add `_pragma=busy_timeout(5000)` to the DSN. | S |
| B6 | `startSessionOutput` uses an unbounded goroutine copier into `os.Stdout`; a blocked terminal blocks the crawler. Use a bounded pipe or write to the file directly and mirror to stdout best-effort. | M |

## 3. Performance

| ID | Finding | Proposal | Effort |
|---|---|---|---|
| C1 | `rSortFreq` fully sorts ~tens of thousands of tokens every 50 links, although only the top 100 are printed. | Use a top-K heap (`container/heap`) for display and keep the full map for frequencies. | M |
| C2 | `whatlanggo.Detect` runs on every paragraph and `stopWordsCount` uses regexes per paragraph. With 2 workers CPU reached 137 %; 8 workers would saturate. | Detect language once per document (sample 3 paragraphs), reuse precompiled boundary regexes (already done for most). | M |
| C3 | `corpusfreqlib.Init` still parses 700k lines on first run before writing the JSON index. Ship a prebuilt compressed index in the repository to make cold start O(1). | S |
| C4 | Per-domain politeness only honours robots `Crawl-delay`; sites without it can be hammered by 8 workers. | Add a generic configurable `crawlDelay` per domain and reserve the next slot before sleeping. | M |

## 4. Architecture / maintainability

| ID | Finding | Proposal | Effort |
|---|---|---|---|
| D1 | ~30 package-level mutable variables make unit tests interdependent and force mutexes everywhere. | Introduce a `Crawler` struct owning config, state and stores; pass it to functions. Keep thin wrappers for compatibility. | L |
| D2 | `run()` is still ~180 lines with mixed concerns (CLI, config, store, corpus, loop). | Split into `setup()`, `crawl()`, `report()` orchestrators. | M |
| D3 | Config is duplicated between `crawler.yaml` (global) and profile YAML; no schema validation or `--config` override. | Add a single config loader with validation and structured errors. | M |
| D4 | Tests cover pure functions but not the CLI modes (`-query`, `-explore`, `-generate-profile`) nor multi-worker race behaviour end to end. | Add CLI integration tests using a local `httptest` server and `-workers=4`. | M |
| D5 | `robotsPolicy.Wait` records the request time before sleeping, so several workers can bunch requests to the same host. | Reserve `lastReq = now + delay` before releasing the mutex. | S |

## 5. Product-level opportunity

| ID | Finding | Proposal | Effort |
|---|---|---|---|
| E1 | All configuration and result inspection happens by editing YAML and reading text logs. There is no way to iteratively tune a profile without command-line flags. | Build the local web GUI analysed in `docs/GUI_ANALYSIS.md`: profile editor, run monitor, curated-text viewer, keyword extraction and feedback loop. | XL |

## Versioning note

All findings A1–A7, B1–B6, C1–C4, D1–D5 and E1 are registered as pending roadmap items ordered by
increasing difficulty. None of them blocks the current release; they are the natural v1.6+ backlog.
