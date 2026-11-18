# GUI Studio proposals — feasibility analysis (2026-11-18)

Source: external proposal for a "goCrawler Studio" interface. Analysed against the **actual** code.

## Important correction to the proposal

The proposal assumes the GitHub repo shows a React container and that the interface templates are
not public. That is **not the case here**: goCrawler is a Go project and the entire Workbench is a
single HTML/CSS/JS string embedded in `crawler.go` (`guiHTML`), served by `guiHandler()` with
`net/http`. There are no hidden templates or frontend build step, so every change can be made
directly in one file and tested with `gui_test.go`.

## Assessment

| Proposal | Current state | Real new value | Verdict |
|---|---|---|---|
| SSE telemetry instead of polling | `setInterval(stats,1500)` over `/api/stats`; counters already include visits, cache, network, cache I/O, rejections | Live push avoids 1.5 s lag and polling traffic; useful for long crawls | **Include** — medium |
| Start / Pause / Drain / Stop | Only Start/Stop; `stopRequested` flag exists | Pause (halt scheduling), Drain (finish queue, no new links) are genuinely new control modes | **Include** — medium |
| KPI: URLs/s, latency, worker saturation, pending queue | Has totals and `download_ms` | Rates and queue depth turn counters into diagnostics | **Include** — small/medium |
| Config sliders: workers, rate limit, User-Agent, regex rules | Values exist in YAML/flags but not in GUI | GUI control without editing YAML | **Include** — medium |
| Filterable event console (INFO/WARN/ERR) + export JSON/CSV/Sitemap | No event console; rejection drill-down exists | New observability and hand-off formats | **Include** — medium |
| Audit table by HTTP status / Content-Type | Not stored per URL | Needs structured per-URL records (status, type, latency) in SQLite | **Include** — large |
| URL drawer: headers, response time, metadata, in/out links | Only URL + curated text | Requires retaining raw response metadata | **Include** — large |
| Interactive topology graph (depth/status colours) | Not available | Highest visual value, highest cost: needs edge list with depth and status | **Include** — large |
| Dark/light, monospace, high-density theme | Dark only, monospace partly | Low-cost polish, real usability gain | **Include** — small |

## Decision

All nine items bring something new; none is already implemented. They are added to the roadmap as
P22–P30, sorted by increasing effort. The recommendation is to ship P22–P26 first (telemetry and
control), then P27–P28 (structured audit data), and leave P29–P30 (graph/drawer) for a v2 release.

A single-file interactive mockup is **not** needed: the real interface is already a single embedded
file. Changes should be made directly in `crawler.go` with `gui_test.go` coverage, including the
JavaScript delimiter-balance test that caught a previous regression.
