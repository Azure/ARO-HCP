# rightsize-requests

Query per-cluster production usage from Azure Managed Grafana and right-size the
CPU/memory **requests** recorded in `config/config.yaml` - in place, preserving
comments and formatting.

See [Configurable Resource Requests](../../docs/configurable-resource-requests.md)
for the implemented categories, exact config paths, default-preservation policy,
51 additional HCP capabilities, and upstream controls that remain blocked. A
configurable resource or editable capability is not automatically safe to size.

This tool is the companion to the `ServiceCPUDrift` / `ServiceMemoryDrift`
alerts (see `docs/alerts/service-memory-resources.md`). Those alerts fire when a
service's 30-minute-average usage/request ratio is above 1.2 for 5 minutes; this tool
computes a right-sized request from observed peak usage so you can "go back and
fix it up."

## Offline Input

Consume one or more existing `right-sizing.json` reports instead of querying
Grafana. This mode does not construct Azure credentials or access the network:

```bash
go run . --input right-sizing.json --config ../../config/config.yaml --dry-run
go run . --input right-sizing.json --config ../../config/config.yaml
# Combine explicitly selected reports into one sizing dataset:
go run . --input run-a/right-sizing.json --input run-b/right-sizing.json --config ../../config/config.yaml --dry-run
# Permit validated decreases and disable the default 10% deadband:
go run . --input right-sizing.json --config ../../config/config.yaml --allow-decrease --change-threshold 0
```

Repeat `--input` once per file; a single flag still works. Each value is a literal
filename, so commas in filenames are preserved, not treated as separators. Empty
paths are rejected during flag validation before credentials or file access.
There is no automatic directory discovery or Kusto collection: select report files
explicitly. The loader prints selected filenames and checksums for source
attribution.

All files are validated as one dataset **before any writes**. Reports with
identical canonical JSON are deduplicated (ignoring whitespace and object-key
order, but not array order), so selecting a report twice does not double count
its evidence. Reports with mixed CPU windows (`2m` and `10m`) are rejected.
Different report time ranges are allowed; their evidence is combined, not
averaged. Suggestions round peaks up without extra headroom; rounding
is not applied again.

The report UI defaults to **Editable workloads only** and **Changes only**, using
the shared service, seven-HCP and additional-HCP capability catalogs in
`pkg/targets`. Changes compare requests with suggestions, not raw peaks; matched
requests are no-ops. Uncheck the filters
to see managed, unmapped or ineligible rows. JSON retains all evidence, and a
supported mapping alone does not authorize an edit.

`AdditionalMinimalTargets()` lists 51 capabilities and mirrors the audited
regular-container YAML in
`hypershiftoperator/deploy/regular-resource-targets.yaml`, excluding the original
seven minimal targets; a repository parity test prevents drift. It is a supported
capability list, not configured baseline values. Exact additional HCP identities
require a known management-cluster role, an `ocm-arohcp` namespace with a nonempty
suffix, the audited kind, and a regular (not init) container. For example,
`Deployment/control-plane-operator/control-plane-operator` and
`StatefulSet/etcd/etcd-metrics` are supported but still require configuration.
Guest kube-state-metrics retains its separate `mgmtAgent.guestKSMResources` mapping.

The HTML renderer does not load configuration. It marks these capabilities
`editable=true` but sets display-only `eligible=false` and `actionable=false`, with:
"Requires configured baseline in hypershift.additionalMinimalResourceRequests; use
--additional-hcp-config. Not automatically editable until configured."
Uncheck **Observed changes only** to inspect them. Raw JSON measurements and
eligibility are unchanged. Shared-target blockers include additional families
across all source clusters and HCP namespaces, before display filtering. Additional
targets contribute no projected savings because the report cannot prove a baseline.
The public `RequiresConfiguredBaseline` predicate identifies this requirement;
it does not check whether configuration exists. Use `--additional-hcp-config PATH`
with `--input` and an explicit `--namespace-prefix` to size existing configured
entries; the strict CLI never inserts baseline entries from report suggestions. Size class
and live pod limits still require independent verification.

Normal config-mode offline writes are restricted to
**`clouds.dev.defaults.<ResourcePath>.requests.cpu` and `.memory`** using the shared
identity catalog. `ResourcePath` is exact: it may end in `.resources`,
`controllerResources`, `initResources`, `guestKSMResources`, or another explicitly
cataloged block. Global `defaults`, public clouds, all limits, and unrelated
configuration are left unchanged. Missing
dev paths are inserted. `--write-config` may select a separate, existing sparse
overlay; its dev override takes precedence over the source file's dev override,
then the source file's global defaults. Missing or nonnumeric current requests
are skipped. `NONE` and `unlimited` request baselines are nonnumeric, not zero:
an owner must manually seed reviewed requests before automation can size these
capabilities. Missing dev paths can be inserted only when an effective numeric
baseline already exists; this is not permission to silently introduce resources.

Expanded mappings require exact kind/workload/container identity, init status,
and, where specified, a known cluster role. Role detection accepts only terminal
`svc`, `svc-<digits>`, `mgmt-<digits>`, `opstool`, or `opstool-<digits>` segments.
Unknown roles never select role-specific paths. Namespace prefix matching is
limited to the cataloged `ocm-arohcp*` and `klusterlet-*` families with nonempty
suffixes; Prometheus accepts the exact base StatefulSet name or numeric shard
suffixes. The original namespace/container mappings retain their resolved-owner
requirement without adding workload/role constraints. There is no fuzzy matching.
Init containers require an explicit init mapping and eligible measurements;
merely containing `init` in a container name does not identify an init container.

Before queuing a request update, the effective limit for that resource is read
from the target dev override, source dev override, then source global defaults.
A positive numeric limit blocks a larger proposed request with an explicit
warning; equality is allowed. Absent limits, blank values, numeric zero, `NONE`
and `unlimited` are treated as uncapped. Malformed nonempty limits block the
update, without falling back past a malformed override. Limits are never changed.

Every row mapping to the same config resource contributes, across all reports,
clusters and workloads. The **maximum suggested value** wins, not the last row
or an average. For example, CPU peaks of 0.1 and 0.4 cores in two reports select
480m, never the quiet report's 120m, regardless of input order. If any
mapped row is ineligible, has unresolved identity or an unsupported init role,
or lacks peak, suggestion, request bounds or complete replica measurements, that entire mapped
resource is blocked. CPU and memory are evaluated independently. Unknown mappings
are skipped with an aggregate row count; their details and warnings are logged
only at verbosity 1. Report warnings, mapped row warnings and blocking reasons
remain visible at normal verbosity.

If a target/resource is missing from **any** distinct report, reductions for that
target/resource are blocked even with `--allow-decrease`, with an explicit
report-missing reason. Increases remain possible subject to the other checks.
Missing evidence is not zero usage; an existing ineligible row still blocks the
entire target/resource, including increases.

This checks completeness within the supplied dataset, not whether every weekly
job was discovered or every replica was observed. Report-level warnings are
printed but are not independently interpreted as target blockers; the producer's
row eligibility and the missing-report checks govern that decision. Weekly
automation must retain a manifest of missing/failed artifacts and assess global
collection warnings before authorizing reductions. Alert-derived floors, saved
manual floors, and Kusto integration are not implemented by this input mode.

To protect against stale reports, the effective current request must either equal
the maximum suggestion (a no-op), or lie within at least one contributing row's
inclusive observed `[requestMin, requestMax]` range across the dataset, including
the upper bound. A value in a gap between
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

Exactly one source mode is required: one or more `--input` files, or
`--grafana-url`, never both (including explicitly empty flags). In input mode, explicitly
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
go run . --input run-a/right-sizing.json --input run-b/right-sizing.json \
  --sizing-template ../../hypershiftoperator/deploy/templates/cluster.clustersizingconfiguration.yaml \
  --namespace-prefix ocm-arohcpci01- \
  --dry-run
```

The available CI data is incomplete. The default strict mode blocks reductions
with incomplete evidence. The explicitly authorized CI-only experiment below is
an opt-in exception, not a claim that the sample is complete or safe.

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
limits are edited. Explicit `--config`, `--write-config`, `--write-prefix` and
`--source-prefix` are incompatible with this mode, even at their defaults.

Mapping requires exact workload/container names from the selected template and
explicit controller kinds: Deployment for the six supported deployment entries,
and StatefulSet for etcd. No aliases or size classes are inferred. The maximum
suggestion across matching HCPs in all reports is used. The same dataset
validation, deduplication and report-missing reduction protection apply.
Any ineligible or incomplete mapped row
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

### Additional HCP Config

Use `--additional-hcp-config PATH` instead of `--sizing-template` for additional
audited regular-container targets (49 usable of 51 cataloged). From the repository
root:

```bash
go run ./tooling/rightsize-requests \
  --input run-a/right-sizing.json --input run-b/right-sizing.json \
  --additional-hcp-config config/config.yaml \
  --namespace-prefix ocm-arohcpci01- \
  --dry-run
```

`hypershift.additionalMinimalResourceRequests` defaults to `{}`. Each stable ID
contains required `deploymentName` and `containerName`, and optional positive
Kubernetes quantity strings `cpu` and `memory` (not `resources.requests`). The
schema and editor reject zero/nonpositive quantities and `NONE`/`unlimited`;
omit an optional resource instead. IDs accepted by the editor match
`^[a-zA-Z][a-zA-Z0-9_-]*$`. Duplicate identities and the original seven targets
are rejected. The allowlist must exist at
`../hypershiftoperator/deploy/regular-resource-targets.yaml` relative to the
config directory.

The full `resource-request-override.hypershift.openshift.io/<deployment>.<container>`
annotation key must be a Kubernetes qualified name; its `deployment.container`
suffix must not exceed 63 characters. The catalog retains
`openshift-route-controller-manager/openshift-route-controller-manager` for audit
parity, but its 69-character suffix is unsupported by the current override API.
The `csi-snapshot-controller-operator/csi-snapshot-controller-operator` target is
also unsupported because its suffix is 65 characters.
Configured invalid targets fail validation, including in dry runs; the chart also
rejects them. This applies even when neither CPU nor memory is supplied.

This mode sizes only existing effective entries from `defaults` and
`clouds.dev.defaults` in that file. It writes only dev CPU/memory overrides,
copying existing identities into dev if needed. It never inserts a new effective
target or an unconfigured resource from suggestions. Empty maps stay empty:
**manual, reviewed baseline seeding is required**. See the
[syntax-only CPO example](../../docs/configurable-resource-requests.md#additional-hcp-workflow);
its `200m`/`256Mi` values are placeholders, not validated sizing recommendations.

The same literal namespace-prefix validation, user-asserted minimal size class,
maximum-across-reports aggregation, stale checks, deadband, and reduction
permission apply. The CLI selects matching namespaces across all source clusters,
not a management-cluster-name filter; the report capability predicate separately
requires a known management role. Init, ineligible, incomplete, and unknown/wrong
owner evidence blocks the affected configured resource. Missing evidence in a
report blocks reductions. No live pod limits are checked or changed.

### Experimental CI HCP CPU Reductions

`--experimental-hcp-cpu-reductions` deliberately waives eligibility, coverage,
unmeasured-replica and missing-report blockers for **CPU reductions only**. It
requires `--allow-decrease`, offline `--input`, exactly one existing HCP mode, and
exactly `ocm-arohcpci00-` or `ocm-arohcpci01-` as the namespace prefix. No other
mode gains relaxed options. Strict readers/runners and ordinary editor `Apply`
still reject unconfigured targets as before.

Every report is fully validated by `readDataset` before filtering, including
memory rows; malformed second reports fail before any destination write. All
nonnull peaks from exact regular-container catalog identities on known management
clusters participate, including ineligible rows and rows missing request bounds.
The maximum peak across all distinct sources governs, rounded up to the v3 10m
quantum (minimum 10m), without additional headroom. Null peaks are unknown, not
zero. Unresolved owners, wrong kinds, init containers, unknown cluster roles and
uncataloged identities are skipped with warnings, never mapped by inference.
The shared static catalog covers all 58 identities (seven original and 51
additional); its existing parity test checks the audited YAML. Invalid annotation
targets are skipped with a diagnostic before planning reductions or seeds. The
experimental runner uses this compiled catalog rather than loading a catalog
beside the config.

Existing CPU requests must be positive numeric values. Equality with the target
prints `NOOP` **before** stale checking, so reapplying the same dataset converges.
Other changes require current to lie in the **union** of known observed request
ranges, not the envelope across gaps. The normal inclusive deadband remains;
increases are printed and skipped. No request bounds means no observed baseline,
not zero. Missing bounds do not discard a known peak.

Additional mode intentionally seeds an absent cataloged CPU identity/resource only
when all known request bounds agree on one positive observed baseline. Ambiguous,
nonpositive and entirely unknown baselines are refused; unknown bounds are warned
about. Seeds are written directly at the reduced CPU target, not at a guessed
baseline. New IDs are deterministic `workload-container` strings; existing IDs are
retained. ID collisions and conflicting default/dev identities fail staging, even
in a dry run. The original seven are never additional targets. Global defaults
stay unchanged, including the empty additional map; only dev identities and CPU
leaves can be inserted. Existing memory is preserved and new seeds have no memory.

Each invocation edits **one file** atomically, preserving mode and checking that
the source has not changed since reading. Run twice for the two scopes; there is
no two-file transaction. No reports, memory requests, limits, other size classes,
unrestricted template branch, public config, or normal service requests change.
Dry runs perform additional CPU staging/validation in a temporary file, then
discard it without replacing the destination. No rendering or commits occur.

Audit stdout includes canonical source checksums, each source window, the latest
window end, all report and row warnings (including ignored rows), governing maxima
and sources, skipped increases, and summary counts. `missing-report-targets` counts
target/report pairs, `unmeasured-replicas` counts reported replica deficits across
distinct sources, and `unknown-identities` counts selected-prefix CPU rows not
mapped in the selected scope. These counts are not distinct live pod totals.
Warnings can be voluminous; retain the full stdout when reviewing the experiment.
The tool reports gross reductions only, **not net savings or a safety guarantee**.
HCP resource floors are not applied, live limits are not checked, size class is a
user assertion, and unobserved replicas may have higher peaks.

The CI prefix constrains input evidence, not deployment scope: the original
template edits affect its shared `e2e_minimal` branch, and additional overrides
are written under `clouds.dev.defaults`. They therefore apply to all dev
environments using that limited size class, not just the sampled CI shard.

Reproduce the authorized four-report experiment from the repository root, without
applying either file:

```bash
go run ./tooling/rightsize-requests \
  --input /tmp/opencode/right-sizing-v3-2103929857506283520/right-sizing.json \
  --input /tmp/opencode/right-sizing-v3-2103981635216084992/right-sizing.json \
  --input /tmp/opencode/right-sizing-2104275998722756608/right-sizing.json \
  --input /tmp/opencode/right-sizing-2104335322849480704/right-sizing.json \
  --sizing-template hypershiftoperator/deploy/templates/cluster.clustersizingconfiguration.yaml \
  --namespace-prefix ocm-arohcpci01- \
  --experimental-hcp-cpu-reductions --allow-decrease --dry-run

go run ./tooling/rightsize-requests \
  --input /tmp/opencode/right-sizing-v3-2103929857506283520/right-sizing.json \
  --input /tmp/opencode/right-sizing-v3-2103981635216084992/right-sizing.json \
  --input /tmp/opencode/right-sizing-2104275998722756608/right-sizing.json \
  --input /tmp/opencode/right-sizing-2104335322849480704/right-sizing.json \
  --additional-hcp-config config/config.yaml \
  --namespace-prefix ocm-arohcpci01- \
  --experimental-hcp-cpu-reductions --allow-decrease --dry-run
```

These are the selected saved reports, not the earlier failed `210406...` run.
With the pre-experiment baseline and default 10% deadband, they propose nine
reductions derived from the catalog and evidence, not hardcoded target values:

| Scope | Workload / Container | CPU Before | CPU Target |
|---|---|---:|---:|
| Template | cluster-policy-controller / cluster-policy-controller | 100m | 10m |
| Template | kube-controller-manager / kube-controller-manager | 100m | 30m |
| Template | openshift-apiserver / openshift-apiserver | 100m | 30m |
| Template | openshift-controller-manager / openshift-controller-manager | 100m | 20m |
| Additional seed | azure-cloud-controller-manager / cloud-controller-manager | 75m observed | 10m |
| Additional seed | etcd / etcd-metrics | 40m observed | 20m |
| Additional seed | konnectivity-agent / konnectivity-agent | 40m observed | 10m |
| Additional seed | kube-scheduler / kube-scheduler | 25m observed | 10m |
| Additional seed | router / router | 50m observed | 10m |

The template run reports four changes, etcd already at 100m, a skipped
kube-apiserver increase to 200m, and a stale ovnkube-control-plane baseline. The
additional run reports five changes/five seeds, skipping route-controller because
its 69-character annotation suffix exceeds Kubernetes' 63-character limit.
Different evidence or current configuration can change these results. Removing
`--dry-run` applies that one scope only; review both plans and verify actual
rendered charts separately.

**Keep this as a dry run until representative minimal-cluster evidence and live
limits have been verified.** `--additional-hcp-config` and `--sizing-template`
are mutually exclusive; neither accepts explicit `--config`, `--write-config`,
`--source-prefix`, or `--write-prefix`. Both are input-only and never render or
commit. This mode does not edit the seven literal template entries, other size
classes, public-cloud overrides, or limits.

### Report Contract

Every selected report is validated before any writes. All example fields below are
required, with exact key spelling; unknown/duplicate fields, null nonnullable fields and
trailing JSON are rejected. `version` must be `3`, `headroom` must be `1`, and
`cpuWindow` must be `10m` or `2m`. `start` and `end` are nonzero RFC3339 timestamps
with `start <= end`. `changeThreshold` is a required finite number in `[0,1]`
(the producer defaults to `0.1`); `actionable` and `alertRisk` are required booleans
on every recommendation. Empty arrays are allowed.

Version 1 right-sizing reports used incompatible nearest rounding, and version 2
reports added 20% headroom. Both are rejected, even when an individual suggestion
happens to agree. Rerender the original replica peaks via `render-right-sizing`;
do not just change the report's
version field. The raw `replica-peaks` artifact remains version 1, independently
of the version 3 right-sizing report consumed here. Relabeling old reports is not
a migration: the headroom and suggestion formula are also validated.

```json
{
  "version": 3,
  "start": "2026-09-01T00:00:00Z",
  "end": "2026-09-02T00:00:00Z",
  "headroom": 1,
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
    "suggested": 0.2,
    "suggestedQuantity": "200m",
    "delta": -0.1,
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
An optional `samples` field carries a nonnegative usage-observation count for
display. Missing or null means unknown; it does not influence edit decisions.
`requestMin` cannot exceed `requestMax`. Null measurements are unknown, not zero.
A null suggestion requires an empty `suggestedQuantity`.

Optional top-level `savings` (absent or null is accepted) contains `time` and
`resources`. Each resource entry has `cluster`, `resource` (`cpu` or `memory`),
`before`, `after`, `reductions`, `increases`, `changedContainers`,
`excludedContainers`, and `unknownContainers`. The timestamp must fall within the
report window; totals are finite nonnegative cores/bytes and counts are nonnegative
concurrent container instances. This optional field is informational:
CLI suggestions and no-op/edit decisions still use recommendation evidence and
effective current requests, never the savings summary.
Version-3 reports without `samples` or `savings` remain valid.

Live collection builds savings from the highest-pod-count retained utilization
snapshot within the report window (earliest on ties). Offline, add optional
`--utilization-input utilization.json` from the same run to `render-right-sizing`.
Known regular-container requests are compared before/after global target-max
suggestions, without historical replica multipliers. Multi-pod placement aggregates
and missing, stale or limit-uncertain changes are excluded; known excluded requests
stay unchanged, while unknown requests make totals lower bounds. This assumes
decreases and dev/minimal-HCP applicability, not proven size class or an actual CLI
plan under current repository stale/limit checks. It estimates neither node nor
cost savings. Without a usable snapshot, savings is unavailable.

The numeric `suggested` and parsed `suggestedQuantity` must agree with this formula,
where `unit` is 10m CPU (0.01 cores) or 10Mi memory:

```text
suggested = max(1, ceil(peak / unit)) * unit
```

Rounding uses ceiling chunks with a minimum of 10m/10Mi, without a separate
safety floor or nearest-rounding tolerance. Exact chunks remain unchanged and
values just above a chunk round up: a 100m CPU peak yields 100m, while a 100.1m
peak yields 110m. A 12m CPU peak yields 20m; a 100.1Mi memory peak yields 110Mi.
Input values are written directly:
no headroom is added or rounding applied again, nor is Grafana's 16Mi memory rounding
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
`pkg/targets/targets.go` can be extended; the tool never guesses.

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
| `--input` | *(one source required)* | Version 3 offline report; repeat once per file, commas are literal; dev request overrides or HCP sizing template, no credentials |
| `--sizing-template` | *(none)* | Input only: edit the seven existing limited-branch `e2e_minimal` requests in this Helm template |
| `--additional-hcp-config` | *(none)* | Input only: size existing `hypershift.additionalMinimalResourceRequests` in this config, writing dev overrides; never seed missing targets/resources |
| `--namespace-prefix` | *(none)* | Required with either HCP mode: explicit literal HCP environment prefix, e.g. `ocm-arohcpci01-` |
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

`pkg/targets/targets.go` is the canonical shared catalog: the original 13
namespace/container mappings **plus expanded service/infrastructure mappings**,
seven literal minimal-HCP targets, and 51 audited additional minimal-HCP identities
(49 usable; route-controller and csi-snapshot-controller-operator have overlong
annotation suffixes).
The old 13-mapping inventory is not the current coverage list. See the
[implemented-target tables](../../docs/configurable-resource-requests.md#implemented-config-paths)
for exact paths, roles, and policy sources. `internal/rightsize/mapping.go` adapts
the service catalog for the CLI. Legacy Grafana lookup lacks owner/role evidence
and supports only unambiguous concrete namespace/container mappings, not the full
offline catalog.

Container names must match the `container` label emitted by cAdvisor /
kube-state-metrics (the pod spec container name). In particular,
`secretSyncController.k8s.resources` maps to `provider-azure-installer`, while
`Deployment/secrets-store-sync-controller-manager/manager` maps separately to
`secretSyncController.k8s.controllerResources`. A `manager`-only mapping is unsafe.
The original table's 2026-08-28 int-cluster check does not validate the expanded
catalog; chart/API source checks are not proof of live deployment or sizing.
Run with `--dry-run` first and check the "unmapped workloads" section of the
report to catch any drift (e.g. a renamed container) before writing.

## Development

```bash
go build ./...
go test ./...
```
