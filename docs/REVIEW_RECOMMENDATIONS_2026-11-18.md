# Review of external recommendation (2026-11-18)

Source: external deep analysis of goCrawler v1.8.14 shared by the author. This document records
which suggestions are worth adopting, which are already solved, and which are rejected, with my
own effort ratings. The roadmap only receives the accepted items.

## 1. Accuracy check (facts verified in the code)

| Claim in the recommendation | Verification | Verdict |
|---|---|---|
| "Per-request HTTP clients and `Close=true`" | `initHTTPClient()` already builds a shared `http.Client` with a tuned `Transport` (`MaxIdleConns=100`, `MaxIdleConnsPerHost=10`, 90 s idle timeout) and `proxyGet()` only creates requests, not clients. | **Outdated** — no action needed. |
| "18 MB `all.num`" | `corpusfreqlib/all.num` is 18 MB and tracked by Git; a smaller `freq-index.json.gz` (2.8 MB) is also tracked. | **Accurate** — P59 is valid. |
| "Example profiles missing from the tree" | `profiles/*` is gitignored; no tracked examples, although `profiles/` exists locally with research profiles. | **Accurate** — worth fixing. |
| "README says Go 1.20" | Header says Go 1.20 while Requirements correctly say Go 1.26+. | **Accurate** — small doc bug. |
| "GUI uses HTML string in crawler.go" | `guiHandler()` registers ~16 `/api/*` handlers in the same 132 KB file. | **Accurate** — modularisation is justified. |

## 2. Accepted new roadmap items (effort-ordered)

The external IDs are kept for traceability; the effort is my own assessment.

| ID | My effort | Item | Rationale |
|---|---|---|---|
| P61 | S | Onboarding pack: badges, `CONTRIBUTING.md`, issue/PR templates, tracked example profiles under `profiles/examples/`, and fix the Go 1.20 header in README | Cheap, removes the main friction for new users; keeps current gitignore for real research profiles |
| P62 | S | Notification hooks: webhook and/or desktop notification on completion, errors or rejection-threshold breach (P57) | Small, high value for unattended crawls |
| P63 | S | Accessibility polish: high-contrast and reduced-motion modes, keyboard navigation on top of P45 | Small CSS/JS only, no backend changes |
| P59 | S/M | Large-file hygiene: track `freq-index.json.gz` as the primary artifact and generate/compress `all.num` at build time or move it to Git LFS | Clones shrink by ~18 MB; must not break the offline self-test, so keep a small committed fixture |
| P52 | M | Adaptive politeness: honour 429/503 + `Retry-After`, exponential per-domain back-off, max req/s and connections per host (P52) | Protects both the target sites and the crawler; complements robots crawl-delay |
| P54 | M | Corpus export pack: Markdown bundle, JSONL, CSV and Hugging Face-style `data/` folder with metadata (P54) | Complements P46; researchers need portable output more than more GUI panels |
| P56 | M | Profile inheritance/composition: base profiles + overlays (English + PDF + strict robots) (P56) | Removes duplication in `profiles/` and makes experiments reproducible |
| P64 | M | GUI/API contract: JSON Schema or generated OpenAPI for the `/api/*` handlers | Prevents drift while the API grows; small once P27 is in place |
| P53 | M/L | Corpus quality dashboard: readability, lexical diversity, domain coverage, language-confidence histogram and topic purity (P53) | Strong research feature; some metrics are cheap, but storing/aggregating them needs data-model work |
| P65 | M/L | Fuzz/property tests for URL parsing, PDF extraction and FTS5 query sanitisation; expand multi-worker `httptest` coverage | Direct hardening of the areas changed most often |
| P55 | L | Optional JS rendering behind a build tag/runtime flag (pure-Go or external Chrome path); default stays pure HTTP (P55) | Opens modern sites but adds a large, platform-dependent surface — opt-in only |
| P58 | L | Incremental/delta crawls using ETag/Last-Modified/content hash, with new-vs-updated reporting (P58) | High long-term value; needs cache schema and state model changes |
| P66 | L | Staged modularisation: extract `gui/`, then `pipeline/`, then `download/` from `crawler.go` | Improves maintainability; split after P27/P64 stabilize the API, not before |
| P67 | L | Streaming/memory bounds for large corpora: stream paragraphs and bound in-memory frequency/dedup structures | Only relevant for very large runs; keep after functional features |

## 3. Existing roadmap items re-rated

- **P27 (SSE)** remains the highest-leverage item; I keep it at M and treat it as the prerequisite
  for P24, P25, P34 and P36. The recommendation's priority ordering is correct.
- **P28/P29 (audit records + table)** stay at L: valuable, but they require a data model first.
- **P30 (topology) and P47 (keyword impact)** stay XL/deferred, as recommended.
- **P46 (report export to Markdown/print-to-PDF)** is kept separate from P54 (corpus export):
  one is a human report, the other is machine-readable output.

## 4. Rejected or deferred

| Suggestion | Decision | Reason |
|---|---|---|
| Plugin interface / WASM filters (P60) | **Deferred** | Security and ABI surface too large before the core API is stable; see Section 3 of the roadmap |
| Wails/Fyne desktop wrapper | **Deferred** | Loopback-only web GUI already works; wrapper adds packaging burden without new capability |
| Docker image | **Deferred (optional)** | Native single binary is the stated primary distribution; image can be added later if requested |
| "Session bookmarks / favorite profiles" | **Not added** | Low value compared with P39 (profile history) and P44 (session comparison) already in the roadmap |
| "Per-request HTTP client fix" | **Rejected** | Already implemented |
| Aggressive modularisation now | **Deferred to P66** | Do it in stages after the API stabilises; a big-bang split would slow the GUI work |

## 5. Risks called out that are already mitigated

- **PDF quality variance**: v1.8.14 already emits `invalid_pdf`, `pdf_no_text` and `too_large`
  rejection reasons; a GUI PDF preview snippet would be a nice P53/P54 follow-up, not a blocker.
- **JS-heavy sites**: document the limitation in README (P61 onboarding pack) rather than rushing P55.
- **Scope control**: ship P27/P25/P34/P36 first; the new S/M items are intentionally small.
