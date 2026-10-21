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
| `observability-summary.html` | **single tabbed page** with one tab for the Azure Monitor alerts view and one tab per metrics **panel** (charts) | `options.go` (`Run`) assembles `[]observabilityTab` from `renderAlertsHTML` + `runQueries`→`renderPanelHTML`, then `renderObservabilityPage` (`render.go`) writes one page using `artifacts/observability.html.tmpl` |
| `alerts.json` | Azure Monitor alerts that fired (raw data) | `options.go` (`Run`) + `alerts.go` |
| `junit_alerts.xml` | alerts as JUnit (fails the step) | `junit.go` (`alertsToJUnit`) |

Each tab's HTML is a full, self-contained page (the existing alerts and metrics
panel templates, unchanged) embedded into its own same-origin `<iframe srcdoc>`
so per-section CSS/JS stay isolated. Iframes are created lazily on first
activation (while visible) so charts size correctly; the wrapper auto-sizes each
iframe to its content height.

The `-summary.html` suffix is **required**: Prow's Spyglass HTML lens only renders
files matching `.*-summary.*\.html` inline. Emitting **one** such file (rather
than one per panel) means Spyglass shows a single inline iframe with tabs instead
of a separate collapsible section per panel.

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
