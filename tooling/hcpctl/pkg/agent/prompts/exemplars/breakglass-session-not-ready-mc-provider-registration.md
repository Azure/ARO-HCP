# Test Failure Analysis: SRE should be able to log into a cluster via a breakglass session

## Root Cause

A breakglass e2e session timed out waiting to become `Ready` because the sessiongate management-cluster (MC) provider was not registered at the moment the `Session` was reconciled. The session-reconcile worker skips any `Session` whose MC provider is not yet registered and relies on the provider-(re)registration path to requeue it later. A *cold* re-registration — rebuilding the remote AKS client and informers and blocking on cache sync — did not complete within the `Session`'s short TTL. sessiongate deletes a `Session` at `creationTimestamp + TTL` regardless of readiness, so the `Session` was removed before it could ever reach `Ready`, and the test's readiness poll expired.

The identity was **not** at fault: a sibling breakglass session created with the *same* identity in the same run succeeded because it reused an already-warm provider. This is a provider-registration timing and observability gap in sessiongate, amplified by a test-side TTL/timeout that was shorter than a legitimate cold registration.

The operator's Kusto investigation of the failing run's sessiongate service-cluster logs corroborated this mechanism (the management cluster was not registered in time for a subsequent session sync); residual uncertainty about exact provider registration/deregistration timing in this run is captured in "What is Proven vs. Not Proven".

## Classification

- **Category:** Product Failures
- **Component:** Sessiongate (the service-cluster breakglass `Session` controller). Note: sessiongate is not one of the standard L2 components; treat it as a service-cluster control-plane timing gap. A too-tight test-side TTL/timeout is a contributing Test Reliability factor, but the dominant, actionable cause is the provider-registration behavior, so classify as Product Failures.
- **Confidence:** 0.7 — the mechanism (skip-while-unregistered plus a cold re-registration that outlasts the `Session` TTL) is well supported by code and logs; whether to call this an environmental timing blip or a product defect remains a judgment call.

## Debugging Lessons (read these first)

This failure class is hard to debug for three reasons. Internalize them before you start:

1. **sessiongate runs in the SERVICE cluster, not the management cluster.** Its pods reconcile `Session` CRs in the service cluster and talk to the management (AKS) cluster over a *remote* client. Its logs therefore live in the service cluster's `ServiceLogs.containerLogs` (filter `pod_name startswith 'sessiongate'`). They are **never** in the management-cluster artifact dump / snapshot. If you look only at mgmt-cluster artifacts you will see "session not ready" with no cause.
2. **The decisive skip is logged at `V(4)`** — invisible at the deployed verbosity — and sets **no** `Session` condition. So a `Session` stuck waiting on provider registration shows nothing in Kusto and nothing in its own `.status`. The `start sync` / `end sync` lines are logged at plain `Info` and *are* visible; the **absence of a completing sync** for a given session name is itself the signal.
3. **A `Session` is deleted at `creation + TTL` even if it never became `Ready`.** "The poll timed out" and "the `Session` disappeared" can be the same event. Always compare the `Session` TTL against the worst-case time-to-`Ready`.

## Summary

The test provisions an HCP cluster, then exercises three SRE breakglass sessions. The first two (longer TTL) succeed and are used to assert session expiry. The third — a negative/owner-isolation case that creates a short-TTL session for **another user** — timed out waiting to become `Ready`. In the failing run the third session (`breakglass-bcnzj`) never reached `Ready=True`: sessiongate began a sync for it but the MC provider was not registered, so the worker skipped it and returned, expecting the registration path to requeue it. The provider had been torn down (it is deregistered whenever a management cluster has zero sessions) and the subsequent cold re-registration — which blocks on informer cache sync (`MCProviderCacheSyncTimeout = 30s`) plus AKS REST-config fetch, client/informer start, and requeue — did not finish inside the session's 1-minute TTL. sessiongate then deleted the session at `creation + TTL`, and the test's ~1-minute readiness poll expired. A sibling session (`breakglass-gtcj2`) created with the **same** identity succeeded because it hit an already-warm provider — which rules the identity out as the cause.

## Key Timeline

| Time (UTC) | Event | Source |
|---|---|---|
| 2026-09-29 ~22:58 | Test creates the third breakglass session `breakglass-bcnzj` (TTL 1m, another-user/owner-isolation case) | e2e test (`test/e2e/admin_api.go`) |
| 22:58:00.651 | Sibling session `breakglass-gtcj2` (same identity) has its first sessiongate sync and registers | `ServiceLogs.containerLogs` (pod `sessiongate*`) |
| ~22:58–22:59 | sessiongate logs `start sync` for `breakglass-bcnzj` but no completing sync / no `Ready` — MC provider not registered (skip at `V(4)`, not visible) | `ServiceLogs.containerLogs` + `sessioncontroller.go` |
| ~22:59 | Session's 1-minute TTL elapses; sessiongate deletes it (deletion is independent of readiness) | `sessioncontroller.go` deletion path |
| ~22:59 | Test's readiness poll (~12 polls at 5s) times out: "Session is not ready" | e2e test |

## Causal Chain

### 0. Q: Why did this test fail?

**A:** The breakglass sub-test timed out waiting for its third session (created for another user) to become ready.

#### Proof 1 (log -- test error)

```
failed to create breakglass credentials for another user
timeout waiting for session to become ready (last status: Session is not ready)
```

### 1. Q: Why did the session never become `Ready`?

**A:** sessiongate started reconciling `breakglass-bcnzj` but never completed a sync that set `Ready`, because the management-cluster provider for its target MC was not registered at reconcile time. A sibling session on the same MC and same identity (`breakglass-gtcj2`) *did* register — so the input (identity/request) was valid; the difference was provider warmth.

#### Proof 1 (kusto -- sync started but never completed for the failing session)

```kql
cluster('<svc-kusto-cluster-uri>').database('ServiceLogs').table('containerLogs')
| where timestamp between (datetime(2026-09-29T22:58:14Z) .. datetime(2026-09-29T23:00:00Z))
| where pod_name startswith 'sessiongate'
| where log has 'breakglass-bcnzj' and log contains "start sync"
| order by timestamp asc
```

A `start sync` appears for `breakglass-bcnzj` with no corresponding completing sync that sets `Ready`. Compare against the sibling `breakglass-gtcj2`, which shows registration and a completing sync.

### 2. Q: Why was the sync skipped without setting any status?

**A:** When the MC provider is not registered, the worker logs a `V(4)` message and returns, relying on the registration path to requeue. `V(4)` is below the deployed log level, and the branch sets no `Session` condition, so the wait is invisible in both Kusto and the CR.

#### Proof 1 (code -- the silent skip)

`sessiongate/pkg/controller/sessioncontroller.go`:

```go
mc, ok := c.getManagementClusterProvider(session.Spec.ManagementCluster.ResourceID)
if !ok {
    logger.V(4).Info(
        "management cluster provider not yet registered, skipping session reconciliation as the registration process will requeue",
    )
    return true
}
```

By contrast, the sync entry/exit are logged at plain `Info` (hence visible in Kusto):

```go
logger.Info("start sync")
defer logger.Info("end sync")
```

### 3. Q: Why was the MC provider not registered in time?

**A:** The provider is deregistered whenever a management cluster has zero sessions, and rebuilt cold when the next session arrives. A cold registration blocks on informer cache sync (`MCProviderCacheSyncTimeout = 30s`) on top of the AKS REST-config fetch, client/informer start, and the requeue — long enough to miss a 1-minute window, especially right after a deregistration.

#### Proof 1 (code -- deregister-at-zero, cold rebuild)

`sessiongate/pkg/controller/sessioncontroller.go` (`reconcileManagementClusterProvider`):

```go
if len(sessions) == 0 {
    return c.unregisterMCProvider(mgmtClusterID)
} else {
    return c.registerMCProvider(ctx, mgmtClusterID, MCProviderCacheSyncTimeout)
}
```

`sessiongate/pkg/controller/constants.go`:

```go
// MCProviderCacheSyncTimeout is the maximum time to wait for management cluster
// informer caches to sync during provider registration
MCProviderCacheSyncTimeout = 30 * time.Second
```

#### Proof 2 (kusto -- trace the provider registration for the target MC)

Filter sessiongate logs by the management cluster's ARM resource ID to see when its provider (de)registered around the failure window:

```kql
cluster('<svc-kusto-cluster-uri>').database('ServiceLogs').table('containerLogs')
| where timestamp between (datetime(2026-09-29T22:58:14Z) .. datetime(2026-09-29T23:59:00Z))
| where pod_name startswith 'sessiongate'
| where log has "/subscriptions/<subscription-id>/resourceGroups/hcp-underlay-ci01-j6158336-mgmt-2/providers/Microsoft.ContainerService/managedClusters/ci01-j6158336-mgmt-2"
| order by timestamp asc
```

### 4. Q: Why was there no recovery — why did the session fail permanently?

**A:** sessiongate deletes a `Session` once `now > creationTimestamp + TTL`, regardless of whether it ever became ready. With a 1-minute TTL, the session was deleted before the cold registration could complete and requeue it, so there was nothing left to become ready. The test's own ~1-minute readiness poll expired in the same window.

#### Proof 1 (code -- deletion at creation+TTL, independent of readiness)

`sessiongate/pkg/controller/sessioncontroller.go`:

```go
expiresAt := metav1.NewTime(session.CreationTimestamp.Add(session.Spec.TTL.Duration))
if c.clock.Now().After(expiresAt.Time) {
    // ...delete the session...
}
```

### 5. Q: Why did a sibling session with the same identity succeed?

**A:** `breakglass-gtcj2` was reconciled while the MC provider was already warm, so it registered and reached `Ready` immediately. Same identity, different provider state — which refutes any "bad/negative identity" explanation and localizes the fault to provider-registration timing.

## What is Proven vs. Not Proven

### Proven

- The failing session never reached `Ready`; sessiongate logged `start sync` for it without a completing/ready sync.
- A sibling session with the **same** identity registered and succeeded (identity is not the cause).
- The skip path is logged at `V(4)` (hidden) and sets no `Session` condition.
- sessiongate runs in the service cluster and reaches the MC over a remote client; its logs are in `ServiceLogs.containerLogs`, not mgmt-cluster artifacts.
- The provider is deregistered at zero sessions and re-registered cold with a 30s cache-sync bound (`MCProviderCacheSyncTimeout`).
- A `Session` is deleted at `creation + TTL` regardless of readiness.

### Not Proven (without raw `V(4)` provider logs / high-resolution timing)

- The exact millisecond the provider deregistered relative to the failing session's reconcile.
- Whether it was a single cold start or repeated skips across several requeues.
- The precise cache-sync duration in this specific run.

## Suggestions

### Reusable Kusto recipes

Trace a specific session's reconcile history (service cluster):

```kql
cluster('<svc-kusto-cluster-uri>').database('ServiceLogs').table('containerLogs')
| where timestamp between (datetime(<start>) .. datetime(<end>))
| where pod_name startswith 'sessiongate'
| where log has '<session-name>'
| order by timestamp asc
```

Trace management-cluster provider (de)registration by ARM resource ID:

```kql
cluster('<svc-kusto-cluster-uri>').database('ServiceLogs').table('containerLogs')
| where timestamp between (datetime(<start>) .. datetime(<end>))
| where pod_name startswith 'sessiongate'
| where log has '<management-cluster-arm-resource-id>'
| order by timestamp asc
```

### Fixes

- **Test-side (fastest):** the `Session` TTL, not the poll timeout, is the binding cap — a session is deleted at `creation + TTL` regardless of readiness. Raise the short-TTL negative-path session's TTL (and the shared readiness poll timeout) to comfortably exceed a worst-case cold registration. Bumping only the poll timeout cannot help a session that gets deleted first.
- **Product-side (observability):** raise the "provider not yet registered, skipping" log from `V(4)` to `Info` and/or set a `Session` `NotReady` condition (reason e.g. `ManagementClusterProviderNotRegistered`) so the wait is visible in Kusto and in the CR. Log the `Session` status/conditions each sync so the service-cluster `Session` state is captured.
- **Product-side (robustness):** add a self-requeue safety net (e.g. `workqueue.AddAfter`) in the skip branch so a slow or failed registration cannot strand a session; and consider a deregistration grace period so a quickly-following session reuses a warm provider instead of paying the cold cache-sync cost.
