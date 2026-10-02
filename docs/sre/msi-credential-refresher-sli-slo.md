# ARO-25956: Define SLIs and SLOs for HCP Workload Identity

## Objective

The objective is to define SLIs and SLOs for the HCP-owned portion of the Workload
Identity journey — the MSI Credential Refresher, the component that requests and
rotates managed service identity (MSI) credentials in Key Vault on behalf of
customer identities — so that SRE can detect a credential-rotation failure before
it causes a customer-facing authentication outage.

No end-to-end Workload Identity SLO is defined by this document. HCP does not own
the customer's workload configuration or the customer application's token usage,
and a completed rotation is not proof that a workload can authenticate. This
journey is therefore scoped to the Credential Refresher component only. Out of
scope:

- customer-owned Workload Identity configuration;
- customer application configuration and token use;
- proof that a workload successfully authenticates after a rotation.

## Success Criteria

- SLIs are defined from the authoritative metric the component actually emits
  (verified against source), not from a log query or derived script.
- Each of the 5 baseline categories in the Alerting Recommendation ADR is formally
  assessed, with an explicit decision — adopt, defer, or not pursued — backed by
  production evidence and/or source-code verification, not assumption.
- Claims about root cause (e.g. orphaned secrets) are checked against production
  data in Kusto, not left as plausible-sounding theory.
- The one adoptable SLO today (credential expiry safety) has an explicit target,
  error budget, and measurement window.
- Gaps that block an SLO are documented as scoped follow-up work with a named
  repository, not left ambiguous.

## Dependencies

- Credential Refresher metrics scraped into Azure Managed Prometheus (status:
  spot-checked, not yet exhaustively verified across all regions — see Required
  Work).
- Production Grafana access (`Managed_Prometheus_services-<region>` datasources,
  instance `arohcp-prod-g5d9a9akashnb5gd.suk.grafana.azure.com`).
- Credential Refresher source (ADO repo `Credential-Refresher`) for verifying
  metric semantics and outcome-label behavior.
- ARO-HCP backend cluster-deletion flow (`backend/pkg/controllers/cluster/deletion/`)
  for the orphaned-secret fix identified below.
- Kusto access to the regional `hcp-prod-*.kusto.windows.net` clusters
  (`ServiceLogs` database) for verifying the orphaned-secret root cause against
  production data.

## Service Health Monitoring

### SLO

An SLO (Service Level Objective) is a specific, measurable, agreed-upon target
that defines the expected performance or health of an HCP service. The
Alerting Recommendation ADR requires five baseline categories — availability,
errors, latency, traffic, saturation — as the minimum coverage per journey.

#### Credential Expiry Safety SLO (adopt)

This is a domain-specific SLI, beyond the 5 ADR baseline categories, and it is
the only one of the six assessed signals adoptable today.

**What it measures:** whether customer MSI credentials are being kept ahead of
expiry by the refresher.

**Good events:** expiry observations at `d > 0` days until expiry.
**Bad events:** expiry observations at `d <= 0` days until expiry.
**Population:** all expiry observations for recognized MSI/UAMSI secrets.
**Source metric:** `credential_refresher_days_until_msi_credential_expiration_bucket`
— authoritative: this is the only production signal that reflects the
refresher's calculated renewal state, confirmed against source
(`internal/refresher/refresher.go`): `daysUntilExpiration := (cannotRenewAfter.Sub(now).Hours() / 24) - 90`.

**Target:** 100% of observed expiry evaluations at `d > 0`, over a rolling
30-day window per region.
**Error Budget:** 0% — any observation at `d <= 0` is a direct
customer-credential-expiry risk, not a tolerable degradation.
**Metric:** recording rule not yet implemented; proposed name once built:
`expirysafety:credential_refresher_days_until_msi_credential_expiration:sli_ratio_30d`.

```promql
(
  sum by (region, cluster) (
    max without (prometheus_replica) (
      increase(credential_refresher_days_until_msi_credential_expiration_bucket{le="+Inf"}[30d])
    )
  )
  -
  sum by (region, cluster) (
    max without (prometheus_replica) (
      increase(credential_refresher_days_until_msi_credential_expiration_bucket{le=~"^0([.]0)?$"}[30d])
    )
  )
)
/
sum by (region, cluster) (
  max without (prometheus_replica) (
    increase(credential_refresher_days_until_msi_credential_expiration_bucket{le="+Inf"}[30d])
  )
)
```

An absent or zero-denominator result must be treated as `NoData`, not as 100%.

**Known gaps before this SLO can be adopted as-is:**

1. The metric repeats on every refresh pass; it counts observations, not unique
   credentials. A single stuck credential inflates both the bad-event count and
   its own repeat weight. Confirmed in production on 2026-10-01 (7-day window,
   deduplicated): of 10 regions checked, 8 returned data. 6 of those 8 show a
   stuck/expired population — `westeurope`, `brazilsouth`, `centralindia`,
   `switzerlandnorth`, `uksouth`, `australiaeast` — ranging from ~7,000 to
   ~53,000 repeated observations at `d <= -90` with little or no movement
   across intermediate buckets, consistent with the same small set of secrets
   being re-observed every refresh cycle without ever resolving. The other 2
   (`eastus2`, `canadacentral`) were clean, with no observations below 30 days.
   The remaining 2 regions checked (`westus`, `eastus2euap`) returned no data
   at all — see Known Gap #3.
2. Key Vault secrets belonging to deleted HCP clusters remain in the population
   (orphaned secrets) and can trip the SLO for a resource that no longer exists.
   This is not a missing-cleanup-job problem: the Credential Refresher already
   has a purge mechanism (`internal/controller/purge_controller.go`,
   `internal/refresher/purge.go`), confirmed in source, but it only calls Key
   Vault's `PurgeDeletedSecret`/`ListDeletedSecretProperties` APIs — i.e. it
   empties the soft-delete recovery bin, it does not decide which live secrets
   are orphaned. The actual gap is upstream: no code path in the ARO-HCP repo
   ever calls `DeleteSecret` on a cluster's MSI Key Vault secret when the
   cluster is deleted (re-verified directly against
   `backend/pkg/controllers/cluster/deletion/` — 6 controller files, zero
   references to Key Vault, secrets, or identity deletion), and no code path
   deprovisions the underlying managed identity either — the `msi-dataplane`
   SDK's `dataplane.Client` interface exposes `DeleteSystemAssignedIdentity`,
   but ARO-HCP's own interface wrapper
   (`backend/pkg/azure/client/mi_dataplane_client.go`) deliberately narrows
   itself to a single method, `GetUserAssignedIdentitiesCredentials`, and no
   call site anywhere invokes deletion. Once ARO-HCP's cluster-deletion flow
   calls `DeleteSecret`, the Credential Refresher's existing purge controller
   will clean it up automatically on its next cycle — no new purge logic needs
   to be built in the Credential Refresher component. Root-cause confirmation
   against production data is below.
3. Scrape coverage of this metric into Azure Managed Prometheus was spot-checked
   across 10 regions on 2026-10-01: `westeurope`, `brazilsouth`, `centralindia`,
   `switzerlandnorth`, `uksouth`, `australiaeast`, `eastus2`, and `canadacentral`
   all returned data; `westus` and `eastus2euap` returned no data for this
   metric at all, confirming a real scrape gap in at least those two regions.
   This was a sample, not an exhaustive check — these 10 regions were directly
   observed live in Grafana and Kusto.

#### Root-cause confirmation for Known Gap #2 (orphaned secrets)

Confirmed with two independent, cross-verified examples via Kusto:

- **UK South** — `internalID 2p8orvoq5fqd0vdv9plga3q4dt9f45da`, a
  `Not-Renewable` identity whose 10 operator secrets became eligible for
  renewal on 5/10/2026 and passed the point of no return
  (`CannotRenewAfterDateTime`) on 9/21/2026. As of the 2026-10-01 check, it
  does not resolve to any currently-active cluster in `backendLogs`, checked
  two ways: against a reduced baseline of 30 active clusters (24h window) and
  31 active clusters (48h window), and against every raw matching log line
  directly (no reduction) over the same windows. All checks returned zero
  matches for this ID while correctly returning dozens of other real, active
  clusters.
- **Brazil South** — `internalID 2p1i8s7fsjlq5gh0ludi77e1m5p4r8v6`, renewal due
  4/29/2026, past the point of no return on 9/10/2026. Same two-way
  verification as of the 2026-10-01 check: zero matches against baselines of
  45 (24h) and 53 (48h) active clusters, and against the raw unreduced log
  scan.

This confirms the Not-Renewable population includes genuinely orphaned
secrets, not silent live-customer impact, in at least these two regions. Four
other regions were investigated but are inconclusive, not contradictory:
`westeurope`'s baseline returned only 3 active clusters, too thin to trust its
own "no match" targeted result; `centralindia` (4), `switzerlandnorth` (1),
and `australiaeast` (0) had baselines too thin or empty to justify running the
targeted check at all.

**Methodology note for future verification:** the `backendLogs` "dumping
resourceID" log line does not fire on a predictable daily cadence per
cluster — the same region can show a healthy baseline in a 48-hour window and
zero entries in a 24-hour window. Any future resolve-to-cluster check of this
kind should use at least a 48-hour window and should always establish a
baseline count before trusting an absence as "orphaned."

**Decision requested:** approve this SLO in principle, pending resolution of
the three gaps above, tracked as immediate follow-up work.

### Deferred: Availability and Errors (scoped follow-up)

Both require the same underlying fix, not a policy decision:

- **Availability** has no metric today that proves a refresh cycle completed
  successfully per vault/region. `/healthz` only proves process startup —
  confirmed by the component's own source comment (`internal/app/root.go`):
  *"Can add a more sophisticated health check in the future. This at least will
  confirm that the app got past initial configuration validation."* Worse, a
  controller can fail silently: confirmed in source
  (`internal/app/vault_reconciler.go`) that if the discovery, refresh, or purge
  controller's goroutine exits for any reason other than context cancellation,
  the error is logged and nothing else happens — the main process keeps
  running, `/healthz` keeps returning 200, and `/metrics` keeps serving, with no
  signal anywhere that a controller has stopped doing its job. Fix: emit a
  per-vault `credential_refresher_last_successful_cycle_timestamp_seconds` after
  a complete refresh pass succeeds.
- **Errors** cannot produce a valid ratio today because a single secret can
  emit more than one `outcome` value in the same pass — confirmed in source
  (`internal/refresher/refresher.go`): a successful rotation emits `rotation`
  and then unconditionally also emits `no-op` for the same secret, and a Key
  Vault write failure is logged but emits no `error` label at all, falling
  through to that same unconditional `no-op` observation (not merely "folds
  into no-op as a fallback" — it is never labeled `error`). Fix: emit exactly
  one terminal outcome (`no-op`, `rotation`, or `error`) per processed secret,
  and count Key Vault write failures as `error`.

Once both fixes ship, Availability becomes `last-successful-cycle age <
threshold` and Errors becomes `error / total` over the corrected outcome
counter. Recommend tracking this as a scoped Credential Refresher
instrumentation change, separate from this SLI/SLO ticket, with SLO targets
set after a production baseline is collected against the corrected metrics.

### Not pursued: Latency, Traffic, Saturation

**Latency.** The refresher is a scheduled background reconciler (three
independently configurable intervals — discovery, refresh, purge — sharing a
10-minute default, confirmed in `internal/app/root.go`), not a request/response
service. `credential_refresher_process_time_seconds` gives accurate p50/p95/p99
secret-processing time, but no customer-facing expectation exists for that
number in isolation — a slow individual pass only matters if it causes
credentials to miss their renewal window, which is what the expiry-safety SLI
already measures. Recommendation: keep the processing-time histogram as a
diagnostic panel for triaging Key Vault/MSI-RP slowness; do not set a latency
SLO for this component.

**Traffic.** Demand is bounded by the number of Key Vault secrets in a region
and runs on a fixed schedule; it isn't elastic customer-driven load. A drop in
processing rate is ambiguous between "no work due" and "the controller
stopped," and that ambiguity is exactly what the Availability fix above
resolves directly. Recommendation: no standalone Traffic SLO; the vault/secret
population count from the discovery controller remains available as a
diagnostic.

**Saturation.** Queue depth, reconcile latency, and discovery failures are
useful precursor signals, and the workload (vaults/secrets per region) is small
and bounded. No capacity threshold has been validated against production
queue-depth data — this has not yet been checked and should not be assumed
either way. Recommendation: no Saturation SLO until (a) queue-depth data is
reviewed and (b) the vault/secret population grows enough to make backlog a
plausible risk; keep the existing diagnostics and namespace-level pod/resource
alerts as the current safeguard.

## Metric Inventory and Authority

| Metric | What it records | Authority |
| --- | --- | --- |
| `credential_refresher_days_until_msi_credential_expiration_bucket` | Histogram of `(cannotRenewAfter - now) - 90 days` for each recognized MSI/UAMSI secret, recorded before a refresh decision. | Authoritative for the refresher's calculated expiry state. Not a unique-secret inventory, not proof a rotation completed, and not proof of customer authentication. Labeled by `vault_name` only — no secret ID or live-cluster identity. |
| `credential_refresher_process_time_seconds_{bucket,sum,count}` | Per-secret processing duration, labeled by `outcome`, `type`, `vault_name`. | Authoritative for processing time. `outcome` values are not mutually exclusive per secret (see Deferred: Errors above). |
| `credential_refresher_refresh_controller_queue_depth` | Current refresh-controller queue length. | Component saturation diagnostic. |
| `credential_refresher_refresh_controller_reconcile_latency_seconds` | Duration of one vault reconciliation. | Component latency diagnostic, not end-to-end rotation duration. |
| `credential_refresher_discovery_controller_failures_total` | Discovery failures by `failure_stage`. | Component availability diagnostic. |
| `credential_refresher_discovery_controller_desired_vaults` | Vaults currently desired by discovery. | Discovery diagnostic. Discovery is vault-level: confirmed in source (`internal/controller/discovery_controller.go`) as an Azure Resource Graph query for Key Vaults matching a configured tag — it has no knowledge of secrets or clusters inside a discovered vault. |
| `credential_refresher_purge_controller_queue_depth` | Current purge-controller queue length. | Component saturation diagnostic (confirmed in source, `internal/controller/purge_controller.go`). |
| `credential_refresher_purge_controller_reconcile_latency_seconds` | Duration of one vault purge pass. | Component latency diagnostic (confirmed in source, same file). |

Azure Managed Prometheus exposes a `prometheus_replica` label on these series.
Any query used for an SLO or alert must deduplicate with `max without
(prometheus_replica)(...)` before aggregating; the currently deployed alert
rules do not do this and must be corrected before their counts are trusted
(see Required Work).

## Dashboards and Alerting

Tracked separately — ARO-25957 (Dashboards) and ARO-25975 (Alerting). Both
move forward based on the SLI/SLO decisions here. What exists today, for
reference only:

- **Dashboard:** `observability/grafana-dashboards/msi-credential-refresher/msi-credential-refresher.json`
  ("Credential Refresher Dashboard"), on `Managed_Prometheus_services-<region>`
  datasources.
- **Alerts:** `observability/alerts/msi-credential-refresher-prometheusRule.yaml`
  defines three threshold alerts on the expiry histogram, evaluated over a
  30-minute lookback with a 5-minute hold:

| Alert | Window (days until expiry) |
| --- | --- |
| `ClusterCredentialExpiringSoon` | `0 < d <= 30` |
| `ClusterCredentialExpired` | `-90 < d <= 0` |
| `ClusterCredentialNotRenewable` | `d <= -90` |

These are threshold safeguards, not SLO burn-rate alerts, and they do not
currently deduplicate `prometheus_replica`. They should be corrected and kept
regardless of the outcome of the SLO discussion above — they are the fastest
path to detecting a customer-impacting expiry today.

## Required Work

**Immediate (unblocks the expiry-safety SLO):**

1. Verify which HCP service-region Azure Monitor Workspaces scrape the
   refresher ServiceMonitor. 10 production regions have been spot-checked (see
   Known Gap #3): `westeurope`, `brazilsouth`, `centralindia`,
   `switzerlandnorth`, `uksouth`, `australiaeast`, `eastus2`, `canadacentral`,
   `westus`, `eastus2euap`. 2 confirmed scrape gaps (`westus`, `eastus2euap`)
   need to be root-caused and fixed.
2. Add `max without (prometheus_replica)` deduplication to the existing alert
   expressions and to any recording rule built from this SLI.
3. **In the ARO-HCP repo, not the Credential-Refresher repo:** add a step to
   the cluster-deletion flow
   (`backend/pkg/controllers/cluster/deletion/`) that deletes the cluster's MSI
   Key Vault secret via `DeleteSecret`. The Credential Refresher's existing
   purge controller already purges soft-deleted secrets on a schedule — once
   ARO-HCP soft-deletes the secret on cluster teardown, no further
   component-side work is needed. Open question for whoever implements this:
   whether the secret name can be deterministically derived from the identity
   data already persisted on `ServiceProviderCluster.Status.MSIManagedIdentities`
   using the same naming prefix the refresher checks
   (`dataplane.ManagedIdentityCredentialsStoragePrefix` /
   `UserAssignedIdentityCredentialsStoragePrefix`), or whether a new persisted
   mapping is required — not yet confirmed.

**Follow-up (unblocks Availability and Errors, in the Credential-Refresher repo):**

1. Emit `credential_refresher_last_successful_cycle_timestamp_seconds` per
   vault.
2. Emit exactly one terminal `outcome` per processed secret; count Key Vault
   write failures as `error`.
3. Collect a production baseline on the corrected metrics before proposing
   targets.
4. Add recording rules, dashboard panels, and `promtool` tests once the above
   lands (tracked in ARO-25957 / ARO-25975).

## Open Decisions

1. Are we good with the credential-expiry-safety SLO, once the three gaps
   above are fixed?
2. Okay with deferring Availability and Errors until the instrumentation fix
   ships, instead of holding this up?
3. Okay with not pursuing Latency, Traffic, or Saturation as SLOs for this
   component, for the reasons above?
4. Follow-up work, split across two repos:
   - The orphaned-secret fix (`DeleteSecret` call on cluster deletion) — ARO-HCP
     repo (backend).
   - The Availability/Errors instrumentation fix (last-successful-cycle
     metric, single terminal outcome) — Credential-Refresher repo (ADO).
