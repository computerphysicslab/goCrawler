# PDF support analysis

**Date:** 2026-11-18
**Status:** analysis / design (not implemented yet)
**Related roadmap IDs:** P48–P51

## 1. Goal

Download PDF documents found while crawling and feed their **text content** into the existing
filters and corpus pipeline (language, stopwords, relevance, minimum length, deduplication,
FTS5 indexing). No PDF generation, signing, redaction or OCR is in scope for v1.

Today goCrawler refuses PDFs in two places:

- `crawler.yaml` includes `pdf` in `regexLinkBannedTokens`, so `.pdf` links never enter the pool.
- `doNextLink` rejects responses whose `Content-Type` is `application/pdf`.

Both gates must become opt-out and controlled by the profile instead of global.

## 2. Open-source Go candidates

| Library | License | Pure Go | Text extraction | Maintenance | Verdict |
|---|---|---|---|---|---|
| `github.com/ledongthuc/pdf` | BSD-3-Clause | yes | plain text, styled runs, rows | active fork of `rsc/pdf` | **recommended** |
| `github.com/dslipak/pdf` | BSD-3-Clause | yes | plain text, styled runs, rows | active; merges `rsc/pdf` + `ledongthuc/pdf` | fallback |
| `github.com/pdfcpu/pdfcpu` | Apache-2.0 | yes | **no text extraction** (pages, images, fonts, forms) | very active | rejected for this task |
| `github.com/gen2brain/go-fitz` (MuPDF) | AGPL-3.0 | cgo / WASM | excellent, plus rendering | active | rejected: native dependency + AGPL |
| `github.com/klippa-app/go-pdfium` | BSD | cgo / WASM | good | active | rejected: native dependency |
| `github.com/unidoc/unipdf` | commercial | yes | good | active | rejected: license |

Conclusions:

- **`ledongthuc/pdf` is the best fit.** It is pure Go, permissively licensed, exposes exactly what
  we need (`pdf.Open` + `reader.GetPlainText()` or per-page `Page(i).GetPlainText(nil)`), keeps the
  single-binary model and has no CGO / system dependencies.
- `pdfcpu` is the right tool for merging/splitting/validating PDFs, not for extracting text, so it
  is not part of this feature.
- MuPDF wrappers would improve extraction quality but break the zero-dependency pure-Go design and
  add an AGPL component; they are explicitly out of scope.
- Scanned/image-only PDFs contain no text layer. Extraction will return little or empty text and
  those documents must be treated like any other low-quality document (reject with reason
  `too_short`). OCR is a future item, not part of P48–P51.

## 3. Proposed design

### 3.1 Profile options

```yaml
process_pdf: false        # opt-in; PDFs are ignored when false
pdf_max_bytes: 26214400   # 25 MiB hard cap per document
pdf_pages_limit: 200      # safety limit for pathological documents
pdf_language_min_ratio: 0.5
```

These live in the profile struct and are parsed in `loadProfile`. Defaults keep current behaviour
(PDFs disabled), so `crawler.yaml` can be left untouched until a research profile enables them.

### 3.2 URL gate

- Remove `pdf` from the *global* `regexLinkBannedTokens`; apply the ban only when
  `!process_pdf` (and/or when the URL does not match `include`/`exclude`).
- Keep the `isOffTopicURL` checks; a `.pdf` URL is not off-topic by itself.

### 3.3 Download

Add a raw downloader used only for PDFs:

```
downloadRaw(url) -> (body []byte, contentType string, statusCode int, err error)
```

- Enforce `pdf_max_bytes` while reading (`io.LimitReader` + final size check).
- Validate `Content-Type` contains `application/pdf` or the body starts with `%PDF-`.
- Do not pass the body through `html2text` or `getLanguageFromHTML`.

### 3.4 Extraction

New small package `pdftextlib`:

```
Extract(data []byte, pageLimit int) (text string, pages int, err error)
```

Implementation sketch:

- `pdf.NewReader(bytes.NewReader(data), int64(len(data)))` (avoids touching disk).
- Iterate pages `1..NumPage()`, stop at `pageLimit`.
- Prefer `page.GetPlainText(nil)`; on failure fall back to concatenating
  `page.Content().Text`.
- Join pages with `\n\n`, trim, collapse excessive whitespace.
- Treat encrypted/invalid PDFs as errors (`recordRejection("invalid_pdf", ...)`), never as panics.
- Guard against decompression bombs and huge outputs with the existing limits.

Keep the `ledongthuc/pdf` import inside `pdftextlib` only; the rest of the code depends on a local
interface so a future swap to `dslipak/pdf` is a one-file change.

### 3.5 Pipeline reuse

Once text is extracted, run it through the exact same filters as HTML:

1. normalize/clean text;
2. language detection (reject `non_english`);
3. stopword ratio (`stopword_ratio`);
4. relevance score (`off_topic_url` / `low_relevancy`);
5. minimum length (`too_short`);
6. duplicate signature (`duplicate`) — `isUniqueDocument`;
7. insert into `corpus_pages` with a `source_type = 'pdf'` marker and `page_count` metadata.

The existing SQLite schema may need a column or a metadata JSON field; prefer a nullable
`source_type` / `page_count` to keep queries cheap.

## 4. Tests

- Unit: `pdftextlib` extracts text from a small generated PDF fixture; handles encrypted/empty/
  truncated PDFs without panic.
- Integration: a local `httptest` server serves a `.pdf` URL; profile with `process_pdf: true`
  accepts it and stores the text; profile with the flag off rejects it.
- Limits: oversized PDF is rejected with `too_large`; `pdf_pages_limit` is respected.
- Race: `go test -race ./...` remains clean.

## 5. Roadmap items

- **P48 (S)** profile flags + PDF URL/content-type gating.
- **P49 (M)** raw downloader with size cap and PDF magic-byte validation.
- **P50 (M)** `pdftextlib` wrapper with graceful failures and page limit.
- **P51 (L)** full pipeline/corpus integration, metadata, tests and GUI/CLI visibility.

## 6. Risks

- Extracted text quality varies wildly; rejected/too-short rates may rise. Mitigation: keep PDFs
  opt-in per profile and expose rejection counters.
- Some PDFs are password protected; they must fail fast, not block workers.
- Very large PDFs could stress memory; hard caps and `io.LimitReader` are mandatory.
- Adding a dependency increases the attack surface; `govulncheck` must stay clean after wiring.
