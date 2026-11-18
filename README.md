# goCrawler

[![CI](https://github.com/computerphysicslab/goCrawler/actions/workflows/ci.yml/badge.svg)](https://github.com/computerphysicslab/goCrawler/actions/workflows/ci.yml)
[![Go 1.26+](https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go)](https://go.dev/)
[![Latest release](https://img.shields.io/github/v/release/computerphysicslab/goCrawler)](https://github.com/computerphysicslab/goCrawler/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

**Topic-restricted web crawler and text-mining tool for building curated English corpora.**

goCrawler does not crawl the web indiscriminately. Starting from a small set of seed URLs, it
downloads pages, extracts links and plain text, and keeps **only the documents that pass a chain of
language, length, relevance, uniqueness and topic filters**. The result is a curated, topic-specific
text corpus plus token-frequency statistics and (optionally) n-gram analysis.

By default, downloaded pages are cached in a **local SQLite database**, so no external service is
required. Redis remains supported as an alternative cache backend.

- Demo video: <https://youtu.be/7IDkNYcMXxU>
- Contributing: [CONTRIBUTING.md](CONTRIBUTING.md) · shareable starter profiles in [`profiles/examples/`](profiles/examples/)
- Author: Juan Ignacio Pérez Sacristán (`computerphysicslab`)
- Module: `goCrawler` · Go 1.26+ (`toolchain go1.26.11` in `go.mod`)

---

## Table of contents

1. [Features](#features)
2. [How it works](#how-it-works)
3. [Project layout](#project-layout)
4. [Requirements](#requirements)
5. [Build and test](#build-and-test)
6. [Configuration](#configuration)
7. [Running the crawler](#running-the-crawler)
8. [Cache backends](#cache-backends)
9. [Outputs](#outputs)
10. [Logs](#logs)
11. [Algorithms and NLP details](#algorithms-and-nlp-details)
12. [Package reference](#package-reference)
13. [Example: astrophotography image stacking](#example-astrophotography-image-stacking)
14. [Troubleshooting](#troubleshooting)
15. [Known limitations and inactive code](#known-limitations-and-inactive-code)
16. [Optional: installing Redis](#optional-installing-redis)
17. [Roadmap and author](#roadmap-and-author)

---

## Features

- **Topic-focused crawling**: a per-profile whitelist of URL tokens (`linkOk`) and trusted domains
  (`curatedDomains`) decides which links are followed.
- **Language filtering**: paragraphs are classified with `whatlanggo`; non-English paragraphs are
  dropped, so the resulting corpus is English-only.
- **Multi-stage quality filters**: stopword ratio, minimum/maximum document length, keyword score,
  near-duplicate detection and token-distribution checks.
- **Ranking and selection**: the best paragraph of each document is selected and an overall
  relevance score is computed and written to `output/ranking-URL.csv`.
- **Intercorpus contrast**: frequencies of general English (British National Corpus + contemporary
  English) are subtracted to surface *topic-specific* terms.
- **Persistent page cache**: SQLite (default) or Redis; repeated runs reuse downloaded pages and
  avoid network load.
- **Corpus export**: the Workbench Corpus tab searches with FTS5 or exports a profile-matched TXT
  file containing every curated document that fits the selected profile.
- **Zero required services**: the default setup needs only Go and a writable working directory.
- **Optional PDF ingestion**: profiles can opt in to download PDFs and index their extracted text
  with the pure-Go `ledongthuc/pdf` library (no CGO); size and page limits keep it safe.

---

## How it works

`main()` (`crawler.go:1494`) performs the following steps:

1. Create `./logs` and `./output` if missing.
2. Load `crawler.yaml` (general config), optional `proxy.yaml`, and `<profile>.yaml`.
3. Initialize the configured cache store (SQLite by default).
4. Initialize the English reference corpus (`corpusfreqlib.Init`).
5. Add the profile's seed URLs to the link pool (`linkBootstraping`).
6. Initialize `output/ranking-URL.csv` and truncate `logs/corpusCuratedText.log`.
7. Loop calling `doNextLink()` (`crawler.go:1089`) until the pool is empty.

### Link validation funnel

Every discovered link passes through `linkSeemsOk()` (`crawler.go:340`):

| # | Check | Failure destination |
|---|---|---|
| 1 | Length between 13 and 256 characters | discarded silently |
| 2 | `net/url.ParseRequestURI` | `logs/linksNotOk.ParseRequestURI.log` |
| 3 | Strict URL regex (scheme, host, port, safe path characters only) | `logs/linksNotOk.additional.log` |
| 4 | Profile whitelist: `linkOk` or `curatedDomains` | `logs/linksNotOk.log` |
| 5 | Blacklists (`regexBannedDomains`, `regexLinkBannedTokens`) and empty domain | discarded silently |

The strict validator was added after malformed values such as `http://(http://...` caused panics;
its log also captures `javascript:`, `mailto:`, `tel:`, relative paths and URLs with query strings.

### Link scheduling

`getNextLink()` (`crawler.go:391`) prioritizes links by `Count² / (domainCounter[domain] + 1)`,
so less-crawled domains and frequently seen URLs win. Seed links are processed first. A domain with
more than six transport failures is permanently skipped for that run. Links belonging to the same
second-level domain as the current page are not added (`addLinksOf`, `crawler.go:1070`).

### Document processing

For each downloaded page (`doNextLink`, `crawler.go:1089`):

1. Split into paragraphs; drop paragraphs not detected as English.
2. Strip URLs, HTML tags, very long tokens (> 80 chars) and markup leftovers.
3. Compute the stopword ratio; drop paragraphs below 0.1.
4. Build `curatedContent` from paragraphs of at least 200 characters with stopword ratio 0.1–0.38.
5. Apply `minDocLen` / `maxDocLen`.
6. Reject near-duplicates using a signature of the seven most frequent tokens.
7. Apply `scoreThreshold`.
8. Check `maxFreq / numWords` in the 0.002–0.1 range.
9. Append the document to the corpus and write a row to the ranking CSV.
10. Every 50 processed links, refresh the frequency files and JSON dumps.

---

## Project layout

| Path | Purpose |
|---|---|
| `crawler.go` | Single `package main` entry point (~1560 lines): downloading, filtering, scoring, output. |
| `storelib/` | Pluggable cache interface (`Exists`, `Get`, `Set`, `Close`) with SQLite and Redis backends. |
| `redislib/` | Redis client wrapper built on `redigo` (single shared connection, TCP `:6379`). |
| `corpusfreqlib/` | English reference frequencies from the British National Corpus and a contemporary English corpus; `Freq(token)` exposes standard frequencies. The tracked `freq-index.json.gz` is authoritative; see [docs/FREQUENCY_DATA.md](docs/FREQUENCY_DATA.md). |
| `iolib/` | File helpers: `FileExists`, `CopyFileContents`, `String2file`, `String2fileAppend`, `File2string`. |
| `stringlib/` | String helpers: `RmNewLines`, `IsNumeric`. |
| `profiles/` | Example per-topic YAML profiles (Crypto, Litecoin, COVID-19, VAERS, Machine Learning, Revolution, astrophotography, ...). |
| `crawler.yaml` | General configuration shared by all profiles. |
| `logs/` | Runtime logs and curated corpus (ignored by Git). |
| `output/` | Ranking CSV, frequency lists and JSON dumps (ignored by Git). |
| `storelib/store_test.go` | Tests for both cache backends. |

---

## Requirements

- **Go 1.26 or newer** (`go 1.26.0`, toolchain `go1.26.11` in `go.mod`). If your `go` is older
  (e.g. 1.20.3), it cannot parse `go.mod`; install the 1.26 toolchain with
  `go install golang.org/dl/go1.26.11@latest && go1.26.11 download`, then either call
  `go1.26.11 run crawler.go ...` or put a symlink to it earlier in `PATH`
  (`ln -sf ~/sdk/go1.26.11/bin/go ~/.local/bin/go`). The Makefile uses `GO ?= go1.26.11`.
- A writable working directory.
- **No Redis required** for the default setup. Redis 5+ is only needed if `cacheBackend: redis` is
  selected. The SQLite driver (`modernc.org/sqlite v1.31.0`) is pure Go: no CGO or system SQLite
  library is needed.

---

## Build and test

```shell
go1.26.11 build ./...   # compile all packages (or: make build)
go build -o crawler crawler.go
go vet ./...            # static checks
go test ./...           # cache backend tests
gofmt -l .              # formatting check (should print nothing)
```

The same commands are available as `make` targets: `make build`, `make test`, `make fmt`,
`make vet`, `make run PROFILE=<name>`, `make selftest` and `make clean`.

Every function in the Go sources has an English doc comment. Automated tests live next to the code
they exercise (`crawler_test.go`, `storelib/store_test.go`, `robotspolicy/robotspolicy_test.go`).

### Built-in self-test

The binary can verify itself without network access or a profile file:

```shell
go run crawler.go --selftest     # or: make selftest
```

It checks tokenisation, domain extraction, keyword ranking, n-grams, the English reference corpus,
the robots policy, frequency counting, the local SQLite store, invalid-backend handling and
rejected-link deduplication. It prints `[PASS]`/`[FAIL]` per check and exits with status 0 only when
all checks pass.

---

## Configuration

### General configuration (`crawler.yaml`, in the working directory)

| Key | Meaning |
|---|---|
| `regexBannedDomains` | Domains never followed (social networks, search engines, GitHub, Wikipedia, etc.). |
| `regexLinkBannedTokens` | URL tokens/extensions rejected (login, search, PDF, images, archives, ...). |
| `engStopWordsWOthe` | English stopword list; `"the"` is prepended automatically. |
| `engLowRelevancyWords` | Low-information words added to the stopword regex. |
| `specialStopwords` | URL parts, months, file extensions and TLDs for cleaning. |
| `downloadTimeout` | HTTP timeout in seconds (default `8`). |
| `addUrl` | Optional external indexer endpoint (currently commented out). |
| `cacheBackend` | `local` (SQLite, default) or `redis`. |
| `cachePath` | SQLite database path (default `./cache/cache.db`). |
| `notifyWebhookURL` | Optional JSON webhook for crawl notifications (treated as a secret). |
| `notifyDesktop` | Use the OS notifier (`notify-send`/`osascript`/PowerShell), default `false`. |
| `notifyOnCompletion` / `notifyOnError` | Emit those events once a channel is configured, default `true`. |
| `rejectionNotifyThreshold` | Notify once per rejection reason when its counter reaches N (`0` disables). |
| `maxDownloadMB` | Maximum size of a downloaded page in MiB (default `5`). |
| `logLevel` | Structured log level: `debug`, `info`, `warn` or `error` (default `info`). |
| `respectRobots` | Fetch and honour `robots.txt` before each online download (default `true`). |
| `userAgent` | User-Agent sent for page and `robots.txt` requests. |

### Profile configuration (`<profile>.yaml`)

```yaml
curatedDomains: |        # trusted domains (regex alternation)
  example\.com|other\.org

linkOk: |                # tokens that must appear in a URL
  stack|stacking|imaging

regexRankingKeywords: |  # case-insensitive regex used to score paragraph content
  (?i)\W(stack|imaging|sensor)\W

minDocLen: 50            # minimum curated text length
maxDocLen: 100000        # maximum curated text length
scoreThreshold: 20       # minimum relevance score (default 0 if omitted)

process_pdf: true        # allow downloading PDFs (default false)
pdf_max_bytes: 26214400  # per-PDF size cap in bytes (default 25 MiB)
pdf_pages_limit: 200     # maximum pages to extract (default 200)

bootstrapingLinks:       # seed URLs
- "https://example.com/start/here"
```

The effective URL whitelist is built as
`(?i)^https*://.*(<linkOk>|<curatedDomains>)` in `yamlInitSpecific()` (`crawler.go:1471`).

`yamlInitSpecific()` looks for `<name>.yaml` in `profiles/` first and falls back to the working
directory for backwards compatibility. The argument to `go run crawler.go <name>` is the profile
name without its extension; the resolved path is printed as `profile file:` at startup.

PDF URLs stay in `regexLinkBannedTokens` by default. Setting `process_pdf: true` bypasses that
single token check for `.pdf` URLs when the profile is loaded; banned domains and the `linkOk`
whitelist still apply. Extracted text is cached under a separate `pdf_` prefix.

### Proxy (optional)

If `proxy.yaml` exists in the working directory, `proxyHost`, `proxyUser` and `proxyPass` are read
and used for HTTP requests. It is not included in the repository.

---

## Running the crawler

```shell
make run PROFILE=crypto             # resolves profiles/crypto.yaml
go run crawler.go astrophotography  # resolves profiles/astrophotography.yaml
go run crawler.go astrophotography --resume   # restore link pool and counters from SQLite

# other CLI options
go run crawler.go -profile=crypto -backend=local -workers=4 -log-level=debug
go run crawler.go -dry-run -profile=crypto
go run crawler.go -query=drizzle               # search the curated corpus (SQLite FTS5)
go run crawler.go -explore                     # corpus stats and top terms
go run crawler.go -generate-profile=https://example.org/stacking
go run crawler.go -version -license
```

The crawler runs until its link pool is exhausted. Progress is printed to stdout, including
`[ONLINE]` (fetched) or `[CACHE]` (served from the cache) for each URL, and a summary every 50
links. The profile argument is mandatory; omitting it prints usage and exits with status 1.
`go run crawler.go --help` also prints usage.

---

## Cache backends

The cache stores downloaded pages under the key `cache_<URL>` with a JSON value of
`{"Content": "...", "Links": [...]}`.

### SQLite (default)

```yaml
cacheBackend: local
cachePath: ./cache/cache.db
```

- Pure-Go driver `modernc.org/sqlite`, no external service and no CGO.
- Creates the parent directory and the `cache` table automatically
  (`key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at DATETIME`).
- WAL journal mode, `synchronous=FULL` (disk-synchronized commits), `busy_timeout=5000`, one open
  connection.
- `Set` uses `INSERT ... ON CONFLICT(key) DO UPDATE`, so repeated runs update existing entries.
- The cache is persistent across restarts; delete the file to start from scratch.

### Redis (optional)

```yaml
cacheBackend: redis
```

- Requires a Redis server on `127.0.0.1:6379` (see [Optional: installing Redis](#optional-installing-redis)).
- If Redis is unavailable, `storelib.Init` returns an error and the program exits with a clear
  message instead of panicking.
- `redislib` no longer calls `panic` or `log.Fatal` on connection failures.

Both backends implement the same four operations, so switching requires no code changes.

---

## Robots.txt policy

Before every **online** download, the crawler asks the `robotspolicy` package whether the URL is
allowed:

- one `robots.txt` is fetched and cached per host (scheme + host);
- the correct rule group is selected with the configured `userAgent`;
- disallowed URLs are skipped and recorded (deduplicated) in `logs/robotsBlocked.log`;
- a `Crawl-delay` declared by the site is enforced between requests to that host;
- a missing (404) or unreachable `robots.txt` means **allow** (fail open), with the error printed.

Cached pages are not re-checked, so a warm cache does not generate extra `robots.txt` traffic.
Set `respectRobots: false` in `crawler.yaml` to disable the policy.

### CPU usage and parallelisation

The crawler is sequential: it does **not** use all available cores. In a controlled 106-second run
on an 8-core machine it consumed 24.78 s user + 0.88 s system CPU, about **24 % of one core**
(mostly waiting on the network). `GOMAXPROCS` defaults to 8, but no crawling work is parallelised.

**Can downloads be parallelised?** Yes, but the goal is overlapping *network latency*, not using the
8 CPU cores: the workload is I/O-bound, so even a single core could drive many concurrent HTTP
requests. Eight workers is a reasonable upper bound, not a requirement.

The crawler is currently a single loop (`getNextLink` → `downloadCached` → filter → write output).
These globals are mutated from that loop and would need protection or ownership separation:

| Shared state | Location | Risk when parallel |
|---|---|---|
| `lPool []aLink` | `crawler.go:97` | status updates and appends race |
| `domainCounter map[string]int` | `crawler.go:104` | priority calculation races |
| `uniqueSignature map[string]string` | `crawler.go:966` | duplicate documents accepted twice |
| `corpusFreqs freq` | `crawler.go:967` | map writes race |
| `csvWriter` | `crawler.go:66` | `csv.Writer` is not concurrency-safe |
| `logRejectedLink` sets and file appends | `crawler.go:327` | dedup and interleaved writes |
| `cacheStore` | SQLite `SetMaxOpenConns(1)` / single Redis connection | serialises all traffic |
| `robotsPolicy` | `robotspolicy/` | already mutex-protected; per-host delay needs reserving |

Pure functions (tokenisation, language detection, HTML conversion, ranking) need no changes;
config and the English reference corpus are read-only after initialisation.

A safe design is a dispatcher goroutine owning `lPool`/`domainCounter`, N workers doing download
and filtering, and an aggregator goroutine owning all result structures and file writes. Each worker
must also respect per-domain politeness (including `Crawl-delay`). Expected gain is a large reduction
in wall time (network latency dominates), but CPU usage will stay low and output ordering will change.
This is roadmap item 14; it is **not implemented**. Use `go test -race` when implementing.

## CLI options

| Flag | Meaning |
|---|---|
| `-profile=NAME` | profile name (the first positional argument also works) |
| `-backend=local\|redis`, `-cache=PATH` | override cache settings from the command line |
| `-workers=N` | concurrent download workers (1–32, default 1); shared state is mutex-protected |
| `-log-level=LEVEL` | `debug`, `info`, `warn` or `error` |
| `-max-links=N` | maximum links added from one page |
| `-dry-run` | load configuration and exit without crawling |
| `-query=TEXT` | full-text search over the curated corpus (SQLite FTS5) |
| `-explore` | print corpus statistics and top topic terms |
| `-generate-profile=URL` | write `profiles/generated.yaml` from a seed URL |
| `-resume`, `--resume` | restore the previous crawl from SQLite |
| `-version`, `-license` | print version or license and exit |
| `-gui`, `-gui-port=N` | start the embedded web Workbench on `127.0.0.1:N` (default 8765); only one instance per user is allowed, and a busy port is an error (no silent fallback) |

Only one Workbench can run per user: a lock file in the user cache directory (`~/.cache/goCrawler/gui.lock`
on Linux) records the running PID and port. Starting a second instance prints an error instead of
silently binding another port; stale locks left by crashes are recovered. Ctrl+C or SIGTERM shuts the
server down gracefully and removes the lock.

The **Corpus** tab combines FTS5 search with a profile selector: choosing a profile and clicking
**Save corpus** writes `output/corpus-<profile>.txt` with all accepted documents whose URL and text
match that profile, then offers a download link. The Workbench ends with an **About** tab showing
version, copyright, MIT license, author, links, platform, bundled components and a QR code pointing
to the repository.

`-h`/`--help` prints the usage summary. Multi-language corpora can be selected with the profile
keys `language` (default `English`) and `minLanguageConfidence`.

## Logging, rejection counters and resume

Runtime messages are emitted with structured `slog` records (`time=... level=... msg=... key=value`)
at the level configured by `logLevel`. Every filter increments a rejection counter; progress and the
final summary log the counts by reason (`non_english`, `stopword_ratio`, `too_short`, `too_long`,
`duplicate`, `low_relevancy`, `token_distribution`, `invalid_url`, `off_topic_url`,
`robots_blocked`). This makes profile tuning measurable instead of guesswork.

The link pool, domain counters, uniqueness signatures and accumulated frequencies are saved in the
SQLite store under `state:crawl` every 50 links and at shutdown. Running with `--resume` restores
them, appends to `ranking-URL.csv` and the corpus instead of truncating them, and continues from the
last processed index. Without `--resume`, any previous state is cleared at startup.

### Notifications (optional)

Set `notifyWebhookURL` in `crawler.yaml` to receive JSON POSTs, and/or `notifyDesktop: true` to use
the local notifier (`notify-send`, `osascript` or PowerShell). Events are `crawl_completed`,
`crawl_error` and `rejection_threshold`; completion/error notices are on by default once a channel
is configured and can be disabled with `notifyOnCompletion` / `notifyOnError`. Set
`rejectionNotifyThreshold: 1000` to get one notice per rejection reason when its counter is
reached. Webhook URLs are treated as secrets and are never logged.

## Security scan

`govulncheck ./...` reports **no vulnerabilities** with the pinned toolchain and dependencies
(`golang.org/x/net v0.62.0`, `gopkg.in/yaml.v2 v2.4.0`). Re-run it after every dependency update.

Packaging drafts are included: `snap/snapcraft.yaml` (strict confinement), `debian/` (source
skeleton for a PPA), AppStream metadata and a desktop launcher in `assets/`. See
[`docs/UBUNTU_SUBMISSION.md`](docs/UBUNTU_SUBMISSION.md) for the decisions and remaining blockers.

## Distribution and audit readiness

Packaging options (PPA, Snap Store, Debian/Ubuntu Universe, Main) and the work needed before each
are analysed in [`roadmap.md`](roadmap.md), section "Ubuntu / Debian distribution readiness".
The first blocker is the absence of an open-source `LICENSE` file.

## Outputs

All files live in `output/` and are ignored by Git.

| File | Content |
|---|---|
| `ranking-URL.csv` | Tab-separated: score, curated document length, signature (7 top tokens), URL, best paragraph. |
| `corpusFrequencies.txt` | Raw token frequencies in British National Corpus format: `frequency word none 0`. |
| `corpusNoEngFrequencies.txt` | Topic-specific frequencies after subtracting standard English (`intercorpusContrast = 20`). |
| `LPool.json` | Snapshot of the link pool (`aLink` records: URL, domain, count, status). |
| `domainCounter.json` | Per-domain visit counters. |
| `*_keywords.txt` | Manually exported keyword lists for some historical topics (not generated by the code). |

The curated text itself is written to `logs/corpusCuratedText.log`: each document is the URL, a blank
line, the curated text, and a `----` separator.

---

## Logs

| Log | Behaviour | Meaning |
|---|---|---|
| `corpusCuratedText.log` | truncated at startup | Current run's curated corpus. |
| `corpusCuratedText-*.log` | manual | Renamed corpora from previous runs (Crypto, Litecoin, VAERS, ...). |
| `download.log` | truncated at startup | One entry per download: URL, status, plain-text and link counts. |
| `domainFailed.log` | truncated at startup | Domains with transport errors; more than 6 failures discards a domain. |
| `cgi.log` | truncated at startup | Links whose query string was removed during canonicalization. |
| `linksNotOk.log` | **append-only** | Links failing the profile whitelist. |
| `linksNotOk.additional.log` | **append-only** | Links failing the strict URL regex. |
| `linksNotOk.ParseRequestURI.log` | **append-only** | Links failing Go's URI parser. |
| `robotsBlocked.log` | **append-only, deduplicated** | URLs skipped because `robots.txt` disallows them. |
| `<Profile>-YYYY-MM-DD-ZHH-MM-SS.txt` | one per run | Full console output of the session (see below). |

### Session output files

Every run tees its console output to `logs/<Profile>-<YYYY-MM-DD>-Z<HH>-<MM>-<SS>.txt`, where the
timestamp is **UTC** (the `Z` prefix). Example: `Astrophotography-2026-11-17-Z20-30-33.txt`.
The profile name is capitalised; the file contains the complete output from configuration loading
to the final `Done!!!` message, including `[CACHE]` / `[ONLINE]` / `[ROBOTS-BLOCKED]` markers.

### Measured cache benefit

Two consecutive runs with the same `astrophotography` profile on this machine:

| Run | Wall time | Cache hits | Online downloads | Documents | CPU |
|---|---:|---:|---:|---:|---:|
| Cold (empty cache) | 194 s | 0 | 76 | 35 | 16 % of one core |
| Warm (populated cache) | 45 s | 76 | 0 | 35 | 53 % of one core |

The warm run was about **4.3× faster**, produced exactly the same 35 documents and made zero HTTP
requests. The SQLite file was 32 MB for those 76 pages. Note that the warm run is more CPU-dense
(53 % vs 16 %) because it no longer waits on the network, but it is still single-threaded.

> **Warning:** the three `linksNotOk*.log` files grow without limit and are never deduplicated. In
> historical runs they reached about 1.9 GB (over 45 million lines), mostly repeated social-share and
> navigation links. They are safe to delete; the useful knowledge extracted from them is recorded
> in `context-ai.md` (in the repository root of the workspace).

---

## Algorithms and NLP details

- **Tokenization** (`tokenize`, `crawler.go:535`): split on every non-alphanumeric character.
- **Stopword filtering** (`analyze`, `crawler.go:679`): lowercase and remove stopwords; the Snowball
  stemmer is implemented but disabled.
- **Relevance scoring** (`rankingScore`): three configurable strategies. `keywords` keeps the legacy
  `100 * uniqueMatches / sqrt(1 + tokenCount)` formula; `tfidf` computes the share of the document's
  IDF weight matched by the profile vocabulary (length-normalised); `hybrid` averages both. The
  profile `synonyms` block expands keyword matching to related terms.
- **Best paragraph** (`bestParagraph`, `crawler.go:590`): the highest-ranked paragraph, capped at
  2000 characters.
- **Uniqueness**: a document is rejected if its seven most frequent tokens match an already accepted
  document.
- **Frequency contrast**: standard English frequencies (BNC + contemporary) are normalized to
  `"the"` and subtracted with a contrast factor of 20 to highlight topic-specific vocabulary.
- **N-grams**: `ngramsOf`, `ngramsFreqsOf` and `ngramsFreqsOfAll` are implemented, but the global
  n-gram block in `main()` is disabled behind `if false`. `bigramsOf` is fully commented out.

---

## Package reference

**`crawler.go` (main)**

- Configuration: `yamlInitGeneral`, `yamlInitProxy`, `yamlInitSpecific`
- Fetching: `proxyGet`, `download`, `downloadCached`
- Links: `linkSeemsOk`, `getNextLink`, `addLink`, `linkBootstraping`, `addLinksOf`,
  `isBanned`, `getDomain`, `getSecondLevelDomain`, `domainReportFailed`, `domainHadFailed`
- Text: `tokenize`, `splitParagraphs`, `rankingByKeywords`, `bestParagraph`, `analyze`,
  `stopwordFilter`, `stemmerFilter`, `freq.add`, `rSortFreq`
- Output: `csvInit`, `lPoolDump`, `domainCounterDump`
- N-grams: `ngramsOf`, `ngramsFreqsOf`, `ngramsFreqsOfAll`
- Main loop: `doNextLink`, `main`

**Libraries**

- `storelib`: `Init(backend, path)` plus the `Store` interface.
- `redislib`: `Ping`, `Set`, `Get`, `Exists`, `SetInterface`, `GetInterface`.
- `corpusfreqlib`: `Init`, `Freq`, `Test`.
- `iolib`: `FileExists`, `CopyFileContents`, `String2file`, `String2fileAppend`, `File2string`.
- `stringlib`: `RmNewLines`, `IsNumeric`.

---

## Example: astrophotography image stacking

A ready-to-use English profile is included as `profiles/astrophotography.yaml`. It targets
alignment/registration and integration/stacking of astronomical images with keywords such as
`stack`, `registration`, `integration`, `subframe`, `signal-to-noise`, `dither`, `drizzle`, `fwhm`,
`pixinsight` and `siril`.

```shell
go run crawler.go astrophotography
```

In a 2026-11-17 test run, the crawler produced 29 English documents (272 KB corpus) from seeds on
Sky at Night, Siril, DeepSkyStacker, Cloudy Nights and AstroBackyard. Top-ranked sources were the
Sky at Night stacking guide (129.6), Miklosoft stacking & rejection (117.2) and Siril tutorials
(113.2). Topic-specific frequencies included `stacking`, `frames`, `histogram`, `siril`,
`deepskystacker` and `pixinsight`.

---

## Troubleshooting

| Symptom | Cause / fix |
|---|---|
| `panic: runtime error: index out of range` at startup | Profile argument missing; run `go run crawler.go <profile>`. |
| `unknown cacheBackend "..."` | Fix `cacheBackend` in `crawler.yaml`: use `local` or `redis`. |
| `Cache store error: ... permission denied` | The process cannot create `cache/`; fix directory permissions or set `cachePath` to a writable path. |
| Pages download as ~125 characters with 0 links | The site blocked the request or requires JavaScript. Replace the seed URL with a crawler-friendly page. |
| No documents in the corpus | Lower `scoreThreshold`, loosen `linkOk`, or add better seed URLs. |
| `linksNotOk*.log` growing quickly | Expected: social-share and navigation links are rejected in bulk. Delete the logs; consider the deduplication item in `roadmap.md`. |
| Redis connection errors with `cacheBackend: redis` | Start Redis (`redis-cli ping` should print `PONG`) or switch to `local`. |

---

## Known limitations and inactive code

- Single-threaded crawling; no `robots.txt` handling and no politeness delay between requests.
- No CLI flags; configuration is YAML plus the single positional profile argument.
- No resume support: `LPool.json` and `domainCounter.json` are written but never read back.
- No formal tests for the crawling/filtering logic; only the cache backends are tested.
- No license file in the repository.
- Inactive code: global n-gram block (`if false`), `bigramsOf()` commented out, legacy
  `go-cache`/gob cache functions, `gob.Register(CachedData{})`, and a commented stack trace at the
  end of `crawler.go`.
- The legacy `cacheInit`/`cacheSave` functions and the `go-cache` dependency are kept for reference
  but are not used by the running program.

---

## Optional: installing Redis

Redis is only needed when `cacheBackend: redis` is configured. On Ubuntu/Debian:

```shell
sudo apt-get update
sudo apt-get install redis-server

sudo systemctl start redis        # or: sudo /etc/init.d/redis-server start
redis-cli ping                    # expected: PONG

# useful commands
sudo systemctl stop redis
sudo systemctl restart redis
ps aux | grep redis
```

By default Redis listens on `127.0.0.1:6379`, which is the hard-coded endpoint in `redislib`.

---

## Testing

The suite has 65 tests: unit tests for configuration validation, ranking (keyword/TF-IDF/semantic),
state persistence, download errors, link filtering, stores and robots policy; HTTP handler tests for
every Workbench endpoint via `httptest`; and an end-to-end crawl against a local HTTP server with two
workers.

```shell
go test ./...                       # all packages
go test -race ./...                 # concurrency checks
go test -cover ./...                # coverage report
go run crawler.go --selftest        # built-in binary self-test
```

Coverage: main package ~68 %, iolib ~74 %, robotspolicy ~82 %, storelib ~67 %, corpusfreqlib ~50 %.

## Versioning and CI

Releases, version tags and details of every milestone are in [`CHANGELOG.md`](CHANGELOG.md).
GitHub Actions run build/vet/test/self-test on Linux, Windows and macOS (`.github/workflows/ci.yml`);
tagging a `v*` version builds cross-platform binaries and a Debian `.deb` package
(`.github/workflows/release.yml`).

## License

Released under the [MIT License](LICENSE). Copyright (c) 2020-2026 Juan Ignacio Pérez Sacristán.

## Roadmap and author

Planned improvements, ordered from simplest to most complex, are listed in [`roadmap.md`](roadmap.md).
A function-by-function review with proposed refactorings is in [`docs/FUNCTION_ANALYSIS.md`](docs/FUNCTION_ANALYSIS.md).

Author: Juan Ignacio Pérez Sacristán — <computerphysicslab@gmail.com> —
[LinkedIn](https://www.linkedin.com/in/semanticwebarchitect/).

Interests: text mining, NLP, named entity recognition, and building text corpora and search
applications for the health/biomedical/clinical domain.
