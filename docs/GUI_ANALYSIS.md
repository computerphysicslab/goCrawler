# GUI design analysis — goCrawler Workbench

Status: **proposed** (roadmap item G-CORE). This is the design analysis requested for a graphical
tool that manages search profiles and makes crawler results explorable and reusable.

## 1. Goal and user stories

The CLI is excellent for automation but poor for iterative tuning. The GUI must let a researcher:

1. **Manage profiles**: create, duplicate, rename and delete profiles; edit every field (`linkOk`,
   `curatedDomains`, `regexRankingKeywords`, `minDocLen`, `maxDocLen`, `scoreThreshold`,
   `rankingStrategy`, `synonyms`, `language`, `maxLinksPerPage`, `bootstrapingLinks`).
2. **Edit keywords and seed URLs** interactively: add, remove, bulk-paste, enable/disable individual
   entries, validate URLs live, and see which keywords actually matched the curated corpus.
3. **Run and monitor**: start/stop a crawl, observe pages visited, cache hits vs online downloads,
   download time per page and cumulative, rejection counters by reason, and robots.txt blocks.
4. **Inspect results**: read the curated text of every accepted document with copy-to-clipboard,
   full-text search (reusing the FTS5 index) and filters by domain/score.
5. **Extract and feed back**: run keyword extraction over the curated text (the logic currently in
   `tools/corpus_feedback.py`), review the proposed additions with a diff, and apply them to the
   profile with one click; then re-run the crawler — a closed feedback loop.

## 2. Recommended technology decision

Three viable options were considered:

| Option | Pros | Cons |
|---|---|---|
| Fyne (pure Go GUI) | Native windows, single binary | Heavy dependency tree; custom widgets for text-heavy lists are awkward; theming limited |
| Wails (Go + web frontend) | Rich UI, small runtime | Requires Node toolchain and embedding complexity; cross-compilation harder |
| **Local web UI (Go stdlib + embedded HTML/CSS/JS)** | Zero new dependencies, works on Windows/Linux/macOS through the browser, excellent text rendering and copy/paste, trivially cross-compiled, reuses existing HTTP/SQL code | Runs in a browser rather than a native window (can later be wrapped in Wails/Fyne without changing the backend) |

**Decision: local web UI**, exposed only on `127.0.0.1` and started with `gocrawler --gui` (optional
`-gui-port`). All assets are embedded with `go:embed`, so the release remains a single binary. This
is the lowest-effort, highest-power path and keeps the CLI untouched.

## 3. Architecture

```
Browser  <--HTTP/JSON-->  gui.Server (Go stdlib)  <-->  Crawler runtime
                                 |                        |
                                 +--> SQLite (cache, state:crawl, corpus_fts)
                                 +--> profiles/*.yaml (read/write, validated)
                                 +--> logs/corpusCuratedText.log (curated text)
```

- `gui/server.go`: routes, JSON DTOs, embedded assets.
- `gui/profile.go`: load/save YAML using Viper; preserves comments where possible or regenerates a
  canonical, documented profile.
- `gui/keys.go`: keyword extraction ported from `tools/corpus_feedback.py` into Go so the GUI has no
  Python dependency.
- `gui/events.go`: server-sent events (SSE) stream pushing progress records from the crawl loop.
- The crawler gains a `ProgressSink` interface; the CLI uses a no-op sink, the GUI an SSE sink.

No authentication is required because the server binds to loopback only. All file paths are
validated against the project root to avoid path traversal.

## 4. Screens and interactions

### 4.1 Dashboard
Cards: current profile, status (idle/running), links processed, documents accepted, cache hit rate,
elapsed time, and a live event log. Buttons: Start, Stop, Resume.

### 4.2 Profile editor
Two-column layout: left the tree (General / Keywords / Domains / Seeds / Synonyms / Advanced),
right the form. Keywords and seeds are tag-style chips with inline add/remove. A "Validate" button
compiles all regexes and reports errors before saving. "Duplicate profile" creates a copy.

### 4.3 Run monitor
Table with one row per processed URL: URL, status, source (CACHE/ONLINE), download time in ms,
result (accepted / rejected + reason). Aggregated bar chart of rejection counters and cache ratio.
Uses SSE so the page updates without polling.

### 4.4 Corpus viewer
Left: list of accepted documents (score, domain, date). Right: the **curated text** exactly as stored
in `logs/corpusCuratedText.log`, rendered in a selectable monospace block with a Copy button and a
Download button. Search box uses the FTS5 index.

### 4.5 Keyword lab
Select one or many curated documents (or all). Compute token frequencies with BNC contrast, show a
ranked table with count, document frequency and score. Tick the terms to add; a diff panel shows the
exact changes to `linkOk` and `regexRankingKeywords`. Buttons: "Apply to profile" and "Apply and
re-run". This is the feedback loop the user asked for.

## 5. API sketch

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/profiles` | list profiles |
| GET/PUT | `/api/profiles/{name}` | read/write a profile |
| POST | `/api/profiles/{name}/duplicate` | duplicate |
| POST | `/api/crawl` | start (`profile`, `workers`, `resume`) |
| POST | `/api/crawl/stop` | request graceful stop |
| GET | `/api/events` | SSE progress stream |
| GET | `/api/documents?query=&domain=` | curated documents / FTS5 search |
| GET | `/api/documents/{id}/text` | full curated text (copy/paste source) |
| POST | `/api/keywords/extract` | extract keywords from selected documents |
| POST | `/api/profiles/{name}/keywords` | apply extracted keywords (diff-reviewed) |
| GET | `/api/stats` | counters, cache hits, timings |

## 6. Data model (DTOs)

`ProfileDTO{Name, LinkOk[]string, CuratedDomains[]string, RankingKeywords[]string, Synonyms map[string][]string, BootstrapLinks[]string, MinDocLen, MaxDocLen int, ScoreThreshold float64, RankingStrategy, Language string, MaxLinksPerPage int}`.

`RunEvent{Index, URL, Source, DownloadMS, Accepted, Reason, Timestamp}`.

`DocumentDTO{ID, URL, Domain, Score, Length, CuratedText}`.

## 7. Implementation phases and effort

| Phase | Scope | Effort |
|---|---|---|
| G1 | Embedded server, dashboard, list profiles, read-only profile view | M |
| G2 | Full profile editor with validation and save | M |
| G3 | Run monitor with SSE and graceful stop (reuses audit item B1) | M |
| G4 | Corpus viewer with FTS5 search, copy/download | M |
| G5 | Keyword lab and feedback-to-profile loop (Go port of `corpus_feedback.py`) | M |
| G6 | Packaging polish: `--gui` flag, tray/launcher shortcut, docs and screenshots for AppStream | S |

Total: **XL** as a single roadmap item, naturally split into six M/S milestones.

## 8. Risks and mitigations

- **Scope creep**: ship G1–G3 first; the keyword lab (G5) is the differentiator but can follow.
- **Profile write corruption**: always write to a temp file and atomically rename; keep a `.bak`.
- **Long-running crawls**: SSE and graceful stop prevent the UI from becoming unresponsive.
- **Cross-platform**: because the server is pure Go and assets are embedded, all existing release
  targets work unchanged; the GUI opens the user's default browser on start.

## 9. Conclusion

The GUI is feasible without new dependencies, reuses 80 % of the existing code (config, crawler,
SQLite/FTS5, keyword extraction), and directly enables the iterative "crawl → inspect → extract →
retune → re-crawl" workflow that the current CLI cannot provide. Recommended as the top product
item of the v1.6+ backlog.
