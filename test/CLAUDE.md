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
| `panel-<slug>-summary.html` | one page per metrics **panel** (charts) | `queries.yaml` panels, rendered by `runQueries` → `renderPanel` (`options.go`, `render.go`, `chart.go`); filename is `panel-%s-summary.html` with `sanitizeTitle(panel.Title)` (`render.go`) |
| `alerts-summary.html` + `alerts.json` | Azure Monitor alerts that fired | `options.go` (`Run`) + `alerts.go` + `render.go` template |
| `junit_alerts.xml` | alerts as JUnit (fails the step) | `junit.go` (`alertsToJUnit`) |

The `-summary.html` suffix is **required**: Prow's Spyglass HTML lens only renders
files matching `.*-summary.*\.html` inline (see the comment in `options.go` near
`fmt.Sprintf("panel-%s-summary.html", ...)`).

Panel-title → filename examples (slug = lowercase, non-alphanumerics → `-`):
- "Frontend Metrics" → `panel-frontend-metrics-summary.html`
- "Backend Metrics" → `panel-backend-metrics-summary.html`
- "Fleet Controller Metrics" → `panel-fleet-controller-metrics-summary.html`
- "Maestro Metrics" → `panel-maestro-metrics-summary.html`

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
