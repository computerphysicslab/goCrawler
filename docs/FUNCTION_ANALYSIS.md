# goCrawler function-by-function analysis

Date: 2026-11-17. Scope: every function in `crawler.go` and the four library packages.
Findings are ordered by area; "Roadmap" refers to items in `roadmap.md`.

## 1. Main crawler (`crawler.go`)

### Configuration and lifecycle

| Function | Finding | Proposed improvement | Roadmap |
|---|---|---|---|
| `yamlInitGeneral` | Uses the global Viper instance; panics on read errors; no way to test without real files. | Use a local Viper instance (as `yamlInitSpecific` already does) and return an error. | 33 |
| `yamlInitProxy` | Same global Viper; silently ignores a missing file but panics on a malformed one. | Return `(bool, error)`; log at debug level when absent. | 33 |
| `yamlInitSpecific` | Parses the profile into globals; compiles no regexes, so callers compile them per call. | Compile `regexLinkOk`, `regexBannedDomains`, `regexLinkBannedTokens` and `regexStopwords` once here and store the compiled values. | 28 |
| `usage` | Correct and simple. | Keep. Also mention `--resume` (already added). | — |
| `startSessionOutput` | Uses `os.Pipe` and a copier goroutine. Error paths in `main` call `os.Exit`, which skips the deferred close, so the tail of a failed run can be lost. | Replace `os.Exit` with an error return from a `run()` function and let `main` exit after defers. | 33 |
| `initLogger`, `countRejection`, `rejectionSummary` | Correct; counters are plain maps (safe while single-threaded). | Add a mutex before enabling the worker pool (item 14). | 14 |
| `saveCrawlState`, `loadCrawlState` | Snapshot has no schema version, so a future change can silently corrupt resume. | Add `Version int` and reject unknown versions. | 33 |
| `csvInit` | Opens the file with `log.Fatal` on error; the handle is now closed via `defer` in `main`. | Return an error instead of `log.Fatal` so defers run. | 33 |

### Download and cache

| Function | Finding | Proposed improvement | Roadmap |
|---|---|---|---|
| `proxyGet` | Creates a new `http.Client` for every request and sets `req.Close = true`, disabling keep-alive → TCP/TLS handshake per page. | One package-level client with a tuned `http.Transport` (`MaxIdleConns`, `MaxIdleConnsPerHost`, TLS handshake timeout) and no `Close` flag. | 31 |
| `download` | Caps the body with `io.LimitReader` (good). Rewraps the consumed body as `NopCloser` so `collectlinks` can read it again; works but holds the whole page in memory. | Acceptable for 5 MiB pages; for larger corpora stream to a temp file and parse twice. | 31 |
| `downloadCached` | A corrupt JSON value in the cache causes `panic`. Writes failures are only printed. | On unmarshal error, discard the entry and re-download; return cache write errors through the logger. | 33 |
| `getDomain`, `getSecondLevelDomain` | Compile a regex on every call, even though the patterns are constants. | Precompile once at package level; `strings.TrimPrefix` for `www.` and `strings.LastIndex` for the SLD are even cheaper. | 28 |
| `isBanned` | Compiles both blacklist regexes on every link — one of the hottest paths. | Use the regexes compiled in `yamlInitGeneral`. | 28 |

### Link pool

| Function | Finding | Proposed improvement | Roadmap |
|---|---|---|---|
| `linkSeemsOk` | Compiles the strict URL regex per call; silently drops links shorter than 13 chars without incrementing a rejection counter. | Precompile; add a `too_short_url` counter. | 28 |
| `initRejectedLinkSets`, `logRejectedLink` | In-memory sets bounded to 1M entries; unsynchronised. Loading a large log at startup costs memory. | Use a SQLite table for rejected links; add a mutex before parallel crawls. | 6 / 14 |
| `getNextLink` | O(n) scan of the pool per link. **Bug:** if no candidate passes `isBanned`/`linkSeemsOk`, `maxURL` stays empty but `increaseDomainCounter(lPool[0].Domain)` still runs, corrupting priorities. | Guard on `maxURL == ""` before incrementing; replace the scan with a priority queue. | 29 / 14 |
| `addLink` | O(n) linear duplicate search; compiles the CGI-stripping regex per call; `avoidFilters` has two meanings (bootstrap vs. skip checks). | Maintain a `map[string]int` index from URL to pool position; precompile the regex; split the two booleans. | 28 / 34 |
| `linkBootstraping` | Correct. | No change. | — |
| `lPoolDump`, `domainCounterDump` | Write JSON diagnostics that are never read; actual resume uses `state:crawl`. | Keep for debugging or remove after item 11 is stable. | 5 |

### Filtering and NLP

| Function | Finding | Proposed improvement | Roadmap |
|---|---|---|---|
| `domainReportFailed`, `domainHadFailed` | **Performance bug:** `domainHadFailed` reads and splits the whole `domainFailed.log` on every download (O(n²) across a run). | Keep an in-memory `map[string]int` counter; persist it in `state:crawl`; keep the log for humans only. | 30 |
| `tokenize`, `tokensCount`, `lowercaseFilter`, `stemmerFilter` | Pure and correct. `stemmerFilter` is unused. | Keep; either wire stemming behind a profile flag or delete it. | 5 |
| `splitParagraphs`, `stopWordsCount`, `stopWordsOnBorderCount`, `lowRelevancyWordsOnBorderCount`, `stopwordFilter` | Each compiles a constant or config-derived regex on every call. | Precompile in `yamlInitGeneral` / package scope. | 28 |
| `rankingByKeywords` | Compiles `regexRankingKeywords` per call; score divides by `sqrt(1+tokens)` and therefore penalises long, relevant documents (observed with Siril GHS). | Precompile; normalise by length or move to TF-IDF/embeddings. | 15 / 28 |
| `bestParagraph` | Correct, 2000-character cap. | No change. | — |
| `analyze` | Fine; delegates to `stopwordFilter`. | Benefit from item 28. | 28 |
| `freq.add` | Increments a global `counter` that nothing reads. | Remove the dead counter. | 5 |
| `rSortFreq`, `getKVkeys` | Correct. | No change. | — |
| `ngramsOf`, `ngramsFreqsOf`, `kvSliceRemoveItem`, `ngramsFreqsOfAll` | Complex heuristics with several regexes compiled per call; n-gram block is disabled in `main`. | Precompile; add table-driven tests before re-enabling. | 5 / 15 |
| `addLinksOf` | Correctly skips same-SLD links; no cap on links added per page. | Add a configurable per-page link cap to avoid runaway pools. | 34 |

### Main loop and output

| Function | Finding | Proposed improvement | Roadmap |
|---|---|---|---|
| `doNextLink` | ~330 lines with 8 early returns and all output writes; hard to test in isolation. Frequency dump only fires when an accepted document lands on a multiple of 50 (bug 18). | Split into `filterParagraphs`, `buildCuratedContent`, `isUniqueDocument`, `writeDocument`, `dumpFrequencies`; move the dump outside the acceptance path. | 18 / 32 |
| `main` | Uses `os.Exit` on store/config errors, skipping deferred log/CSV/session closes; very long. | Extract `run() error`; `main` only handles the exit code. | 33 |

## 2. Library packages

| Package / functions | Finding | Proposed improvement | Roadmap |
|---|---|---|---|
| `iolib.FileExists`, `CopyFileContents`, `String2file`, `String2fileAppend`, `File2string` | `String2fileAppend` opens/closes the file on every call (thousands of times per run); `File2string` logs errors but can return an empty string on a nil file. | Batch rejected-link appends or keep the file open; make `File2string` return `(string, error)`. | 6 |
| `stringlib.RmNewLines`, `IsNumeric`, `Test` | Correct and tiny. | Add unit tests (done). | 35 |
| `corpusfreqlib.Init`, `Freq`, `loadCorpus`, `fileExists`, `Test` | Loads 669k+27k lines at every start; read-only afterwards, so safe for workers. | Persist a compact binary/JSON index and load that instead of the text files. | 32 |
| `redislib.newPool`, `defaultConn` | One shared `redis.Conn` is **not** safe for concurrent use; `SetInterface`/`GetInterface` marshal JSON correctly. | Use `pool.Get()`/`Close()` per operation (the pool already exists) before enabling workers. | 14 |
| `storelib` `Init`, SQLite/Redis stores | SQLite deliberately uses `SetMaxOpenConns(1)`; safe but serialises all cache traffic. | Benchmark WAL with N connections; for Redis use the connection pool. | 14 |
| `robotspolicy` `Policy`, `Allowed`, `Wait` | Mutex-protected; `Wait` records the request time before sleeping, so several workers could still bunch requests to one host. | Reserve the next per-host slot (`lastReq = now + delay`) before sleeping. | 14 |

## 3. Test coverage added

`crawler_test.go`, `storelib/store_test.go`, `robotspolicy/robotspolicy_test.go`, plus new tests for `iolib`, `stringlib` and `corpusfreqlib` cover: tokenisation, paragraph splitting, host extraction, blacklists, keyword ranking, stopword filtering, best paragraph, frequencies, n-grams, link validation, failed-domain counting, log deduplication, session-file naming, rejection counters, SQLite state round-trip, cache store edge cases, robots allow/disallow/crawl-delay and fail-open, and a local HTTP end-to-end crawl.
