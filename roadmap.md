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

### 7. Real CLI flags
**Problem:** configuration is YAML plus one positional argument; switching backends requires
editing `crawler.yaml`.
**Proposal:** use `pflag` (already in the dependency tree) for `--profile`, `--backend`,
`--cache-path`, `--max-links`, `--verbose`, `--dry-run`.
**Impact:** scriptable, no file edits needed for common options.

### 8. Load profiles from `profiles/` directly
**Problem:** the YAML must be copied to the working directory before every run.
**Proposal:** search `./profiles/<name>.yaml` first, then fall back to `./<name>.yaml` for backwards
compatibility.
**Impact:** removes a manual, error-prone step.

### 9. Unit tests for the crawling and filtering logic
**Problem:** only the cache backends have tests. Core functions (`linkSeemsOk`, `rankingByKeywords`,
`analyze`, stopword filtering, URI validation) are untested.
**Proposal:** table-driven tests with representative URLs, English/non-English paragraphs and
boundary cases for every threshold.
**Impact:** safe refactoring and reproducible behaviour.

### 10. Structured logging and rejection counters
**Problem:** dozens of `fmt.Printf` calls produce unstructured output; it is hard to know why
documents were discarded.
**Proposal:** migrate to `log/slog` with levels and emit a summary of rejections by reason
(language, length, relevance, duplicate, banned domain).
**Impact:** profile tuning becomes measurable instead of guesswork.

### 11. Resume support
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

### 13. Full-text search over the corpus using SQLite FTS5
**Problem:** the crawler builds a useful corpus but offers no way to query it.
**Proposal:** store curated documents in the existing SQLite database with an FTS5 virtual table
and add a query command/CLI that returns matching documents and snippets.
**Impact:** turns the corpus into a real search tool without adding a new service. **Highest-value
item in this roadmap.**

### 14. Concurrent workers with per-domain rate limiting
**Problem:** crawling is completely sequential; fetching hundreds of pages is slow.
**Proposal:** introduce a bounded worker pool with a shared queue and per-domain rate limits.
Requires making `lPool`, `domainCounter`, the logs and the SQLite writes thread-safe.
**Impact:** large speed-up, but significant refactoring and new race-condition risks.

### 15. Better ranking: normalize length and add semantic similarity
**Problem:** `rankingByKeywords` divides by `sqrt(1 + tokenCount)`, so long, highly relevant
documents score low (observed with the Siril GHS tutorial). Keyword matching also misses synonyms.
**Proposal:** normalize scores by document length, consider TF-IDF or embeddings for semantic
similarity, and make the strategy configurable per profile.
**Impact:** better corpus precision and recall; requires evaluation against a labelled set.

### 16. Configurable multi-language support
**Problem:** `whatlanggo` detection only accepts English (`crawler.go:1144`); there is no parameter
to change it.
**Proposal:** add `language` and `minLanguageConfidence` to the profile and generalize the paragraph
filter; optionally support several languages at once.
**Impact:** opens the tool to non-English corpora; changes a core filter.

### 17. Profile generator and corpus exploration UI
**Problem:** writing profiles by hand is tedious, and exploring results means opening raw text and
frequency files.
**Proposal:** a helper that derives `linkOk`/`regexRankingKeywords` from a seed URL plus a few terms,
and a small CLI or web UI to browse ranked documents, frequencies and n-gram statistics (re-enabling
the dormant n-gram code).
**Impact:** lowers the entry barrier and makes the collected knowledge much more usable.

---

### 18. Fix stale frequency dumps
**Problem:** the frequency files are written inside `doNextLink()` only when a document passes every
filter *and* `numLinksProcessed % 50 == 0`. Most passing documents do not land exactly on a multiple
of 50, so `corpusFrequencies.txt` and `corpusNoEngFrequencies.txt` can end up describing a single
document even after a long crawl (observed in the 2026-11-17 astrophotography run).
**Proposal:** move the periodic dump to a place that executes for every processed link, or dump on
a final flush when the link pool is exhausted; also dump at run end regardless of the counter.
**Impact:** trustworthy frequency output; **medium priority** because it affects the quality of one
of the main results.

---

## Suggested execution order

1. Items **1–5** (one afternoon): hygiene and crash fixes.
2. Item **6** (same iteration): prevents the disk-space problem from recurring.
3. Items **7–10**: make the tool scriptable and measurable; add regression safety.
4. Items **11–12**: reliability and better seed yield.
5. Item **13**: the big functional leap — a searchable corpus.
6. Items **14–17**: performance, ranking quality and usability.

## Status

Items **1–6 and 12 are DONE** as of 2026-11-17 (item 6 was refined the same day to deduplicate
across runs, not only within a run; item 12 covers robots.txt, User-Agent and Crawl-delay, but not a
general configurable delay). Items **7–11, 13–17 and 18 remain proposed / not started**.
