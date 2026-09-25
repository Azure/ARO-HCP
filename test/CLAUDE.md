Load AGENTS.md for context

## Finding the e2e "gather-observability" Spyglass artifacts

The metrics/alert pages shown inline on a Prow job's Spyglass page (and browsable
under gcsweb at `.../artifacts/e2e-parallel/aro-hcp-gather-observability/artifacts/`)
are produced by the `gather-observability` subcommand, **not** by a Grafana
dashboard. Source lives in `test/cmd/aro-hcp-tests/gather-observability/`.

The CI step `aro-hcp-gather-observability` runs `aro-hcp-tests gather-observability`
(`cmd.go`, `Use: "gather-observability"`) and writes every artifact into the
`--output` dir, which Prow uploads to that GCS path.

### Artifact → producer map

| Spyglass file | What it is | Produced by |
|---|---|---|
| `observability-summary.html` | **single tabbed page** with alerts, metrics **panels**, Utilization, Resource History, and **Right-Sizing** | `options.go` (`Run`) assembles `[]observabilityTab`, then `renderObservabilityPage` (`render.go`) writes one page using `artifacts/observability.html.tmpl` |
| `alerts.json` | Azure Monitor alerts that fired (raw data) | `options.go` (`Run`) + `alerts.go` |
| `junit_alerts.xml` | alerts as JUnit (fails the step) | `junit.go` (`alertsToJUnit`) |
| `replica-peaks.json` | Compact per-container usage/request and exact-UID ownership evidence | `replica_peaks.go` + `replica_peaks_queries.go`; usage queries are `cpu` (2m burst), `cpuSustained` (10m sizing), and `memory` (peak working set) |
| `right-sizing.json` / `right-sizing.html` | Per-container request suggestions and standalone version of the Right-Sizing tab | `buildRightSizingReport` (`right_sizing.go`), `renderRightSizingHTML` (`right_sizing_render.go`), and `artifacts/right-sizing.html.tmpl`, written by `options.go` |

Each tab's HTML is a full, self-contained page (the existing alerts and metrics
panel templates, unchanged) embedded into its own same-origin `<iframe srcdoc>`
so per-section CSS/JS stay isolated. Iframes are created lazily on first
activation (while visible) so charts size correctly; the wrapper auto-sizes each
iframe to its content height.

The `-summary.html` suffix is **required**: Prow's Spyglass HTML lens only renders
files matching `.*-summary.*\.html` inline. Emitting **one** such file (rather
than one per panel) means Spyglass shows a single inline iframe with tabs instead
of a separate collapsible section per panel.

### Right-sizing maintenance

`gather-observability render-right-sizing --input replica-peaks.json --output DIR
--change-threshold .1` rebuilds `right-sizing.html` and `right-sizing.json` without
Azure credentials. The standalone HTML deliberately has no `-summary` suffix.
The updater in `tooling/rightsize-requests` consumes **right-sizing.json**, not
replica peaks. Keep the operational commands and policy in
[CI Operations](../docs/ci/operations.md#right-sizing-requests) rather than
duplicating them here.

Preserve the max-across-replicas 10m CPU / peak working-set policy, 20% headroom,
ceil 10m/10Mi rounding (`max(1, ceil(peak * 1.2 / unit)) * unit`), and inclusive
configurable deadband. Right-sizing reports are version 2; reject old version 1
sizing reports and regenerate from the original version 1 replica peaks instead
of relabeling old suggestions. Legacy 2m fallback must be labeled; missing new 10m evidence
must never trigger fallback. Eligibility checks all observed replicas' exact-UID
ownership and usage/request coverage (10 points, 90% of each own observed span),
not actual full lifetimes or absence of ingestion loss. Amounts are per container,
not concurrent totals. Peak risk is not actual alert state or a clearance guarantee:
drift alerts average usage/request over 30m, use CPU rate5m, and hold >1.2 for 5m.
Offline updates remain explicitly mapped, dev-only, stale/limit guarded, and
credential-, render-, and commit-free; unknown/ineligible evidence cannot authorize edits.

### Adding or changing a chart

Edit `test/cmd/aro-hcp-tests/gather-observability/queries.yaml`. Each entry under
`panels[].queries[]` has: `title`, `description`, `query` (PromQL run via
`query_range` against the Azure Monitor Prometheus workspace), `unit` (free-form
label), `workspace` (`svc` or `hcp`), `step` (default `60s`), and optional
`chartType` (`line` default, or `faceted-stacked-area` with `facetBy`/`stackBy`/`colors`)
and `minPeakThreshold`. The schema is `QuerySpec` in `promql.go`;
`TestLoadQueriesConfigEmbedded` (`chart_test.go`) validates the embedded file parses.

**HA-replica dedup (counters/gauges):** the managed Prometheus may scrape each
target from multiple HA replicas (label `prometheus_replica`). Collapse that
dimension with `max without (prometheus_replica) ( ... )` *inside* your
aggregation before `sum`, or totals are counted once per replica and inflated —
e.g. `sum by (cluster) ( max without (prometheus_replica) ( increase(foo_total[5m]) ) )`.
The header comment in `queries.yaml` and the workqueue queries show the pattern.

### The metrics the charts query

Service metric names come from the services themselves, e.g. the frontend HTTP
metrics are defined in `frontend/pkg/frontend/metrics.go` (names in
`frontend/pkg/frontend/const.go`): `frontend_http_requests_total` (counter) and
`frontend_http_requests_duration_seconds` (histogram). The Azure Monitor managed
Prometheus adds a `cluster` external label, which the queries group by.
