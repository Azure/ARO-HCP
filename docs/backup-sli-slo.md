# HCP Backup and Restore: SLI and SLO Definitions

> **Status:** Draft — needs alignment with Jerome, Simon, Steve, Gerd, David  
> **Jira:** [ARO-29948](https://redhat.atlassian.net/browse/ARO-29948)  
> **Related:**  
> - [Architecture PR #79](https://github.com/openshift-online/architecture/pull/79) — ARO HCP Alerting Recommendation for SRE (Simon)  
> - [ROSA OADP Observability](https://gitlab.cee.redhat.com/hcm-service-lifecycle/rosa/-/blob/main/docs/services/oadp.md#observability)  
> - [PR #6664](https://github.com/Azure/ARO-HCP/pull/6664) — Prior Velero backup monitoring alerts (adalrsjr1, closed)  
> - [PR #7371](https://github.com/Azure/ARO-HCP/pull/7371) — Backup SLO alerts implementation (pending SLI/SLO alignment)

## 1. Scope

This document defines the **Service Level Indicators (SLIs)** and **Service Level Objectives (SLOs)** for the ARO HCP backup and restore user journey. The backup system uses [Velero](https://velero.io/) on management clusters to perform automated backups of Hosted Control Plane (HCP) resources — see [docs/backups.md](backups.md) for the full architecture.

### What is covered

- Velero-managed backup schedules for all HostedClusters on each management cluster
- Three production cadences: **6-hourly** (`0 */6 * * *`, 48h TTL), **daily** (`0 2 * * *`, 30d TTL), **weekly** (`0 3 * * 0`, 90d TTL)
- Backup storage (Azure Blob Storage via CSI data-movement)
- Backup pipeline health (scheduling → snapshot → upload → completion)

### What is NOT covered (yet)

- **Restore operations** — no restore activity occurs in production today; SLIs will be added once restore workflows are exercised and instrumented
- **Testing cadence** (`10min`) — CI/dev only, not production SLO-tracked
- **Customer-facing SLA derivation** — these are internal SLOs for the ARO HCP team, not external SLAs

## 2. Service Level Indicators (SLIs)

Each SLI follows the [ARO HCP Alerting Recommendation ADR](https://github.com/openshift-online/architecture/pull/79) framework, using the four golden signals (Availability, Errors, Latency, Traffic) plus domain-specific signals (Storage Availability, Saturation).

### 2.1 Backup Availability (Freshness)

| Property | Value |
|----------|-------|
| **Definition** | Time elapsed since the last successful backup for each HostedCluster namespace, per cadence |
| **Metric** | `time() - max by (cluster, hosted_cluster_namespace) (velero_backup_last_successful_timestamp{schedule=~".+-6-hourly"})` |
| **Scope** | Per HostedCluster namespace, per management cluster |
| **Unit** | Seconds |

**Why per-namespace (not fleet-aggregated):** Freshness is inherently a per-HostedCluster question. Averaging across a management cluster would hide a single stuck HC behind every other healthy one on the same MC. The `hosted_cluster_namespace` is derived from the schedule name (`{namespace}-{cadence}`) via `label_replace`, matching the backup controller's naming convention in `schedule.go`.

**Why NOT a classic MWMBR dual-window check:** Lag (seconds since last success) is monotonic between successes — both a long window and short window always read the same current lag value. Unlike error rates and latency (real ratios/rates that fluctuate and benefit from two-sample confirmation), staleness only ever goes up until the next success resets it to ~0. Thresholds + `for:` duration provide the tiering instead.

### 2.2 Backup Error Rate

| Property | Value |
|----------|-------|
| **Definition** | Fraction of completed backup outcomes that resulted in failure |
| **Numerator** | `failure_total + partial_failure_total + validation_failure_total` |
| **Denominator** | `success_total + failure_total + partial_failure_total + validation_failure_total` |
| **Metric** | `errors:backup:ratio:rate5m` (recording rule) |
| **Scope** | Fleet-wide per management cluster |
| **Unit** | Ratio [0, 1] |

**Why outcome-based, not attempt-based denominator:** An attempt and its eventual outcome can land in different evaluation windows (e.g. a failure completing after the 10m pre-hook timeout). Using `attempt_total` as the denominator can transiently divide by zero, making the short-window ratio evaluate to +Inf and falsely confirming a fast-burn tier. Keying both numerator and denominator off completed outcomes removes this timing mismatch.

### 2.3 Backup Latency

| Property | Value |
|----------|-------|
| **Definition** | p99 backup duration from start to completion |
| **Metric** | `histogram_quantile(0.99, sum by (cluster, le) (rate(velero_backup_duration_seconds_bucket[window])))` |
| **Scope** | Fleet-wide per management cluster |
| **Unit** | Seconds |

**Note:** `velero_backup_duration_seconds` only populates on completion (success or failure), so this reflects completed-backup duration, not stuck/in-progress backups (those are covered by the Saturation SLI).

### 2.4 Backup Traffic

| Property | Value |
|----------|-------|
| **Definition** | Backup attempt rate |
| **Metric** | `traffic:backup:attempt_rate:rate5m` (recording rule) |
| **Scope** | Fleet-wide per management cluster |
| **Unit** | Attempts per second |

**Burst behavior:** Every HC's 6-hourly schedule uses the same cron (`0 */6 * * *`), so attempts burst around each 6h mark. The attempt rate is legitimately ~0 between bursts — this is expected, not a failure.

### 2.5 Backup Storage Availability ⚠️ PROXY

| Property | Value |
|----------|-------|
| **Definition** | Fraction of 6-hourly schedules whose most recent run did NOT succeed |
| **Metric** | `sli:backup:last_run_failure:ratio5m` (recording rule) |
| **Scope** | Fleet-wide per management cluster |
| **Unit** | Ratio [0, 1] |

**Why proxy:** No direct `BackupStorageLocation` phase metric exists yet. A real BSL outage fails uploads for every schedule near-simultaneously, which this approximates. Also fires for other fleet-wide causes (e.g. a bad Velero image rollout). **Needs [ARO-29949](https://redhat.atlassian.net/browse/ARO-29949)** (kube-state-metrics for velero.io CRs) to become a precise indicator.

**Why 6-hourly only:** Daily/weekly schedules' "last run" status only updates on their own cron tick, so they still show pre-outage success for up to a day/week after an outage starts. Mixing cadences dilutes the signal below detection thresholds.

### 2.6 Backup Saturation ⚠️ PROXY

| Property | Value |
|----------|-------|
| **Definition** | Count of backups currently in a non-terminal phase |
| **Metric** | `backup:in_progress:count` (recording rule) |
| **Scope** | Per management cluster |
| **Unit** | Count |
| **Non-terminal phases** | New, Queued, ReadyToStart, InProgress, WaitingForPluginOperations, WaitingForPluginOperationsPartiallyFailed, Finalizing, FinalizingPartiallyFailed |

**Why proxy:** No direct metric for Velero's BackupRepository/node-agent (kopia) repo-maintenance queue depth. [ARO-29757](https://redhat.atlassian.net/browse/ARO-29757) showed repo contention surfaces as a pile-up of non-completing backups. **Needs [ARO-29949](https://redhat.atlassian.net/browse/ARO-29949)** to become a direct repo-maintenance metric.

### 2.7 Backup Metrics Canary

| Property | Value |
|----------|-------|
| **Definition** | Whether any `velero_backup_attempt_total` samples are being reported by each management cluster |
| **Metric** | `underlay_clusters{cluster=~".*-mgmt-.*"} unless on (cluster) group by (cluster) (velero_backup_attempt_total)` |
| **Scope** | Per management cluster |

**Why needed:** Every other SLI is a `velero_backup_*` PromQL expression, which is silently vacuous if Velero stops reporting metrics. This meta-SLI detects the metric pipeline itself failing.

## 3. Service Level Objectives (SLOs)

### 3.1 Backup Availability SLO

**Objective:** Every HostedCluster's 6-hourly backup must have succeeded within its cadence window, with escalating thresholds for longer gaps.

| Tier | Threshold | `for:` | Meaning |
|------|-----------|--------|---------|
| Fast (1h/5m) | Last success > **12 hours** ago | 2m | Missed more than one full 6-hourly cycle |
| Medium (6h/30m) | Last success > **24 hours** ago | 15m | A full day without a successful backup |
| Slow (3d/6h) | Last success > **3 days** ago | 1h | Multiple days without a successful backup; daily/weekly tiers also at risk |

**Rationale:** The 12h threshold for the fast tier means the backup missed more than one full 6-hourly cadence cycle — the first miss at 6h could be transient (restart, temporary storage issue), but a second consecutive miss at 12h is a real problem.

### 3.2 Backup Error Rate SLO

**Objective:** 99.5% backup success rate (0.5% error budget)

| Parameter | Value |
|-----------|-------|
| **SLO Target** | 99.5% success |
| **Error Budget** | 0.5% of completed attempts may fail |
| **Budget window** | 30 days |

**Burn rate tiers (MWMBR):**

| Tier | Burn Rate | Threshold | Long Window | Short Window | `for:` | Volume Guard |
|------|-----------|-----------|-------------|--------------|--------|--------------|
| Fast | 14.4× | >7.2% failure | 1h | 5m | 2m | ≥3 attempts in 1h |
| Medium | 6× | >3.0% failure | 6h | 30m | 15m | ≥5 attempts in 6h |
| Slow | 1× | >0.5% failure | 3d | 6h | 1h | ≥10 attempts in 3d |

**Rationale:** Looser than Frontend's 99.95% because a single failed backup attempt is recovered by the next cadence tick without immediate customer-visible impact. The volume guards prevent tiny-denominator false positives on low-traffic management clusters.

**30-day error budget math:** With 6-hourly cadence = ~120 attempts per HC per month. 0.5% budget = 0.6 failed attempts per HC per month. Across a management cluster with N HCs: N × 120 total attempts, N × 0.6 allowed failures.

### 3.3 Backup Latency SLO

**Objective:** p99 backup duration < 20 minutes

| Parameter | Value |
|-----------|-------|
| **SLO Target** | p99 < 1200 seconds (20 minutes) |
| **Tiers** | Medium (6h/30m) and Slow (3d/6h) only — no fast tier |

**Rationale:** `CSISnapshotTimeout` is 10 minutes and `ItemOperationTimeout` is 15 minutes (see `internal/backup/backup.go`). A healthy backup should complete well inside 20 minutes. No fast (1h/5m) tier because backup latency can spike briefly during volume-heavy snapshots without indicating a sustained problem.

### 3.4 Backup Traffic SLO

**Objective:** No sustained collapse in backup attempt rate

| Parameter | Value |
|-----------|-------|
| **Detection** | 2h average rate < 10% of 1-day baseline |
| **`for:`** | 1h |
| **Guard** | 1d baseline > 0 (cluster must have been running) |

**Rationale:** Detects global/per-cluster backup-schedule pauses, stuck Backup Schedule Controller, or broken kube-applier delivery — NOT individual HC problems.

### 3.5 Storage Availability SLO ⚠️ PROXY

| Parameter | Value |
|-----------|-------|
| **Detection** | >80% of 6-hourly schedules fleet-wide had a failed last run |
| **`for:`** | 15m |
| **Guard** | ≥3 distinct HostedClusters on the MC |

**Rationale:** A BSL outage fails uploads for every HC simultaneously. The 80% threshold + 3-HC minimum avoids flagging a single bad HC as a fleet-wide issue. Proxy until [ARO-29949](https://redhat.atlassian.net/browse/ARO-29949) provides direct BSL metrics.

### 3.6 Saturation SLO ⚠️ PROXY

| Parameter | Value |
|-----------|-------|
| **Detection** | >10 non-terminal backups |
| **`for:`** | 3h 30m |

**Rationale:** With `--concurrent-backups 4`, a normal 6-hourly cron burst can legitimately queue ~40 backups. At 4 concurrent × 20m p99 = ~200 minutes to drain = ~3h 20m. The threshold and duration tolerate normal burst queuing while catching genuine stuck pile-ups (per [ARO-29757](https://redhat.atlassian.net/browse/ARO-29757)) before the next 6-hourly cycle. Proxy until [ARO-29949](https://redhat.atlassian.net/browse/ARO-29949).

### 3.7 Metrics Canary

| Parameter | Value |
|-----------|-------|
| **Detection** | Management cluster in underlay inventory but zero `velero_backup_attempt_total` samples |
| **`for:`** | 15m |

## 4. Severity

All backup SLO alerts are currently **Severity 4** (IcM, no immediate paging) per the [CEN mapping](https://eng.ms/docs/cloud-ai-platform/azure-core/one-fleet-platform-services/one-fleet-docs-and-references/incident-management/icm-getting-started/cen-standard#severity-mapping). There is no current customer-facing impact from a backup gap on its own — backups are a DR preparedness signal, not a runtime availability signal.

**Open question:** Should severity be elevated once formal RPO targets exist? If a backup gap directly violates a committed RPO, Sev 3 (prompt investigation) may be warranted for the Availability tiers.

## 5. Known Gaps

| Gap | Impact | Resolution |
|-----|--------|------------|
| No direct BSL-phase metric | Storage Availability SLI is a proxy (last-run-failure ratio) | [ARO-29949](https://redhat.atlassian.net/browse/ARO-29949) — kube-state-metrics for velero.io CRs |
| No direct repo-maintenance metric | Saturation SLI is a proxy (in-progress count) | [ARO-29949](https://redhat.atlassian.net/browse/ARO-29949) |
| No "is schedule paused" metric | Availability SLI cannot distinguish paused from stuck | [ARO-29949](https://redhat.atlassian.net/browse/ARO-29949) |
| No restore SLIs | Restore success/duration not tracked | No restore activity in production; add once exercised |
| Error budget tracking not automated | No burn-rate dashboard or automated budget exhaustion alerts | Future work |

## 6. Open Questions for Team Alignment

1. **RPO target:** Is there a formal Recovery Point Objective beyond "within the configured cadence" (6h for 6-hourly, 24h for daily, 7d for weekly)? If so, how does it map to the Availability SLO thresholds?

2. **Error rate target justification:** Is 99.5% the right success rate? The choice is based on "a single failure is recovered by the next tick" — but should we be stricter (e.g. 99.9%) if multiple consecutive failures have higher customer risk?

3. **Restore SLIs:** Tim suggested (in [PR #6664](https://github.com/Azure/ARO-HCP/pull/6664) feedback): *"Maybe have alerts for restore success < 90%"*. Should we define restore SLIs now (even without production data) or wait until restore workflows are exercised?

4. **Backup duration thresholds:** Tim also suggested in PR #6664: *"AVG backup duration > 1h (Sev 4), MAX backup duration >= 4h / +inf (Sev 4)"*. Our current SLO uses p99 < 20m. Should we additionally track average and max duration, or is p99 sufficient?

5. **Severity escalation:** Under what conditions should backup alerts escalate from Sev 4 to Sev 3? E.g. if the Availability slow tier (3d gap) is breached?

6. **ROSA alignment:** How closely should our SLIs/SLOs align with [ROSA's OADP observability](https://gitlab.cee.redhat.com/hcm-service-lifecycle/rosa/-/blob/main/docs/services/oadp.md#observability)? What can we reuse?

## 7. References

- [docs/backups.md](backups.md) — HCP Backup architecture and operational procedures
- [Architecture PR #79](https://github.com/openshift-online/architecture/pull/79) — ARO HCP Alerting Recommendation ADR (Simon)
- [ROSA OADP Observability](https://gitlab.cee.redhat.com/hcm-service-lifecycle/rosa/-/blob/main/docs/services/oadp.md#observability)
- [Google SRE Book — Chapter 5: Service Level Objectives](https://sre.google/sre-book/service-level-objectives/)
- [Google SRE Workbook — Chapter 5: Alerting on SLOs](https://sre.google/workbook/alerting-on-slos/)
- [PR #7371](https://github.com/Azure/ARO-HCP/pull/7371) — Backup SLO alerts implementation
- [PR #6664](https://github.com/Azure/ARO-HCP/pull/6664) — Prior Velero alerts (adalrsjr1, closed)
