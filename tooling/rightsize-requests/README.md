# rightsize-requests

Query per-cluster production usage from Azure Managed Grafana and right-size the
CPU/memory **requests** recorded in `config/config.yaml` — in place, preserving
comments and formatting.

This tool is the companion to the `ServiceCPUDrift` / `ServiceMemoryDrift`
alerts (see `docs/alerts/service-memory-resources.md`). Those alerts fire when a
service's 30-minute-average usage sustains above 1.2× its request; this tool
computes a right-sized request from observed peak usage so you can "go back and
fix it up."

## How it works

1. Authenticates to Azure Managed Grafana using your ambient Azure credentials
   (`az login` locally, or a managed identity in automation), scoped to the
   Managed Grafana service application.
2. Lists Grafana datasources. **Each production cluster is a separate Prometheus
   (Azure Monitor Workspace) datasource.** All `prometheus`-type datasources are
   queried (optionally filtered with `--datasource-pattern`).
3. For each datasource (queried concurrently), runs two PromQL instant queries
   over a lookback window (default 14d) with **per-pod** granularity:
   - **memory:** `container_memory_working_set_bytes`
   - **cpu:** `rate(container_cpu_usage_seconds_total[5m])`
4. Two-stage aggregation:
   - **Per pod, over time:** the `--percentile` quantile (default p95), so a
     pod's transient spikes don't set its request.
   - **Across the fleet of pods/clusters:** the `--fleet-percentile` quantile
     (default p95), so a single anomalous pod or cluster can't drive the
     fleet-wide request (use `--fleet-percentile 0` for raw max).
   Then multiply by a safety margin (default **1.25×**) and round to clean
   Kubernetes quantities (CPU up to nearest 10m, memory up to nearest 16Mi,
   collapsing to Gi).

> Note: Azure Managed Prometheus matches `=~` label selectors **unanchored**, so
> the tool anchors the namespace regex (`^(...)$`) explicitly to avoid pulling in
> hosted-control-plane namespaces (e.g. `ocm-...-aro-hcp-lab-*`).
5. Maps each `(namespace, container)` to its request fields in
   `config/config.yaml` and, by default, edits the file in place. Requests are
   only **increased** unless `--allow-decrease` is passed.

Any observed workload that has no mapping is reported so the table in
`internal/rightsize/mapping.go` can be extended — the tool never guesses.

## Usage

```bash
cd tooling/rightsize-requests

# Preview proposed changes (no writes):
make dry-run GRAFANA_URL=https://arohcp-prod-xxxx.suk.grafana.azure.com/

# Query Grafana and edit config/config.yaml in place:
make run GRAFANA_URL=https://arohcp-prod-xxxx.suk.grafana.azure.com/

# Or invoke the binary directly:
go build -o rightsize-requests .
./rightsize-requests \
  --grafana-url https://arohcp-prod-xxxx.suk.grafana.azure.com/ \
  --config ../../config/config.yaml \
  --window 14d --margin 1.25 \
  --datasource-pattern '^prod-' \
  --dry-run
```

### Flags

| Flag | Default | Description |
| --- | --- | --- |
| `--grafana-url` | *(required)* | Azure Managed Grafana base URL |
| `--config` | `../../config/config.yaml` | config file to edit |
| `--window` | `14d` | PromQL lookback window for peak usage |
| `--step` | `5m` | subquery resolution |
| `--margin` | `1.25` | safety multiplier applied to observed usage |
| `--percentile` | `0.95` | per-pod OVER TIME statistic (0 or ≥1 ⇒ raw max/peak) |
| `--fleet-percentile` | `0.95` | ACROSS pods/clusters statistic (0 or ≥1 ⇒ max) |
| `--datasource-pattern` | `^services-` | regexp on datasource uid; excludes `hcps-*` |
| `--source-prefix` | `defaults` | dotted key prefix in the source config |
| `--write-config` | *(= --config)* | file to WRITE to (e.g. the msft overlay in another repo) |
| `--write-prefix` | *(= --source-prefix)* | dotted prefix in the write config (e.g. `clouds.public.defaults`) |
| `--limit-multiple` | `2.0` | when a container sets a numeric memory limit, set it to this × the new request |
| `--render-cmd` | *(none)* | command to regenerate rendered configs (run in the write repo root) before committing |
| `--commit` | `false` | git-commit the edited file (summary + Grafana Explore links) after writing |
| `--dry-run` | `false` | report changes without editing |
| `--allow-decrease` | `false` | also shrink requests more than 2× oversized |

## Writing to the msft overlay

The source (read) and target (write) configs can differ, so the tool can read
effective current values from base `config/config.yaml` and write **sparse
overrides** into the msft sensitive overlay (a different repo), creating any
missing `resources` blocks:

```bash
rightsize-requests \
  --grafana-url https://arohcp-prod-xxxx.suk.grafana.azure.com/ \
  --config ../../config/config.yaml \
  --write-config /path/to/config.msft.sensitive.clouds-overlay.yaml \
  --write-prefix clouds.public.defaults \
  --window 14d --limit-multiple 2 \
  --render-cmd 'make -C hcp/ render-service-configuration-examples' \
  --commit
```

With `--commit`, the tool stages and commits the edited file with a summary
commit message that includes, per service, a **Grafana Explore deep link** to
the per-pod CPU/memory series behind the number (pinned to the cluster whose
usage is closest to the chosen percentile — not the outlier the percentile
excludes). It does not push or open a PR.

`--render-cmd` runs a command (from the write file's git repo root) to
regenerate rendered configuration before committing; any files it changes are
folded into the same commit. If a run reaches too few live datasources (some
regional Azure Monitor Workspaces are behind stale/duplicate datasources —
consider `grafanactl clean`), re-run when more are reachable.

## After running

The tool edits the source `config/config.yaml` only. Regenerate the rendered
configs afterward from the repo root:

```bash
make -C config materialize
```

Then review with `git diff` and open a PR.

## Extending the service mapping

`internal/rightsize/mapping.go` is the authoritative `(namespace, container) →
config path` table. Container names must match the `container` label emitted by
cAdvisor / kube-state-metrics (i.e. the pod spec container name). The table was
verified against `int-westus3-svc-1` and `int-westus3-mgmt-2` on 2026-08-28; the
only unverified row is `secretSyncController`, which is not deployed in int.
Run with `--dry-run` first and check the "unmapped workloads" section of the
report to catch any drift (e.g. a renamed container) before writing.

## Development

```bash
go build ./...
go test ./...
```
