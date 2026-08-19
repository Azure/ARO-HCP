# ARO-28187: Alert Blast-Radius Categorization

Status: **provisional** — policies and thresholds below are starting values
pending discussion at the ARO HCP Architecture Office Hours. This document is
the artifact David Eads asked for when he put PR #6581 on hold: "break apart
into categories as indicated and attend the ARO HCP Architecture Office
Hours" before any of this merges as final.

## Background

[ARO-28187](https://redhat.atlassian.net/browse/ARO-28187) tracks a class of
CI failures where generic Kubernetes/Prometheus alerts fire during dev e2e
provisioning and are converted into JUnit failures by the
`gather-observability` tool (`test/cmd/aro-hcp-tests/gather-observability/`),
blocking the merge queue. Nine alert types were originally observed across
12–14 of 614 runs (2026-06-30 to 2026-07-07):

| Alert | Workspace | Runs affected (original window) |
|---|---|---|
| KubeDeploymentReplicasMismatch | hcp | 14 (2.3%) |
| KubeDaemonSetRolloutStuck | svc | 13 (2.1%) |
| KubeNodeNotReady | svc | 13 (2.1%) |
| KubeNodeUnreachable | svc | 13 (2.1%) |
| KubeDaemonSetMisScheduled | svc | 12 (2.0%) |
| KubePodNotReady | svc | 12 (2.0%) |
| KubeStatefulSetReplicasMismatch | hcp | 9 (1.5%) |
| KubePodNotReady | hcp | 7 (1.1%) |
| KustoLogsDataStale | svc | 7 (1.1%) |

**These have since gotten much worse.** As of the last ticket update, all 9
alert types are still firing and have worsened significantly: 530+ DEV runs
affected since 2026-07-07, DEV failure rate ~70%, and `KubePodNotReady [hcp]`
alone spiking from 7 runs in the original window to 181 in a single week
(30.8% of DEV runs). The AROSLSRE-1395 fix (a CS topology revert) did **not**
resolve these alerts — they persisted through it and are now at 3–26x the
original rates, suggesting the root cause is distinct from the original CS
topology regression. This reinforces David's point: suppressing the alerts
would have hidden a live, worsening problem rather than surfacing it.

## The rejected approach

[PR #6581](https://github.com/Azure/ARO-HCP/pull/6581) proposed adding these
9 alerts to `known-issues/knownIssues.yaml` as blanket, unscoped entries so
they'd be classified **skipped** instead of **failed**. David Eads' review:

> At a glance, this looks like the opposite of our intended direction. We
> made alerting fail in CI clusters to improve our service and our alerting,
> not so we could build a skip list. Instead of this, how about digging into
> these failures and starting to categorize them like
>
> 1. failing pods cause customer visible region outage - frontend,
>    ingress/services?, stuff in kube-system?
> 2. failing pods cause non-customer visible region outage - backend,
>    cluster-service, fleet scaling, etc
> 3. failing pods cause management cluster outage - kube-applier, hypershift
>    operation
> 4. failing pods cause customer visible cluster outage - pods in their
>    controlplane namespace
> 5. failing pods cause non-customer visible cluster outage - pods in the
>    hostedcluster namespace
> 6. catch-all treated as 1a until reassigned.
>
> And then we discuss reasonable alerting thresholds for each category.

## The new mechanism

`gather-observability` now classifies every alert firing into a **blast-radius
category** (`test/cmd/aro-hcp-tests/gather-observability/alert-categories/categories.yaml`,
parsed/matched by `categories.go`) instead of a binary known/unknown list.
Each category carries a CI **policy**:

- `fail` — any firing fails the owning alert-rule's JUnit test case.
- `fail-over-threshold` — fails only once the category's firings for that
  rule reach a configured `minFirings` and/or `minDurationSeconds`.
- `warn` — never fails CI; firings are still recorded (as a non-failing/skip
  annotation) so they stay visible instead of disappearing into a skip list.
- `ignore` — never fails CI and isn't called out as needing attention. This
  is the successor to the old known-issues skip list, reserved for
  genuinely understood transient noise unrelated to blast radius (e.g.
  short-lived pods that outlive Prometheus's lookback delta) — **not** for
  the ARO-28187 cluster-degradation alerts themselves.

A single alert rule can span multiple categories within one workspace (e.g.
`KubePodNotReady [hcp]` firing for both a controlplane-namespace pod and a
hostedcluster-namespace pod in the same run); each category is evaluated
independently and the rule's test case fails if any of them calls for it.
Unclassified firings (no category rule matches) fail closed as tier 1,
matching David's "catch-all treated as 1a until reassigned."

## Alert-to-tier mapping

| Alert | Tier | Category | Default policy | Rationale |
|---|---|---|---|---|
| (frontend pods) | 1 | customer-visible-region-outage | fail | Frontend is the ARM API; any failure is customer-visible region-wide. |
| (ingress, kube-system on svc) | 1 | customer-visible-region-outage | fail | Same. |
| KustoLogsDataStale [svc] | 2 | non-customer-visible-region-outage | fail | Observability-pipeline lag, degrades the service but isn't customer-facing. **Open question**: may belong in `expected-noise` instead — see below. |
| KubeNodeNotReady/Unreachable [svc] | 2 | non-customer-visible-region-outage | fail | Node-level alerts can't be namespace-scoped; svc-cluster infra degradation is regional but not directly customer-visible. |
| (backend, cluster-service, maestro, fleet pods) | 2 | non-customer-visible-region-outage | fail | Regional service components. |
| KubeNodeNotReady/Unreachable [hcp] | 3 | management-cluster-outage | fail-over-threshold (≥3 firings or ≥15m) | A degraded management-cluster node threatens every HCP on it, but a single transient blip shouldn't hard-fail CI. |
| (kube-applier, hypershift operator, maestro-agent, mgmt-agent pods) | 3 | management-cluster-outage | fail-over-threshold (≥3 firings or ≥15m) | Same reasoning. |
| KubeDeploymentReplicasMismatch [hcp] | 4 | customer-visible-cluster-outage | fail | Namespace-dependent: fails if the deployment lives in a controlplane namespace (the common case for HCP-owned control-plane components). |
| KubeStatefulSetReplicasMismatch [hcp] | 4 | customer-visible-cluster-outage | fail | Same. |
| KubePodNotReady [hcp] (controlplane namespace) | 4 | customer-visible-cluster-outage | fail | kube-apiserver, etcd, KCM, ignition-server, konnectivity, etc. Directly customer-visible for that one hosted cluster. This is the primary driver of the worsening trend (331 runs). |
| KubeDaemonSetRolloutStuck [svc], KubeDaemonSetMisScheduled [svc] | 2 | non-customer-visible-region-outage | fail | DaemonSets on the svc cluster are regional infra, not customer-facing. |
| KubePodNotReady [svc] | 1 or 2 | depends on namespace | fail | Namespace-dependent: `aro-hcp`+frontend pod → tier 1; other svc namespaces → tier 2. |
| KubePodNotReady [hcp] (hostedcluster namespace) | 5 | non-customer-visible-cluster-outage | **warn** | Management-side HostedCluster/NodePool glue, not the guest control plane. Blast radius is one hosted cluster and not directly customer-visible. |

The exact matching rules (regexes on namespace/pod labels per component) are
in `categories.yaml`; see that file's comments for the full namespace
derivation (frontend/backend share the `aro-hcp` namespace, hosted control
plane namespaces follow Cluster Service's `ocm-<env>-<clusterID>[-<domain>]`
convention, etc.).

## Default policy summary

| Tier | Category | Default policy |
|---|---|---|
| 1 | customer-visible-region-outage | fail (any firing) |
| 2 | non-customer-visible-region-outage | fail (any firing) |
| 3 | management-cluster-outage | fail-over-threshold (≥3 firings or ≥15m) |
| 4 | customer-visible-cluster-outage | fail (any firing) |
| 5 | non-customer-visible-cluster-outage | warn (recorded, non-blocking) |
| 6 | catch-all | fail (treated as tier 1) |
| 0 | expected-noise (migrated known-issues) | ignore |

## Open questions for office hours

1. **Frontend/backend namespace collision.** Frontend and backend both
   deploy to the `aro-hcp` namespace on the svc cluster; the category rule
   splits them by pod-name prefix (`aro-hcp-frontend-.*` → tier 1, else tier
   2). Please confirm the frontend Deployment's actual pod-name prefix in
   production.
2. **Node-level alerts have no namespace.** `KubeNodeNotReady`/
   `KubeNodeUnreachable` carry a `node` label, not `namespace`, so they can't
   be bucketed by component. Defaulted to tier 2 (svc) / tier 3 (hcp) by
   workspace. Confirm this is the intended tier, or whether node health
   deserves its own category.
3. **KustoLogsDataStale.** Defaulted to tier 2 (fail). This is observability
   pipeline lag rather than a service outage — should it instead be
   `expected-noise` (ignore), since a stale Kusto export doesn't indicate a
   real customer or service problem?
4. **kube-system tier split.** `kube-system` is tier 1 on the svc cluster but
   tier 3 on the mgmt cluster (disambiguated by workspace). Confirm this
   matches intent — a kube-system failure on the svc cluster can affect
   networking/DNS for every customer in the region, while on the mgmt
   cluster it threatens that management cluster's HCPs.
5. **Tier 3 threshold values.** `minFirings: 3` / `minDurationSeconds: 900`
   (15m) for management-cluster-outage are placeholder guesses. What
   thresholds reflect a real "management cluster is unhealthy" signal versus
   a single transient blip?
6. **Tier 5 policy (warn vs ignore vs fail).** `warn` was chosen as a middle
   ground — visible without blocking merges — but if hostedcluster-namespace
   pod failures are truly inconsequential, `ignore` might be more
   appropriate; if they still merit occasional attention, a
   `fail-over-threshold` might fit better than either extreme.
7. **Catch-all coverage.** Any firing in the `infra` workspace (metric-based
   alerts not scoped to svc/hcp Prometheus, e.g. Cosmos/Kusto resource
   alerts) currently falls to the tier-6 catch-all (fail, per David's "1a
   until reassigned"). Should `infra` get its own default category instead?

## Where this lives

- Category config: `test/cmd/aro-hcp-tests/gather-observability/alert-categories/categories.yaml`
- Matching/classification: `test/cmd/aro-hcp-tests/gather-observability/categories.go`
- CI policy evaluation (fail/warn/ignore/fail-over-threshold → JUnit
  pass/skip/fail): `test/cmd/aro-hcp-tests/gather-observability/junit.go`
- Original rejected approach: [PR #6581](https://github.com/Azure/ARO-HCP/pull/6581)
