# DEV CI Telemetry Exporter (`tenant-quota`)

`tenant-quota` is the historical name of the extensible DEV CI telemetry exporter running on the standalone `opstool` AKS cluster. It began by collecting tenant and subscription quota data, but its scope is growing beyond quota-only telemetry.

Current examples include tenant capacity, subscription quota, E2E resource-group expiry, and Prow job outcomes and durations. The design supports additional CI telemetry sources without turning this README into a fixed collector or metric inventory.

For alert response, routing, and monitoring maintenance, use the canonical [DEV CI Monitoring and Alert Response](../../docs/ci/dev-ci-monitoring.md) runbook.

## Collection Sources of Truth

Use the implementation and deployment sources rather than maintaining a copied inventory here:

- [`main.go`](main.go) registers the collectors run by the process and defines its HTTP endpoints.
- [`pkg/`](pkg/) contains collector, metric, configuration, and credential behavior.
- [`config/config-dev-ci.yaml`](../../config/config-dev-ci.yaml), under `opstool.tenantQuota`, is the source of truth for deployed configuration.

## Runtime Model

At startup the process loads the rendered runtime config, validates credentials, starts watching mounted secret files, resolves subscription IDs when needed, starts the registered collector loops, and serves its HTTP endpoints.

The detailed startup, configuration, credentials, and rendered deployment behavior is defined in:

- [`main.go`](main.go)
- [`pkg/config/config.go`](pkg/config/config.go)
- [`pkg/credentials/provider.go`](pkg/credentials/provider.go)
- [`deploy/config.yaml.tmpl`](deploy/config.yaml.tmpl)

The service listens on port `8080` and exposes `/healthz`, `/readyz`, `/version`, and `/metrics`.

## CI Discovery And Outcomes

Two independent URI-keyed controllers share Kusto clients. Discovery enumerates
public GCS job names and runs under the configured bucket and job-name filter:
PR directory aliases, batch presubmits, branch jobs, and periodic jobs. It follows
all listing pages without recursively listing artifacts. There are no Sippy calls.
Each listing scope has a Kusto-derived maximum build-ID cursor with overlap,
periodic repair, and an optional `startupSince` override. Discovery writes
canonical `jobUri`, `buildId`, and `jobName` rows to `ciDiscoveredJobs`.

Processing selects discovered URIs whose build IDs are absent from `ciProcessedJobs`,
without an age cutoff. It waits for Prow completion and `completionDelay`, then
reconciles the independent `run`, `tests`, `names`, `observability-tests`, and
`observability-names` batches. Only visible semantic tags (or handled empty/absent
sources) permit the processed acknowledgment. Accepted submissions are not proof
of visibility. The bounded TTL cache suppresses recent submissions, not pending
work. Errors retry; incomplete or settling jobs are rescheduled normally.

Deploy the `ciDiscoveredJobs` and `ciProcessedJobs` tables and mappings, plus updated
outcomes/results mappings, before ingestion or controller rollout. The outcomes,
test names, and test results tables retain their existing semantic batch identities.
Observability failures are independent of E2E results. See the
[schema sources](../../dev-infrastructure/modules/logs/kusto/tables/) and
[`ci-outcomes.example.yaml`](ci-outcomes.example.yaml) for current settings.

Metrics expose both queues, retries, processing duration, and submission failures.
`exitOnPanic` controls whether recovered panics terminate the process; the CLI uses
the same policy. Discovery repair is not a completeness watermark: old missing
discovery rows still require a wider explicit scan. Once discovery rows are visible,
the pending-work anti-join survives restarts and late completion.

## One-Shot CLI

Run from `tooling/tenant-quota`. Both commands dispatch before service startup,
never load tenant secrets or start an HTTP server, and require `--config` pointing
at runtime YAML. All CI config is validated even when the collector is disabled.
Neither command polls, reads live cursors, or retries selected keys. Failures do not
prevent independent keys from being attempted. Both default to read-only; only
explicit `--ingest` permits writes. `--dry-run=false` is rejected.

The collector-only [`ci-outcomes.example.yaml`](ci-outcomes.example.yaml) selects
bucket `test-platform-results-public` and job-name substring `e2e-parallel`, with
Kusto database `ServiceLogs`. Its `enabled: false` disables daemon collection, not
these commands. Discovery previews do not use the configured Kusto endpoints.

```bash
# Exact artifact inspection, with no Kusto or Azure credentials.
go run . ci-outcomes --config ci-outcomes.example.yaml \
  --job-uri gs://test-platform-results-public/logs/periodic-ci-Azure-ARO-HCP-test/1976270000000000123 \
  --inspect-artifacts

# Scoped discovery preview, with no Kusto or Azure credentials.
go run . ci-discovery --config ci-outcomes.example.yaml \
  --since 2026-10-08T00:00:00Z --until 2026-10-09T00:00:00Z \
  --limit 100 --workers 1

# Exact outcome ingestion (substitute a real URI selected by discovery).
AZURE_TOKEN_CREDENTIALS=AzureCLICredential go run . ci-outcomes \
  --config ci-outcomes.example.yaml --job-uri gs://BUCKET/logs/JOB/BUILD_ID --ingest
```

`ci-outcomes` requires repeatable `--job-uri gs://...` flags. Canonical URIs are
deduplicated and sorted; one trailing slash is accepted. `--build-id` and `--release`
are no longer supported. Default read-only processing checks live processed rows
and batch tags using only a query client, never ingestors or mapping changes.
`--inspect-artifacts` bypasses Kusto and its credentials, ignoring existing rows
and tags; it cannot be combined with `--ingest`.

`ci-discovery` accepts optional repeatable exact `--job-name` selectors. By default
it selects all names matching the configured bucket/filter and supported scopes.
It requires `--since`, explicit `--until`, and positive `--limit`. Bounds are
inclusive and apply to **build-ID snowflake time**, not artifact start/completion
or ingestion time: a run exactly at `--until` is included. Every page of every
selected scope is read. Canonical references are deduplicated by URI and sorted by
ascending numeric build ID, then canonical URI as the tie-breaker, **before** the
global limit is applied. Validated 19-digit IDs can be compared lexicographically
without numeric conversion. Thus `--limit 3` selects the earliest three references,
not the first three PR paths. Exact `ci-outcomes --job-uri` selection retains URI
ordering; only discovery requires chronological ordering.
Different URIs sharing a build ID remain distinct discovery rows. Unknown selected
names are errors. Dry-run reports rows without Kusto access; ordinary `--ingest`
uses the shared per-URI discovery reconcile to skip already visible rows.

`--all-history` replaces `--since`, never `--until`. Without `--bulk`, it still
requires a positive limit. `--workers` must be positive when supplied and defaults
to the corresponding controller's configured count. Use `--workers 1` for ordered
per-key reports. Bulk mode does not use per-key workers.

Logs go to stderr. Stdout contains JSONL `BatchReport` objects with optional
`jobUri`, `buildID`, `tag`, `table`, `status`, `rows`, and dry-run `payload` rows.
Statuses include `existing`, `empty`, `unavailable`, `would-submit`, `submitted`,
`pending`, `waiting`, and `error`. `pending` describes incomplete/settling work;
`waiting` indicates that reconciliation requested a future check. Both return
normally without waiting or looping. Re-run explicitly or let the daemon continue.
`submitted` means accepted for queued ingestion, not confirmed queryable. Processed
acknowledgment previews may have no payload. Source failures can coexist with usable
partial batches. Selection, initialization, reconciliation, output, cancellation,
and recovered panic errors produce a nonzero exit. Azure-backed modes use the
existing credential chain, for example `AZURE_TOKEN_CREDENTIALS=AzureCLICredential`.
SDK retry sleeps may delay cancellation even after HTTP requests stop.

For a compact discovery preview summary (three rows if at least three match):

```bash
go run . ci-discovery --config ci-outcomes.example.yaml \
  --since 2026-10-08T00:00:00Z --until 2026-10-09T00:00:00Z \
  --limit 3 --workers 1 > discovery-preview.jsonl
jq -s '{reports: length, rows: (map(.rows) | add // 0), statuses: (map(.status) | unique)}' \
  discovery-preview.jsonl
# Example summary: {"reports":3,"rows":3,"statuses":["would-submit"]}
jq -r '.payload[] | [.buildId, .jobUri] | @tsv' discovery-preview.jsonl
# IDs ascend numerically; equal IDs are ordered by canonical URI.
```

## Pre-Deployment Bootstrap

The ad hoc script is only a wrapper around `ci-discovery --all-history --bulk`.
It has no alternate GCS parser, persisted replay state, or secret handling. Supply
an explicit fixed `--until`; omitting it is an error. Bulk mode rejects `--limit`
and enumerates **all supported history** through that bound, across all configured
matching scopes (or explicit job names). The core currently supports canonical
19-digit snowflakes; older unsupported IDs fail rather than being silently skipped.
All rows are held in memory, canonicalized, deduplicated, and sorted by ascending
build ID then canonical URI (the same order as bounded discovery) before one
submission via the shared discovery writer. Any listing failure prevents that
submission. Empty selections submit nothing. The batch tag hashes the exact sorted
row content, so identical reruns reuse the same identity. Changed selections may
overlap earlier batches; queries should use distinct URIs rather than raw counts.

```bash
# Safe default: one JSONL report containing all candidate rows, no Azure access.
bash scripts/backfill-ci-discovery.sh --config ci-outcomes.example.yaml \
  --until 2026-10-09T00:00:00Z > discovery-preview.jsonl

# Only after reviewing the preview and deploying Kusto schemas:
AZURE_TOKEN_CREDENTIALS=AzureCLICredential \
  bash scripts/backfill-ci-discovery.sh --config ci-outcomes.example.yaml \
  --until 2026-10-09T00:00:00Z --ingest > discovery-submitted.jsonl

# Preview count and exact URI manifest for verification:
jq '.rows' discovery-preview.jsonl
jq -r '.payload[].jobUri' discovery-preview.jsonl
# One bulk report, unlike the per-key reports above:
jq '{status, rows, tag}' discovery-preview.jsonl
# Example shape: {"status":"would-submit","rows":1234,"tag":"discovered-bulk-<sha256>"}
```

The command does **not** wait for ingestion visibility. Before deploying the daemon,
verify the preview manifest against Kusto, not just command success. For exact
verification, load the preview's URI list into `Expected` (a Kusto `datatable`,
or a temporary query input for large manifests), then count and anti-join:

```kusto
let Expected = datatable(jobUri:string) [
  // Insert every URI from the preview manifest, not just this example.
  'gs://BUCKET/logs/JOB/BUILD_ID'
];
let Visible = ciDiscoveredJobs | distinct jobUri;
Expected | join kind=leftanti Visible on jobUri
// Must return zero rows. Also compare Expected | count with:
// Expected | join kind=inner Visible on jobUri | count
```

After rollout, monitor the durable processing backlog independently:

```kusto
ciDiscoveredJobs
| distinct jobUri, buildId
| join kind=leftanti (ciProcessedJobs | distinct buildId) on buildId
| summarize pendingJobs=count()
```

## Deployment Layout

The rollout is owned by `Microsoft.Azure.ARO.HCP.DevCI.TenantQuota` in [`pipeline.yaml`](pipeline.yaml).

The pipeline:

- reads shared outputs from [`dev-infrastructure/templates/output-opstool-cluster.bicep`](../../dev-infrastructure/templates/output-opstool-cluster.bicep)
- deploys the Helm chart using [`deploy/values.yaml.tmpl`](deploy/values.yaml.tmpl)
- deploys Azure Monitor rule groups from [`alerting.bicep`](alerting.bicep)

For cluster architecture, shared monitoring, identity, secret, and workload patterns, see [Opstool CI Platform](../../docs/ci/opstool.md).

## Configuration Source of Truth

The source of truth for deployed configuration is [`config/config-dev-ci.yaml`](../../config/config-dev-ci.yaml), under `opstool.tenantQuota`.

Do not update tenant definitions in `deploy/values.yaml`. That file contains static chart defaults.

Subscription IDs are resolved at runtime from configured subscription display names rather than stored in the config.
Role assignment usage and limits are retrieved directly from Azure rather than configured per subscription.

## Secrets and Credential Reload

Client secrets live in the `opstool` workload Key Vault and are mounted into the pod with the CSI Secret Store driver.

Credential reload behavior is defined in [`pkg/credentials/provider.go`](pkg/credentials/provider.go). A Key Vault secret update can be picked up without restarting the pod when the CSI-mounted file refreshes and the process rereads the invalidated credential on its next use.

## Alerting

[`alerting.bicep`](alerting.bicep) is the source of truth for alert names, expressions, thresholds, durations, annotations, and routing. Rules are deployed into the `opstool` Azure Monitor Workspace and use the shared `opstool-pagerduty` Action Group supplied by the `DevCI.Unprivileged` rollout.

Do not duplicate the evolving alert catalog in this README. See [DEV CI Monitoring and Alert Response](../../docs/ci/dev-ci-monitoring.md) for response and maintenance procedures.

## Local Development

Run all commands from `tooling/tenant-quota`.

The [`Makefile`](Makefile) is the source of truth for supported local-development and image-workflow targets.

Example local workflow:

```bash
cd tooling/tenant-quota
make render-config
make fetch-secrets
make run
```

To run the containerized version locally:

```bash
cd tooling/tenant-quota
make run-image
```

## Managing Tenants

### Add or reconcile a tenant service principal

Use:

```bash
cd tooling/tenant-quota
./scripts/manage-service-principals.sh --tenant redhat
./scripts/manage-service-principals.sh --list
```

This script is the supported path for creating and reconciling the service principals, role assignments, and Key Vault secrets used by the exporter.

After adding or changing a tenant:

1. Update [`config/config-dev-ci.yaml`](../../config/config-dev-ci.yaml).
2. Redeploy the `Microsoft.Azure.ARO.HCP.DevCI.Unprivileged` entrypoint with `make dev-ci-local-run`. For a targeted redeploy, run only the `Microsoft.Azure.ARO.HCP.DevCI.TenantQuota` service group.

### Renew a client secret

List current tenant credentials:

```bash
cd tooling/tenant-quota
./scripts/renew-sp-secret.sh --list
```

Renew one tenant:

```bash
cd tooling/tenant-quota
az login --tenant <azure-ad-tenant-id>
./scripts/renew-sp-secret.sh --tenant RedHat0
```

If needed, the script can also restart the deployment:

```bash
./scripts/renew-sp-secret.sh --tenant RedHat0 --restart
```

Because the runtime watches mounted secret files, a restart should normally be optional and is mainly a recovery step if the rotated secret does not propagate promptly.
