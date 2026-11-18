# Second GUI mockup proposal — analysis (2026-11-18)

This proposal extends the earlier "GUI Studio" analysis (`docs/GUI_STUDIO_ANALYSIS.md`, roadmap
P22–P30). Only genuinely new and feasible ideas are added here; overlaps are skipped.

## Decisions already taken (do not re-add)

- **React/Vue external frontend: rejected.** It would break the zero-dependency single-binary design
  and require a Node build step. The interface stays as embedded HTML/CSS/JS in `crawler.go`.
- Dark/light toggle, live KPIs, SSE, Pause/Drain, event export, per-URL audit records, status table,
  topology graph and URL drawer are already P22–P30.

## New ideas worth adding

| Idea | Feasibility with current stack | Effort | Roadmap |
|---|---|---|---|
| Design tokens (CSS variables), 4/8 px spacing scale, consistent radius/elevation, micro-animations in CSS | Pure CSS in `guiHTML`, no JS dependency | S | P31 |
| Fixed sidebar navigation with active profile selector and crawler status indicator | Replace the `<nav>` with a sidebar; reuse `/api/profiles`, `/api/stats` | M | P32 |
| Responsive layout for narrow screens | CSS media queries only | S | P33 |
| Live activity feed: last 20 processed URLs with status colours | Needs a small in-memory ring buffer exposed via `/api/activity` | M | P34 |
| Rejection bar/donut chart (no chart library: CSS bars or inline SVG) | Reuses `/api/stats` rejections | S/M | P35 |
| Progress ring and ETA estimate for the current crawl | Needs start time + processed/total estimate in stats | M | P36 |
| Structured profile editor: chips for keywords/domains/seeds, live regex validation, YAML advanced view | Forms + existing `/api/profile` endpoints | L | P37 |
| Visual diff of profile changes before saving | Small JS diff renderer | M | P38 |
| Profile version history (snapshot per run, with diff) | Store snapshots in SQLite alongside `state:crawl` | L | P39 |
| Keyword Lab 2.0: sortable/filterable table, score-vs-frequency scatter, multi-select and diff preview | Pure JS + existing `/api/keywords` | M | P40 |
| "Apply & Re-run" with confirmation in Keyword Lab | Chain `/api/keywords/apply` + `/api/crawl` | S | P41 |
| Corpus Explorer: virtualised list, keyword highlighting, filters by score/domain/length, export MD/JSON/CSV, open full text | Reuses `/api/documents`, `/api/search` | L | P42 |
| Domain heatmap (accepted pages per domain) | Aggregate from curated corpus | M | P43 |
| Session comparison (two runs of the same profile) | Compare saved snapshots from P39 | L | P44 |
| Command palette (Cmd/Ctrl+K) for quick actions | Pure JS overlay | M | P45 |
| Report export to Markdown/PDF | Markdown is easy; PDF needs either print-to-PDF or a new dependency — use browser print CSS | M | P46 |
| Keyword impact analysis ("if I add these keywords, how many more docs would pass?") | Simulate scoring against cached pages; real value but complex | XL | P47 |

## Recommended order

P31–P36 (visual polish + lightweight telemetry) → P40–P41 (Keyword Lab upgrades) → P32–P33 layout →
P37–P39 (structured editor and history) → P42–P46 → P47 last.

All items are appended to `roadmap.md` section 1, which remains sorted by increasing effort.
