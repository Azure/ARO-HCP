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

At startup the process loads the rendered runtime config, loads and validates both credential files once, resolves subscription IDs when needed, starts the registered collector loops, and serves its HTTP endpoints.

The detailed startup, configuration, credentials, and rendered deployment behavior is defined in:

- [`main.go`](main.go)
- [`pkg/config/config.go`](pkg/config/config.go)
- [`pkg/credentials/provider.go`](pkg/credentials/provider.go)
- [`deploy/templates/configmap.yaml`](deploy/templates/configmap.yaml)

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

### Slot Acquisition Metadata

The `run` batch also reads
`aro-hcp-lease-acquire/artifacts/aro-hcp-slot-state.yaml` beneath the discovered
job artifact directory, for all collected job families. This is the versioned
snapshot published after successful slot acquisition, not a current lease or
release-status report. No log parsing or catalog lookup is used.

`ciJobOutcomes` records:

| Column | Meaning |
| --- | --- |
| `runtimeRegion` | Selected workload region from `runtime_region`, not the catalog region or `config.yaml`'s Kusto location |
| `leasedSlot` | Primary Boskos lease name |
| `leasedAssets` | Dynamic array of `{kind, resourceName}` objects from `leases.assets`, sorted by kind/name |

Only actual additional Boskos leases are stored, not asset requirements or every
resolved Azure resource. Multiple leases per kind and previously unseen kinds are
retained. A valid snapshot with no additional leases produces `[]`; unavailable
metadata produces `null` assets and empty region/slot fields. Missing, malformed,
and unsupported artifacts are logged and counted with the `slot-state` artifact
source; they do not prevent the outcome from being recorded. Transport errors
retry the run batch before submission. Failed acquisitions currently publish no
snapshot, so their partial lease selections are unavailable here.

Deploy the additive `ciJobOutcomes` schema and JSON mapping before this collector.
Ingestion startup checks the new mapping entries. **There is no backfill**:
processed builds are skipped, and an existing run-batch tag prevents rewriting
the outcome even if other batches are still pending. Historical rows have null
assets; they must not be interpreted as having no leases.
Rediscovery does not update these rows. `--inspect-artifacts` can preview the new
fields without writing or bypassing these guards for ingestion.

Regional failure counts:

```kusto
ciJobOutcomes
| where startedAt > ago(7d) and isnotempty(runtimeRegion)
| summarize runs = count(), failures = countif(failed) by runtimeRegion
```

Find runs that leased a particular asset:

```kusto
ciJobOutcomes
| where startedAt > ago(7d) and isnotempty(runtimeRegion)
| mv-expand asset = leasedAssets
| where tostring(asset.resourceName) == "asset-01"
| project buildId, runtimeRegion, leasedSlot, overallResult,
          assetKind = tostring(asset.kind)
```

Use the unexpanded outcome rows for run counts: `mv-expand` produces one row per
asset and omits runs with an empty array.

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

## Secrets and Credential Rotation

Client IDs and client secrets live in the `opstool` workload Key Vault and are mounted into the pod with the CSI Secret Store driver.

Credentials are loaded at startup and cached for the lifetime of the process. **Restart the exporter whenever either the client ID or the password changes.** CSI refresh alone does not update the running exporter. There is no credential watcher or hot reload. Startup rejects missing, unreadable, empty or malformed client-ID files and empty passwords without logging credential material. Syntax validation is not proof that a password belongs to an application; operators must verify that binding before activation. The shared `entra-app-credentials ensure-secret` publisher preserves existing values without checking their binding.

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

Run as the non-root owner of the local files, using a local Linux container engine. `fetch-secrets` keeps the directory at 0700 and files at 0600; `run-image` keeps both mounts read-only. Rootless Podman uses `--userns=keep-id:uid=65532,gid=65532` so the host owner maps to the image's non-root user. Rootful Docker without user-namespace remapping uses the caller's UID/GID for this local run only. The production image still declares `USER 65532:65532`.

Engine detection also handles a `docker` command backed by Podman. Rootless/userns-remapped Docker and rootful Podman are refused rather than running the container as root or weakening host permissions; use rootless Podman or `make run` instead. Remote engines and Docker Desktop file-sharing semantics are not supported by this local identity mapping. On SELinux-enforcing hosts, use a container-readable labeled workspace; this target does not relabel private files or disable SELinux. Restart the local process/container after fetching either changed credential.

## Managing Tenants

### Add or reconcile a tenant service principal

The RedHat0 service principal (Graph `Organization.Read.All` consent, `Reader` on every `ci.<env>` subscription, Key Vault client secret) is managed declaratively by the privileged entrypoint; see [Dev-CI Topology](../../docs/ci/dev-ci-topology.md#the-privileged-entrypoint). Reconcile it as an OWNERS-group member with:

```bash
make dev-ci-privileged-local-run
```

The Microsoft test-tenant (`testTestAro`) service principal is the only one still managed imperatively:

```bash
cd tooling/tenant-quota
make render-config
./scripts/manage-service-principals.sh --tenant test-tenant
./scripts/manage-service-principals.sh --list
```

Bootstrap reads the `TestTestARO` entry from the rendered collector config (`--config` overrides the default), and requires the active Azure CLI tenant to match before creating applications or granting Reader. New applications are created without a password; only after Reader grants succeed does bootstrap add a password and save a protected recovery handoff before attempting authentication or publication. The direct `--keyvault-only --app-id` path also targets the configured `TestTestARO` tenant. Uploading an existing handoff remains independent of the active CLI tenant and does not require local rendered configuration.

The credential scripts require Bash, Azure CLI, jq, yq v4, curl, and GNU coreutils (`stat -c` for protected-handoff checks). Use Linux or a development environment with GNU coreutils on PATH; native BSD `stat` is not supported. Bootstrap restricts subscription lookups to the configured tenant and rejects ambiguous display names.

If bootstrap publication fails, retain the printed handoff and retry its upload command rather than rerunning bootstrap. This reuses the same additive password, including after partial publication. Successful immediate publication removes the handoff; `--skip-keyvault` retains it for the vault writer. Existing handoff destinations are rejected before bootstrap mutations. If password creation has an uncertain outcome or saving the handoff fails, inspect the printed operation name before retrying; never revoke unrelated credentials.

After adding or changing a tenant:

1. Update [`config/config-dev-ci.yaml`](../../config/config-dev-ci.yaml).
2. Redeploy the `Microsoft.Azure.ARO.HCP.DevCI.Unprivileged` entrypoint with `make dev-ci-local-run`. For a targeted redeploy, run only the `Microsoft.Azure.ARO.HCP.DevCI.TenantQuota` service group.

### Renew a client secret

Render configuration and list the configured tenant names (no credential lookup):

```bash
cd tooling/tenant-quota
make render-config
./scripts/renew-sp-secret.sh --list
```

Renew one tenant:

```bash
cd tooling/tenant-quota
az login --tenant <azure-ad-tenant-id>
./scripts/renew-sp-secret.sh --tenant RedHat0
```

After verifying publication and the target Kubernetes context, the script can perform the required restart:

```bash
./scripts/renew-sp-secret.sh --tenant RedHat0 --restart
```

Without `--restart`, restart manually after successful publication. The script uses the current Kubernetes context; it does not obtain cluster credentials. Renewal reads tenant IDs and secret references from the rendered config (`--config` overrides the local default), resolves the client ID from Key Vault, verifies the current pair, and adds a password with `--append`. It never revokes the previous password automatically.

Renewal saves the new password in a protected recovery handoff **before** token verification or Key Vault publication. If either fails, retry the printed handoff upload command instead of running renewal again: upload does not mint another password. No restart occurs on failure. Successful immediate publication removes the handoff; deferred publication retains it for upload. Retain the printed operation name to identify the additive application password if password creation has an uncertain outcome or saving the handoff fails. Inspect that operation before retrying and never revoke unrelated credentials. A prepared handoff is not proof of authentication or publication.

An expired or otherwise unverifiable existing pair fails renewal closed, before minting. Do not treat a timeout or authentication failure as permission to replace an unverified binding. Recover by publishing to new generation-specific names using the privileged publisher (or test-tenant bootstrap), then switching both references as described below. This preserves the active binding and does not require weakening legacy-adoption checks.

### Safe publication and staged migration

Owner review of the credential scripts' file/publication handling and the local launcher's mounts and user-namespace mapping remains required before merge. Automated checks do not replace this security review.

1. Prepare credentials **before** deploying the new consumer. RedHat0 uses the privileged `E2ESubscriptionRBAC` pipeline and the unchanged `entra-app-credentials ensure-secret` publisher. It already creates `<prefix>-client-id` alongside `<prefix>-client-secret`, but preserves present values without tenant assertions, binding checks or read-back verification. For this publisher, the configured ID reference must equal the password reference with `-client-secret` replaced by `-client-id`. CI-bot reconciliation is unchanged.
2. Independently verify the application, configured tenant, client ID and password for **every** configured tenant, including `testTestAro`, before activation. Publisher success alone does not establish that an existing pair is valid. The separate tenant-quota bootstrap and renewal scripts retain token-backed pair checks, protected recovery handoffs and publication read-back; these safeguards do not apply to the shared Go publisher. No tenant-ID migration is included.
3. Run publishers serially for a given pair. Two Key Vault writes are not a transaction. A failed publication must not activate configuration or trigger a restart. The tenant-quota scripts support retrying the same pair from their protected handoff. The shared Go publisher has no such recovery handoff: if its password write fails after password creation, an unused additive password may remain and a rerun may mint another. Inspect uncertain outcomes and clean up unused credentials only after recovery. Token propagation failures may require a retry. Never revoke a working password during this recovery.
4. For **application recreation**, stage both values under new generation-specific names, for example `collector-g2-client-id` and `collector-g2-client-secret`. Do not overwrite an active client-ID name. Establish existing Graph consent and Reader grants on the new principal, verify the pair, then switch both references together. Recreating only the service principal may retain the AppID but still requires restoring grants.
5. Only after pair verification, deploy a compatible exporter image with `clientIdKeyVaultSecretName` and `clientSecretKeyVaultSecretName`. **This migration is not ready for deployment with the current image pin:** `config/config-dev-ci.yaml` still pins `sha256:fffb43f9e00b0b5b0505ca4754eedc2a5fbcf431d79517d47303da577c9c5d83`, annotated with revision `ee0fd6137bfb9160e7a6d4ef2483627a01830b5f`, whose configuration requires a raw client ID. Building, verifying and pinning an image containing the reference-only consumer remains a rollout prerequisite; no replacement digest is supplied here. Verify the actual image revision supports this configuration before cutover; a mutable tag alone is not evidence of compatibility. The old raw-client-ID configuration is not supported by the new binary, and the old binary cannot consume the new reference-only configuration. Keep the old image, configuration and pair together for rollback until acceptance completes; rolling back only the image is unsafe. The ConfigMap checksum triggers a rollout when references change; changing either value in place requires an explicit restart.
6. Keep workload and deployment identities read-only on the required Key Vault secrets. No Graph administration, credential-writing privileges, Kubernetes Secret mirroring, tenant-ID migration or RBAC redesign is required for the exporter.

For test-tenant bootstrap, `manage-service-principals.sh --tenant test-tenant --secret-name <generation>-client-secret` selects a new pair. When the application administrator and vault writer differ, use `--skip-keyvault --defer-secret-out /protected/new-pair.json`, then switch identities and run `--keyvault-only --secret-name <generation>-client-secret --secret-file /protected/new-pair.json`. The handoff is created with mode 0600, must not already exist, and binds the tenant, application, vault and both references. Upload validates the target and authenticates the pair without changing the Azure CLI login. Legacy one-line password files are rejected. Delete the handoff after successful upload; never print or commit it.

Renewal also supports `--defer-secret-out /protected/new-pair.json` instead of immediate publication, followed by the same upload command. Its initial verification requires read access to the existing vault pair and application-management access in the configured tenant; the uploading identity needs vault write access. No script changes identities automatically.

The renewal CLI now requires an explicit `--tenant`; `--list` only lists configured names and does not inspect live credential expirations. The former `--cluster` and `--resource-group` options and automatic AKS credential fetching are removed. Select and verify the intended Kubernetes context yourself before using `--restart`.

Upload accepts only an owned regular handoff file without group or other access (mode 0600 or 0400), not a symlink. It validates a private snapshot containing exactly one JSON object with string credential fields before publication. If transferring between operating-system accounts, use a protected transfer and establish destination ownership and permissions before upload; Azure login changes alone do not require a file ownership change.

### Operator acceptance (separately authorized)

No live deployment or secret access is needed for unit tests. For an authorized rollout:

1. Record publication success, both secret references/versions, image revision and rollout start time **without recording values**. Verify consent/Reader for all target subscriptions before cutover. Ensure CSI has mounted the complete pair; do not print mounted files or token responses.
2. Restart `deployment/tenant-quota-collector` in namespace `tenant-quota`; wait for rollout completion and a new pod UID. Confirm that the new pod's `SecretProviderClassPodStatus` object versions match the published versions of both secrets, without printing their values. Startup must report loaded credentials for every configured tenant without credential-validation errors. Local execution likewise requires stopping the process, fetching both files, then starting it again; never fetch into a directory while starting a process.
3. Prove fresh post-restart collection: inspect new-pod logs for `Updated quota metrics` (directory quota where enabled), `Collected subscription quota metrics` and `Collected resource group metrics` for each configured tenant/subscription. Verify subscription resolution, compute/network quota and role assignment results, with no authentication or partial-collection errors. Check at least one configured collection interval (default 15 minutes), and compare resulting metrics with that collection time. A green `/healthz`, a ready pod, or retained gauges alone do not prove authentication or collection succeeded. Restart clears the in-memory subscription cache (normally 24 hours).
4. Repeat with a same-ID password rotation and with a staged new-identity pair in a safe test environment. Confirm no new credentials are used until restart, then confirm new successful collection. Test an invalid/missing pair on a non-production rollout: startup must fail with a field-specific error and no values. Do not revoke the old pair until successful fresh collection is demonstrated.
5. On failure, retain or restore the previous image/configuration/pair and restart; repair publication or grants before retrying. Do not infer successful rotation from stale metrics.
