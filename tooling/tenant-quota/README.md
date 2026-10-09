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

## CI Outcomes Controller

The CI outcomes controller polls Sippy every `ciJobOutcomes.interval` (default `5m`). For each release it reads Kusto's maximum `startedAt` among completed jobs and subtracts `overlap` (default `3h`) to form the polling cursor. On startup and every `repairInterval` (`12h`), it polls from `min(cursor, now - window)`, where `window` is the repair lookback (`24h`). An empty release starts with that repair lookback. The window is not a cap on recovery after an outage: an older cursor still wins.

Set optional `ciJobOutcomes.startupSince` to an RFC3339 timestamp (for example, `"2026-09-15T00:00:00Z"`) to extend startup discovery for a backfill. It defaults to empty. Until each release's first successful discovery scan, the controller uses `min(derived since, startupSince)`; an earlier cursor or repair lookback still wins. Failed scans retry with the override. Subsequent polls use normal cursor overlap and periodic repair lookbacks, ignoring `startupSince`. There is no persisted cursor or startup checkpoint: every process restart repeats the override until you remove it or set it to empty. A successful discovery only means runs were enqueued, not that ingestion finished.

This is bounded recovery, not a durable completeness watermark. A restart can lose pending runs older than both the current overlap and repair window; asynchronously failed submissions outside those windows likewise require an explicit wider backfill. Sippy exposes job start time, so unusually late completions or imports can also fall outside discovery. In-process retries do not expire when a run ages out of a discovery window.

Discovered runs are sorted and enqueued unconditionally, rather than subtracting runs already present in Kusto. A rate-limited workqueue drives `workers` (`10`) concurrent reconciles. There is no per-pass or per-reconcile timeout; the top-level collector `timeout` remains unchanged for other collectors. Transient failures retry indefinitely until success or process shutdown. Permanent errors, including HTTP 404 and malformed artifacts, are logged and forgotten rather than retried.

Ingestion uses immutable semantic batch tags for `run`, `tests`, and `names`, plus independent `observability-tests` and `observability-names` batches. Retrying a semantic batch reuses its identity so Kusto can suppress duplicate ingestion. The controller does not call ingestion `Wait` or retain completion tracking; successful submission is not a claim that the rows are already queryable. Cursor overlap and repair polls allow missing batches to be submitted again.

Sippy metadata and recently submitted batches use bounded, expiring in-memory caches. Each cache is limited by the same `cacheSize` (`20000` entries) and `cacheTTL` (`15m`) settings; neither is a durable checkpoint. Eviction, expiry, or restart can cause another submission, which remains safe through semantic batch tags. Worker/cache sizes and durations must be positive. The collector requests and limits `1Gi` of memory.

Controller and workqueue metrics are exposed through `/metrics` alongside the other collectors. Use queue depth, retries, processing duration, and ingestion submission failures to distinguish a growing backlog from healthy duplicate suppression. Recovered controller panics are logged and counted; top-level `exitOnPanic` defaults to `false` and can be set to `true` to terminate on panic instead of continuing. Local configuration and the Helm ConfigMap use the same setting.

The existing Kusto tables remain the storage contract: `ciJobOutcomes` for runs, `ciTestNames` for names, and `ciTestResults` for results. The final `ciTestResults.message` string column carries failure or diagnostic text, including observability test details. [`ciTestResultsMapping`](../../dev-infrastructure/modules/logs/kusto/tables/ciTestResults.kql) maps it from the JSON `message` field; absent messages are empty. Apply the updated table and mapping before rolling out the writer. No additional tables or schemas are required.

E2E results retain their existing JSON-derived fields and skip filtering. Observability ingests only failed/error cases from `junit_alerts.xml`, including collection-completeness failures, using `alerts.json.timeWindow` as the evaluated interval. Passing and known-issue skipped alerts are not copied. Old `run-<buildID>` and `tests-<buildID>` tags remain authoritative; previously ingested E2E messages are not backfilled. Observability tags are separate so old E2E ingestion cannot suppress new observability batches.

## One-Shot CI Outcomes CLI

Run from `tooling/tenant-quota`. The CLI dispatches before service startup and never
loads tenant secrets, contacts Key Vault, or starts an HTTP server. Use rendered
runtime YAML, or the collector-only [`ci-outcomes.example.yaml`](ci-outcomes.example.yaml).
All CI settings are validated even if `ciJobOutcomes.enabled` is `false`.

```bash
go build -o tenant-quota-collector .

# Default dry-run: live Kusto tag checks, then JSON rows for missing batches.
AZURE_TOKEN_CREDENTIALS=AzureCLICredential ./tenant-quota-collector ci-outcomes \
  --config ci-outcomes.example.yaml --build-id 123 --build-id 456

# Public artifact inspection: no Kusto queries or Azure credentials, ignoring tags.
./tenant-quota-collector ci-outcomes --config ci-outcomes.example.yaml \
  --release Presubmits --since 2026-10-08T00:00:00Z \
  --until 2026-10-09T00:00:00Z --limit 5 --inspect-artifacts --workers 2

# Explicit opt-in ingestion, with the same selection and reconciliation logic.
AZURE_TOKEN_CREDENTIALS=AzureCLICredential ./tenant-quota-collector ci-outcomes \
  --config ci-outcomes.example.yaml --build-id 123 --ingest
```

Select either repeatable `--build-id` flags or repeatable `--release` flags with
all of `--since`, `--until`, and positive `--limit`. The interval applies to **job
start**, inclusive at `since` and exclusive at `until`, not completion or ingestion.
Candidates from all selected releases are sorted by start time, build ID, and
release, then deduplicated by ID before the global limit. Exact IDs use the existing
Sippy exact-ID API across configured releases; they are deduplicated and sorted.
There is no polling, repair, cursor expansion, or controller retry: each key is
attempted once. Failed keys do not prevent other selected keys from being attempted.
`--workers` defaults to the runtime config; use `--workers 1` for ordered output.

Dry-run constructs only a query client and checks live semantic extent tags on
each invocation. It does not construct ingestors or check ingestion mappings.
`--ingest` additionally requires deployed schema mappings and ingestion permissions.
`--inspect-artifacts` bypasses Kusto entirely and rejects `--ingest`; configured
Kusto settings are still validated but not used. `--dry-run` is optional and cannot
be combined with `--ingest`. Azure-backed modes use the existing default credential
chain and require `AZURE_TOKEN_CREDENTIALS` (for example, `AzureCLICredential` after
logging into the appropriate dev tenant).

Logs go to stderr; stdout contains JSONL reports with `buildID`, semantic `tag`,
`table`, `status`, and `rows` (row count). Status is `existing`, `empty`,
`unavailable`, `would-submit`, `submitted`, or `error`. Dry-run `would-submit`
reports always include the exact JSON rows in `payload`. `existing` reports have
zero rows because their artifacts are not read; unavailable sources are distinct
from valid empty sources. A source may report an error and still emit usable
partial batches, following the controller's artifact behavior. Build-level error
reports additionally summarize failed reconciles. Selection, initialization,
reconciliation, output, cancellation, and recovered panic errors produce a nonzero
exit. Permanent absent/malformed sources retain the controller's skip semantics
and are reported as unavailable rather than retried. `submitted` means queued,
not confirmed queryable. Signal cancellation and `exitOnPanic` use the service's
existing policy and panic metrics handlers. Kusto HTTP requests, including SDK
background auth metadata and resource discovery, are bound to the command or
controller lifetime. SDK resource-discovery retry sleeps are not interruptible,
so cancellation can still delay return after the HTTP request has stopped.

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
