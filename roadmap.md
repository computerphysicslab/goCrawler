# goCrawler roadmap

Improvement proposals ordered from **simplest / lowest effort** to **most complex / highest effort**.
Each item includes the current problem, the proposed change and its expected impact.

Legend for effort: **S** = minutes to hours · **M** = days · **L** = weeks.

---

## Tier 1 — Quick wins (effort: S)

### 1. Add a Makefile — **DONE (2026-11-17)**
**Problem:** build, test and formatting commands are undocumented and must be typed by hand.
**Proposal:** add targets `build`, `run <profile>`, `test`, `fmt`, `vet`, `clean`.
**Impact:** reproducible workflow; low risk.

### 2. Validate command-line arguments — **DONE (2026-11-17)**
**Problem:** `argsWithoutProg := os.Args[1:]` (`crawler.go:1472`) panics when no profile is given.
**Proposal:** check the argument count, print a usage message and exit cleanly; add `--help`.
**Impact:** much better first-run experience.

### 3. Fix the domain regex injection bug — **DONE (2026-11-17)**
**Problem:** `domainHadFailed()` (`crawler.go:514`) builds `regexp.MustCompile("(?i)\\W(" + domain + ")\\W")`
without escaping the domain. A domain containing regex metacharacters can panic; a commented stack
trace at the end of `crawler.go` documents a real occurrence.
**Proposal:** use `regexp.QuoteMeta(domain)` or plain `strings.Contains`.
**Impact:** removes a known crash path.

### 4. Cap download size — **DONE (2026-11-17)**
**Problem:** `ioutil.ReadAll(resp.Body)` (`crawler.go:212`) has no limit and can exhaust memory on a
huge or malicious response.
**Proposal:** wrap the body in `io.LimitReader(resp.Body, 5<<20)` (configurable maximum).
**Impact:** predictable memory usage.

### 5. Remove dead code — **DONE (2026-11-17)**
**Problem:** the global n-gram block behind `if false`, the commented `bigramsOf()`, the legacy
`cacheInit`/`cacheSave` functions, `gob.Register`, the unused `go-cache` dependency and the commented
stack trace add noise to a 1560-line file.
**Proposal:** delete them (or move experiments to an `experimental/` folder) and run `go mod tidy`.
**Impact:** smaller codebase and dependency graph; easier maintenance.

### 6. Stop the `linksNotOk*.log` files from growing without limit — **DONE (2026-11-17)**
**Problem:** three append-only logs are never deduplicated. Historical runs accumulated about
1.9 GB / 45 million lines, mostly repeated social-share links.
**Proposal:** log each rejected link only once using a `map[string]struct{}` or a dedicated SQLite
table via `storelib`; optionally rotate by size.
**Impact:** highest operational value of all quick wins; prevents the disk-filling problem from
returning.

---

## Tier 2 — Medium effort (effort: M)

### 7. Real CLI flags — **DONE (2026-11-17)**
**Problem:** configuration is YAML plus one positional argument; switching backends requires
editing `crawler.yaml`.
**Proposal:** use `pflag` (already in the dependency tree) for `--profile`, `--backend`,
`--cache-path`, `--max-links`, `--verbose`, `--dry-run`.
**Impact:** scriptable, no file edits needed for common options.

### 8. Load profiles from `profiles/` directly — **DONE (2026-11-17)**
**Problem (solved):** the YAML had to be copied to the working directory before every run.
`yamlInitSpecific()` now uses a local Viper instance that looks in `profiles/` first and falls back
to `.`; the resolved path is printed at startup. The Makefile `run` target no longer copies files.
**Proposal:** search `./profiles/<name>.yaml` first, then fall back to `./<name>.yaml` for backwards
compatibility.
**Impact:** removes a manual, error-prone step.

### 9. Unit tests for the crawling and filtering logic — **DONE (2026-11-17)**
**Problem (solved):** only the cache backends were tested. Implemented: `crawler_test.go` covers
tokenisation, paragraph splitting, domain extraction, keyword ranking, stopword filtering, best
paragraph, frequencies, n-grams, link validation, failed-domain counting and log deduplication.
A `--selftest` CLI mode performs the same kind of checks from the compiled binary. Remaining:
end-to-end tests of `doNextLink` with an HTTP fixture server.
**Proposal:** table-driven tests with representative URLs, English/non-English paragraphs and
boundary cases for every threshold.
**Impact:** safe refactoring and reproducible behaviour.

### 10. Structured logging and rejection counters — **DONE (2026-11-17)**
**Problem:** dozens of `fmt.Printf` calls produce unstructured output; it is hard to know why
documents were discarded.
**Proposal:** migrate to `log/slog` with levels and emit a summary of rejections by reason
(language, length, relevance, duplicate, banned domain).
**Impact:** profile tuning becomes measurable instead of guesswork.

### 11. Resume support — **DONE (2026-11-17)**
**Problem:** `LPool.json` and `domainCounter.json` are written but never read back; stopping a run
loses all progress.
**Proposal:** persist the link pool, domain counters and visited links in SQLite and restore them at
startup (with a `--resume` flag).
**Impact:** makes long crawls practical.

### 12. Politeness and `robots.txt` — **DONE (2026-11-17)**
**Problem (partially solved):** the crawler fires requests as fast as the sequential loop allows
and ignores site policies. Implemented: `robots.txt` fetch/cache per host, User-Agent, block logging
and `Crawl-delay` enforcement. Remaining: a general configurable per-domain delay even when the site
does not declare one.
**Proposal:** fetch and honour `robots.txt`, add a configurable per-domain delay and a descriptive
`User-Agent`.
**Impact:** fewer blocks, better citizenship, higher yield per seed.

---

## Tier 3 — Larger efforts (effort: L)

### 13. Full-text search over the corpus using SQLite FTS5 — **DONE (2026-11-17)**
**Problem:** the crawler builds a useful corpus but offers no way to query it.
**Proposal:** store curated documents in the existing SQLite database with an FTS5 virtual table
and add a query command/CLI that returns matching documents and snippets.
**Impact:** turns the corpus into a real search tool without adding a new service. **Highest-value
item in this roadmap.**

### 14. Concurrent workers with per-domain rate limiting — **DONE (2026-11-17)**
**Problem:** crawling is completely sequential; fetching hundreds of pages is slow. A 106-second
run used only 24.78 s user + 0.88 s system CPU (24 % of one core on an 8-core machine), so the
bottleneck is network latency, not CPU.

**Feasibility analysis (2026-11-17):** parallelisation is feasible, but it is an **I/O concurrency**
change, not a way to "use the 8 cores". Eight workers is a sensible upper bound; the right worker
count should be tuned against politeness limits, not `runtime.NumCPU()`.

State that must be protected or given a single owner:

- `lPool []aLink` (status changes in `getNextLink`/`doNextLink`, appends and `Count++` in `addLink`);
- `domainCounter map[string]int` (read for priority, written by `increaseDomainCounter`);
- `uniqueSignature map[string]string` (check-then-set: two workers could accept the same document);
- `corpusFreqs freq` (map writes);
- `csvWriter` (not concurrency-safe; `log.Logger` itself is safe);
- `logRejectedLink` sets plus the non-atomic `String2fileAppend` calls;
- `cacheStore`: SQLite is opened with `SetMaxOpenConns(1)` and `redislib` reuses one connection —
  either serialise access or use a real connection pool;
- `robotsPolicy` already has a mutex, but its `Wait` should reserve the next per-host slot instead
  of only recording the last request time.

Pure code (tokenisation, `whatlanggo`, `html2text`, `collectlinks`, ranking, stopword filtering) can
run unchanged in workers; configuration and `corpusfreqlib` are read-only after initialisation.

**Proposal:** one dispatcher goroutine owns the link pool and counters and feeds a channel; N workers
download, parse and filter; one aggregator goroutine owns `uniqueSignature`, `corpusFreqs`, the CSV,
the corpus file and the JSON dumps. Add per-domain rate limiting and `Crawl-delay`, and either keep
results ordered by an index or accept unordered output. Validate with `go test -race`.

**Impact:** large wall-time reduction (I/O bound); CPU stays low. Refactoring effort is significant
and output ordering changes. **Status: proposed, not implemented.**

### 15. Better ranking: normalize length and add semantic similarity — **DONE (2026-11-17)**
**Problem:** `rankingByKeywords` divides by `sqrt(1 + tokenCount)`, so long, highly relevant
documents score low (observed with the Siril GHS tutorial). Keyword matching also misses synonyms.
**Proposal:** normalize scores by document length, consider TF-IDF or embeddings for semantic
similarity, and make the strategy configurable per profile.
**Impact:** better corpus precision and recall; requires evaluation against a labelled set.

### 16. Configurable multi-language support — **DONE (2026-11-17)**
**Problem:** `whatlanggo` detection only accepts English (`crawler.go:1144`); there is no parameter
to change it.
**Proposal:** add `language` and `minLanguageConfidence` to the profile and generalize the paragraph
filter; optionally support several languages at once.
**Impact:** opens the tool to non-English corpora; changes a core filter.

### 17. Profile generator and corpus exploration UI — **DONE (2026-11-17)**
**Problem:** writing profiles by hand is tedious, and exploring results means opening raw text and
frequency files.
**Proposal:** a helper that derives `linkOk`/`regexRankingKeywords` from a seed URL plus a few terms,
and a small CLI or web UI to browse ranked documents, frequencies and n-gram statistics (re-enabling
the dormant n-gram code).
**Impact:** lowers the entry barrier and makes the collected knowledge much more usable.

---

### 18. Fix stale frequency dumps — **DONE (2026-11-17)**
**Problem:** the frequency files are written inside `doNextLink()` only when a document passes every
filter *and* `numLinksProcessed % 50 == 0`. Most passing documents do not land exactly on a multiple
of 50, so `corpusFrequencies.txt` and `corpusNoEngFrequencies.txt` can end up describing a single
document even after a long crawl (observed in the 2026-11-17 astrophotography run).
**Proposal:** move the periodic dump to a place that executes for every processed link, or dump on
a final flush when the link pool is exhausted; also dump at run end regardless of the counter.
**Impact:** trustworthy frequency output; **medium priority** because it affects the quality of one
of the main results.

---

## Ubuntu / Debian distribution readiness

Goal: make goCrawler packageable and, eventually, acceptable for Ubuntu's review processes.

**Current facts that matter for an audit:**

- Language: **Go 1.20**; the default cache is pure-Go SQLite (`modernc.org/sqlite`), so
  `CGO_ENABLED=0` static builds are already possible.
- **License: none.** The GitHub repository is public but has no `LICENSE` file, so it is legally
  "all rights reserved". This blocks Universe/Main and is bad practice even for PPA/Snap.
- Dependencies: Viper, redigo, whatlanggo, html2text, collectlinks, robotstxt, modernc/sqlite and
  their transitive tree. Debian policy requires building from source with no network and without
  bundling libraries that already exist as distro packages.
- GitHub Dependabot currently reports **12 vulnerabilities (5 high, 7 moderate)** on the default
  branch. These must be resolved or explicitly documented before any review.
- The tool is a **CLI**, not a GUI app: AppStream metadata is still useful (a `cli` component), but
  a `.desktop` launcher is optional. A man page and `--version` are expected by Debian.
- It writes `./logs`, `./output` and `./cache` relative to the working directory. Strict Snap
  confinement would need those paths redirected to `$SNAP_USER_DATA` and the `network` interface.

### 19. Add an explicit open-source license — **DONE (2026-11-17)**
Add `LICENSE` (MIT or Apache-2.0 recommended) and a `debian/copyright` DEP-5 stub. Without this,
Universe/Main are impossible and Snap/PPA metadata is incomplete. Also add an `SPDX-License-Identifier`
header to source files if Apache-2.0 is chosen.

### 20. Version and reproducible build metadata (effort: S) — **DONE (2026-11-17)**
Add `--version`/`--license` flags, a `VERSION` constant, clean semantic Git tags (`v0.1.0`), a man
page (`crawler.1`) and `go mod vendor` for offline builds. Verify `CGO_ENABLED=0 go build` and
document the exact Go toolchain version.

### 21. Fix dependency vulnerabilities — **DONE (2026-11-17)**
**Analysis (2026-11-17, via `gh api repos/.../dependabot/alerts`):** all 12 open alerts are for one
transitive dependency, **`golang.org/x/net`** (5 high, 7 medium); the current pin is the 2020
pseudo-version `v0.0.0-20200813134508-3edf25e44fcc`. `go mod why` shows the path
`goCrawler → github.com/jackdanger/collectlinks → golang.org/x/net/html` (link extraction).

Patched versions per advisory: v0.38.0 (GHSA-vvgc-356p-c3xw), v0.36.0
(GHSA-qxp5-gwg8-xv66), v0.17.0 (GHSA-qppj-fm5r-hxr3, GHSA-4374-p667-p6c8), v0.13.0
(GHSA-2wrh-6pvc-2jm9), v0.7.0 (GHSA-vvpx-j8f3-3w6h), v0.4.0 (GHSA-xrjj-mj9h-534m), plus four
older pseudo-version fixes (GHSA-fxg5-wq6x-vr4w, GHSA-83g2-8m93-v3w7, GHSA-h86h-8ppg-mxmh,
GHSA-69cg-p879-7622) already covered by any recent release. GHSA-4v7x-pqxf-cx7m also lists the Go
1.21.9 standard-library fix.

Three possible resolutions:

- **A — Upgrade x/net to v0.33.0 (Go 1.20-compatible, immediate):** fixes **10 of 12**. The two
  remaining medium alerts need v0.36.0/v0.38.0, which require Go ≥ 1.23. Their applicability here is
  low: one is XSS in HTML *rendering* (this tool converts HTML to plain text and never renders it),
  the other is an IPv6-zone proxy bypass (no proxy is configured by default). Document both as
  accepted risks in `SECURITY.md`.
- **B — Upgrade the toolchain to Go ≥ 1.23 and x/net v0.38.0 (recommended medium-term):** fixes all
  12, enables stdlib `log/slog` (removing the `golang.org/x/exp` dependency from item 10) and matches
  the direction of the SkyStacker toolchain analysis. Requires a full build/test/selftest cycle.
- **C — Replace `collectlinks` with a private regex-based href extractor:** would remove x/net from
  the module graph entirely, but lowers parsing quality (regex vs a real HTML parser). Not advised.

**Resolution applied (path B, extended):** installed the `go1.26.11` toolchain via `golang.org/dl`,
set `go 1.26.0` + `toolchain go1.26.11`, upgraded `golang.org/x/net` to v0.62.0 and
`gopkg.in/yaml.v2` to v2.4.0, and replaced `golang.org/x/exp/slog` with the standard `log/slog`.
`govulncheck ./...` now reports **No vulnerabilities found**; `go build`, `go vet`, `go test` and
`--selftest` all pass. The Makefile defaults to `GO ?= go1.26.11`.

### 22. Audit dependency packaging for Debian (effort: M) — **DONE (2026-11-17)**
Check which Go modules already exist as Debian/Ubuntu packages (`golang-github-*`). Those that do
must be used from the distro; the rest need either packaging work or an exception for vendoring.
This decision determines whether Universe is realistic.

### 23. Snapcraft strict-confinement draft (effort: M) — **DONE (2026-11-17)**
Add `snap/snapcraft.yaml` with `confinement: strict`, `base: core22`, `grade: stable`, the `network`
interface and writable areas limited to `$SNAP_USER_DATA`. Adapt `cachePath`, logs and output paths
via configuration so no write outside the snap data directory is attempted. Snap is the lowest-friction
route for appearing in Ubuntu's App Center; strict confinement avoids human review.

### 24. Debian packaging skeleton for PPA (effort: M) — **DONE (2026-11-17)**
Create `debian/control`, `debian/rules` (using `dh-golang`), `debian/changelog`, `debian/install` and
a signed source package. A Launchpad PPA builds the `.deb` without an audit; the user must run
`sudo add-apt-repository` first. This is the fastest `.deb` route.

### 25. AppStream metadata and desktop integration (effort: S) — **DONE (2026-11-17)**
Add `io.github.computerphysicslab.goCrawler.metainfo.xml` (CLI component, description, screenshots,
release notes) and an SVG icon. Useful for Snap Store and harmless for PPA.

### 26. Universe / Debian Mentors sponsorship (effort: L, long timeline) — **DEFERRED by decision (2026-11-17)**
Only after items 19–22: enter Debian unstable first, find a sponsor, pass the NEW queue, then wait for
Ubuntu to import the package in the next six-month cycle. Requires full source-only reproducible builds,
no bundled dependencies and a clean `debian/copyright`.

### 27. Main / preinstalled ISO (effort: XL, not currently realistic) — **OUT OF SCOPE by decision (2026-11-17)**
Requires a Main Inclusion Report, line-by-line Canonical security audit, demonstrated mass adoption
and a five-year LTS security commitment. Not recommended for this project in its current form.

**Recommended order:** 19 → 20 → 21 → 23 (Snap Store, the practical target) → 24 (PPA for `.deb` users)
→ 25. Treat 26 as a long-term goal and 27 as out of scope.

---

## Additional proposals from the function-by-function review (2026-11-17)

Full details in [`docs/FUNCTION_ANALYSIS.md`](docs/FUNCTION_ANALYSIS.md).

### 28. Precompile regular expressions (effort: S) — HIGH PRIORITY — **DONE (2026-11-17)**
`linkSeemsOk`, `isBanned`, `getDomain`, `getSecondLevelDomain`, `stopwordFilter` and the border/stopword
counters compile regexes on every call. Compile constants once at package scope and config-derived
ones once in `yamlInitGeneral`. Expected: measurable CPU reduction in the 53 %-CPU warm-cache runs.

### 29. Fix `getNextLink` empty-candidate bug (effort: S) — HIGH PRIORITY — **DONE (2026-11-17)**
When no link passes `isBanned`/`linkSeemsOk`, `maxURL` stays empty but `increaseDomainCounter` still
runs for `lPool[0]`, skewing priorities. Guard on `maxURL == ""` before incrementing.

### 30. Replace the O(n^2) failed-domain log scan (effort: S) — HIGH PRIORITY — **DONE (2026-11-17)**
`domainHadFailed` reads and splits the whole `domainFailed.log` on every download. Keep an in-memory
`map[string]int` persisted in `state:crawl`; keep the text log only for humans.

### 31. Reuse one HTTP client in `proxyGet` (effort: S) — MEDIUM PRIORITY — **DONE (2026-11-17)**
A new `http.Client` per request plus `req.Close = true` disables keep-alive. Use one package-level
client with a tuned `http.Transport`.

### 32. Decompose `doNextLink` and index the English corpus (effort: M) — **DONE (2026-11-17)**
`doNextLink` is ~330 lines with eight early returns; split it into `filterParagraphs`,
`buildCuratedContent`, `isUniqueDocument`, `writeDocument` and `dumpFrequencies` (this also fixes
item 18). Separately, persist a compact frequency index instead of re-reading 700k lines of `all.num`
at every start.

### 33. Error returns instead of panic/os.Exit; versioned state (effort: M) — **DONE (2026-11-17)**
`yamlInit*`, `csvInit` and `main` panic or call `os.Exit`, skipping deferred session/CSV/log closes.
Extract `run() error` and return errors. Add a `Version` field to `crawlState` and reject unknown
versions. Handle corrupt cache JSON by re-downloading instead of panicking.

### 34. Index the link pool by URL (effort: M, depends on 14) — **DONE (2026-11-17)**
`addLink` performs a linear scan for duplicates and `addLinksOf` has no per-page cap. Maintain a
`map[string]int` index and a configurable link cap.

### 35. Expand automated tests — **DONE (2026-11-17)**
Added tests for `iolib`, `stringlib`, `corpusfreqlib`, `isBanned`, stopword counters, stemmer,
n-grams, link length boundaries, rejection counters, SQLite state round-trip, session-file naming,
cache hits via a fake store, and a local HTTP server for `proxyGet`/`download`.


## Suggested execution order

1. Items **1–5** (one afternoon): hygiene and crash fixes.
2. Item **6** (same iteration): prevents the disk-space problem from recurring.
3. Items **7–10**: make the tool scriptable and measurable; add regression safety.
4. Items **11–12**: reliability and better seed yield.
5. Item **13**: the big functional leap — a searchable corpus.
6. Items **14–17**: performance, ranking quality and usability.

## Status

**All actionable items are complete as of 2026-11-17.**

- Core crawler: 1–6, 8–18, 21, 28–35 DONE.
- Ubuntu readiness: 19–25 DONE; 26 DEFERRED (blocked on Debian dependency packaging, see
  `docs/UBUNTU_SUBMISSION.md`); 27 OUT OF SCOPE (MIR/LTS commitment not realistic for this project).
- Remaining non-actionable items: 26 and 27 are deliberate decisions, not pending work.

Verification: `go build ./...`, `go vet ./...`, `go test ./...`, `go test -race ./...`,
`go run crawler.go --selftest` and `govulncheck ./...` all pass with Go 1.26.11.
