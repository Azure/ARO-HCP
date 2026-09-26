# rightsize-requests

Query per-cluster production usage from Azure Managed Grafana and right-size the
CPU/memory **requests** recorded in `config/config.yaml` — in place, preserving
comments and formatting.

This tool is the companion to the `ServiceCPUDrift` / `ServiceMemoryDrift`
alerts (see `docs/alerts/service-memory-resources.md`). Those alerts fire when a
service's 30-minute-average usage/request ratio is above 1.2 for 5 minutes; this tool
computes a right-sized request from observed peak usage so you can "go back and
fix it up."

## Offline Input

Consume an existing `right-sizing.json` report instead of querying Grafana. This
mode does not construct Azure credentials or access the network:

```bash
go run . --input right-sizing.json --config ../../config/config.yaml --dry-run
go run . --input right-sizing.json --config ../../config/config.yaml
# Permit validated decreases and disable the default 10% deadband:
go run . --input right-sizing.json --config ../../config/config.yaml --allow-decrease --change-threshold 0
```

Offline writes are restricted to **`clouds.dev.defaults.*.resources.requests.cpu`
and `.memory`** using the existing namespace/container mapping. Global `defaults`,
public clouds, all limits, and unrelated configuration are left unchanged. Missing
dev paths are inserted. `--write-config` may select a separate, existing sparse
overlay; its dev override takes precedence over the source file's dev override,
then the source file's global defaults. Missing or nonnumeric current requests
are skipped.

Before queuing a request update, the effective limit for that resource is read
from the target dev override, source dev override, then source global defaults.
A positive numeric limit blocks a larger proposed request with an explicit
warning; equality is allowed. Absent limits, blank values, numeric zero, `NONE`
and `unlimited` are treated as uncapped. Malformed nonempty limits block the
update, without falling back past a malformed override. Limits are never changed.

Every row mapping to the same config resource contributes, across all clusters
and workloads. The **maximum suggested value** wins, not the last row. If any
mapped row is ineligible, an init container, has an unknown owner, or lacks peak,
suggestion, request bounds or complete replica measurements, that entire mapped
resource is blocked. CPU and memory are evaluated independently. Unknown mappings
are skipped with an aggregate row count; their details and warnings are logged
only at verbosity 1. Report warnings, mapped row warnings and blocking reasons
remain visible at normal verbosity.

To protect against stale reports, the effective current request must either equal
the maximum suggestion (a no-op), or lie within at least one contributing row's
inclusive observed `[requestMin, requestMax]` range. A value in a gap between
disjoint ranges does not qualify. Otherwise the tool skips it with an explicit
`WARNING stale`, even with `--allow-decrease`. Reapplying a report is idempotent.
Decreases require `--allow-decrease`; the Grafana mode's factor-of-two rule does
not apply to offline input.

`--change-threshold` defaults to `0.1` (10%) and accepts finite fractions from 0
through 1. It always controls input-mode decisions, independently of the report's
informational `changeThreshold`. After taking the maximum suggestion across all
mapped rows, the tool compares it to the **effective current** request. Changes
whose absolute size divided by current is **at or below** the threshold are
skipped (with floating-point tolerance at the boundary). Zero disables this
deadband, including when calling `RunInput` with zero-valued `Options`. A positive
suggestion against a zero current request is not suppressed by the deadband.

If the maximum measured peak across mapped rows is **greater than 1.2 times
current**, the deadband is bypassed. This does not bypass eligibility, stale
protection or decrease permission. The producer's `actionable` and `alertRisk`
flags are informational; both decisions are recomputed against current config,
not trusted from individual rows or their observed request bounds.

The drift alerts use a **30-minute-average usage/request ratio >1.2 for 5
minutes**, with CPU usage based on a **5-minute rate**. Offline CPU sizing normally
uses the report's **10-minute rate** (or its declared legacy 2-minute window).
The peak-based deadband bypass indicates risk, not a guarantee that an actual
alert will fire or be prevented; their measurement windows differ from the alerts.

Exactly one of `--input` and `--grafana-url` is required. In input mode, explicitly
setting any of `--window`, `--step`, `--margin`, `--percentile`,
`--fleet-percentile`, `--datasource-pattern`, `--limit-multiple`, `--commit`, or
`--render-cmd` is an error, even when set to its default or false.
`--source-prefix` and `--write-prefix` may only be explicitly set to `defaults`
and `clouds.dev.defaults`, respectively. Offline mode never renders or commits.
Explicit `--change-threshold` is supported only with `--input`, not Grafana.

### HCP Sizing Template

Use `--sizing-template PATH` with `--input` to target HCP requests instead of normal
config overrides. An explicit environment namespace prefix is required:

```bash
go run . --input right-sizing.json \
  --sizing-template ../../hypershiftoperator/deploy/templates/cluster.clustersizingconfiguration.yaml \
  --namespace-prefix ocm-arohcpci01- \
  --dry-run
```

**Do not apply actual values yet: the available CI data is incomplete.** Review
dry-run output only until a complete, representative minimal-cluster sample is
available and live pod limits have been verified.

The prefix must match `^ocm-[a-z0-9][a-z0-9-]*-$`; matching uses a literal prefix,
not regular-expression semantics, and requires a nonempty namespace suffix.
Select the environment explicitly. The prefix is a **user assertion only**:
the report does not prove size class. Use only an `e2e_minimal` cluster sample.
Management-cluster names are not inferred or filtered; all matching HCP namespaces
across all reported clusters participate.

Only existing CPU/memory scalars under `e2e_minimal` in the
`limitClusterSizes=true` branch are edited at their original source positions.
No entries or missing resource scalars are added. Other size classes, the entire
`else` branch, comments and formatting are preserved. No normal config files or
limits are edited. Explicit `--config`, `--write-config` and `--write-prefix`
are incompatible with this mode; `--source-prefix` may only be `defaults`.

Mapping requires exact workload/container names from the selected template and
explicit controller kinds: Deployment for the six supported deployment entries,
and StatefulSet for etcd. No aliases or size classes are inferred. The maximum
suggestion across matching HCPs is used. Any ineligible or incomplete mapped row
blocks that entry/resource across all namespaces and clusters. A wrong kind for
an exact workload/container also blocks it. Unresolved owners, including Pod
rows, conservatively block every supported entry sharing that container name.
Resolved different workloads and unknown containers are skipped, not guessed.
CPU and memory retain independent stale checks, deadbands and decrease permission
against the selected template's effective current scalar.

**Live pod limits are not present in the report or sizing request source and are
not checked.** Verify them before applying requests. Dry-run output warns about
this limitation and the unproven size class; normal config-mode limit checks
described above do not apply to the sizing-template mode.

### Report Contract

The entire report is validated before any writes. All fields below are required,
with exact key spelling; unknown/duplicate fields, null nonnullable fields and
trailing JSON are rejected. `version` must be `2`, `headroom` must be `1.2`, and
`cpuWindow` must be `10m` or `2m`. `start` and `end` are nonzero RFC3339 timestamps
with `start <= end`. `changeThreshold` is a required finite number in `[0,1]`
(the producer defaults to `0.1`); `actionable` and `alertRisk` are required booleans
on every recommendation. Empty arrays are allowed.

Version 1 right-sizing reports used incompatible nearest rounding and are
rejected, even when an individual suggestion happens to agree. Rerender the
original replica peaks via `render-right-sizing`; do not just change the report's
version field. The raw `replica-peaks` artifact remains version 1, independently
of the version 2 right-sizing report consumed here.

```json
{
  "version": 2,
  "start": "2026-09-01T00:00:00Z",
  "end": "2026-09-02T00:00:00Z",
  "headroom": 1.2,
  "changeThreshold": 0.1,
  "cpuWindow": "10m",
  "warnings": [],
  "recommendations": [{
    "cluster": "svc-1",
    "namespace": "aro-hcp",
    "kind": "Deployment",
    "workload": "aro-hcp-backend",
    "container": "aro-hcp-backend",
    "resource": "cpu",
    "initContainer": false,
    "replicas": 2,
    "measuredReplicas": 2,
    "peak": 0.2,
    "burstPeak": 0.3,
    "requestMin": 0.1,
    "requestMax": 0.1,
    "suggested": 0.24,
    "suggestedQuantity": "240m",
    "delta": -0.14,
    "direction": "under",
    "eligible": true,
    "actionable": true,
    "alertRisk": true,
    "warnings": []
  }]
}
```

`resource` is `cpu` or `memory`. Measurements, bounds, suggestions and deltas are
nullable numbers in raw cores or bytes. Non-null measurements and suggestions
must be finite and nonnegative; `delta` is finite but may be signed.
Counts are nonnegative integers, with `measuredReplicas <= replicas`.
`requestMin` cannot exceed `requestMax`. Null measurements are unknown, not zero.
A null suggestion requires an empty `suggestedQuantity`.

The numeric `suggested` and parsed `suggestedQuantity` must agree with this formula,
where `unit` is 10m CPU (0.01 cores) or 10Mi memory:

```text
suggested = max(1, ceil(1.2 * peak / unit)) * unit
```

Rounding uses ceiling chunks with a minimum of 10m/10Mi, without a separate
safety floor or nearest-rounding tolerance. Exact chunks remain unchanged and
values just above a chunk round up: a 100m CPU peak yields 120m, while a 100.1m
peak yields 130m. A 12m CPU peak yields 20m; a 100.1Mi memory peak yields 130Mi.
Input values are written directly:
headroom and rounding are not applied again, nor is Grafana's 16Mi memory rounding
used. `burstPeak`, `delta`, `direction`, `actionable` and `alertRisk` are
informational; decisions compare the validated suggestion and peak against current
config.
`suggestedQuantity` must be the canonical formatted suggestion: a positive whole
decimal integer multiple of 10 followed by exactly `m` for CPU or `Mi` for memory.
Leading zeroes, signs, fractions, exponents, hexadecimal floats, whitespace and
alternative units are rejected, even if numerically equivalent.
Known owner kinds are Deployment, StatefulSet, DaemonSet, ReplicaSet, Job and
CronJob, with a nonempty, non-unknown workload name. Other owners block the mapped
resource even if the row claims to be eligible.

## Grafana Workflow

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
| `--grafana-url` | *(one source required)* | Azure Managed Grafana base URL; alternative to `--input` |
| `--input` | *(one source required)* | Version 2 offline report; dev request overrides or HCP sizing template, no credentials |
| `--sizing-template` | *(none)* | Input only: edit existing limited-branch `e2e_minimal` requests in this Helm template |
| `--namespace-prefix` | *(none)* | Required with `--sizing-template`: explicit literal HCP environment prefix, e.g. `ocm-arohcpci01-` |
| `--change-threshold` | `0.1` | Input only: fractional deadband against effective current requests, inclusive; 0 disables; overrides report threshold |
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
| `--allow-decrease` | `false` | Grafana: shrink requests more than 2× oversized; input: allow any validated decrease |

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

The tool edits only the selected config/overlay or sizing template, not generated
fixtures. After an approved write, regenerate the rendered configs from the repo
root:

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
