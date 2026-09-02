# Worker NodePool Controller

The nodepool controller replaces static, hand-defined management-cluster agent
pools with a controller that continuously converges a cluster's AKS agent pools
toward a computed desired state. It queries Azure for live quota and SKU
metadata, computes a zone-balanced set of pools for the cluster's profile, and
moves reality toward that plan **one safe step at a time, with no forced
eviction**.

The same planning code (`fleet/pkg/compute`) is used by the
[`aks-cluster-create`](../../../dev-infrastructure/scripts/aks-cluster-create)
tool at cluster-creation time, so a freshly provisioned cluster and a
controller-reconciled one arrive at the identical pool set.

## Concepts

### Profile

A **profile** is a named bundle of tiers plus the budget strategy used to size
worker pools. It is selected per environment via the `fleet.nodePoolProvisioning.profile`
config value (`ci`, `development`, `integration`, `production`). Profiles are
defined in code (`pkg/compute/tiers.go`, `LookupProfile` /
`ValidProfileNames`); config only chooses which one applies.

An empty profile disables the controller for that cluster.

### Tier

A **tier** (`TierConfig`) describes one desired VM size class:

| Field | Meaning |
|-------|---------|
| `Role` | `system`, `infra`, or `worker` — kept apart by taints; capacity is not fungible across roles |
| `PoolMode` | `PerZone` (one pool per AZ, capped by `PoolCount`) or `SpanZones` (single pool, AKS places nodes) |
| `Cores` | Desired vCPUs per node; the planner picks a concrete SKU meeting this |
| `FamilyPriority` | Ordered VM family fallback list (newest → oldest generation) |
| `MaxNodes` | Autoscaler ceiling per pool |
| `Labels` / `Taints` | Applied to nodes; the controller owns these and corrects drift |
| `EnableSwift` | Swift v2 multi-tenant NIC support (worker pools) |
| `Required` | Allocation failure for a required tier is surfaced loudly |

Tiers are processed **in order**: tier 0 draws from the shared per-family quota
budget first, tier 1 from the remainder, and so on.

### Budget strategy

Worker pool sizing is bounded by a per-VM-family vCPU **budget**:

- `SubscriptionQuotaBudget` — real budget = quota limit − live usage (production/integration).
- `UnlimitedBudget` — unconstrained, for environments where quota is externally guaranteed (CI/dev).

## Reconcile pipeline

Each `SyncOnce` runs four phases for a single management cluster.

1. **Gather** — list live AKS agent pools; fetch per-family quota usage and
   (cached) SKU metadata. Only pools carrying the `aro-hcp.azure.com/role` label
   are managed. Non-worker pools are counted at `maxCount` (not running count) to
   reserve autoscaler headroom.
2. **Resolve** (`compute.ResolveDesiredPools`) — a pure function
   `(profile, budgets, SKU metadata, zones) → []Pool`. Deterministic, no I/O.
   Walks the family priority list, distributing family budget across zones until
   a zone is full or the family is exhausted, then falls through to the next
   family.
3. **Diff** (`findNextAction`) — compares desired vs. current and returns **exactly
   one** next action (or `nil` when converged).
4. **Act** — executes that one action against the AKS ARM API, then requeues the
   key after a delay sized to how long AKS needs to settle that change.

### Invariants

- **One action per reconcile.** Progress is deliberate and observable.
- **One in-flight operation.** If any pool is transitioning, the only action is
  `wait`.
- **ETag / If-Match on every mutation.** Optimistic concurrency; stale state loses.
- **Grow before shrink.** New/target capacity is added before undesired capacity
  is removed, so the scaling ceiling is never underserved (briefly exceeding it
  is fine).
- **Ceiling floor.** A shrink is refused if it would drop the per-`(role,family)`
  vCPU ceiling (or the global Swift NIC ceiling) below the desired total.
- **Zero forced eviction.** Nodes leave only via AKS graceful cordon + drain.

## Action model

`findNextAction` evaluates these in priority order and returns the first match:

| # | Action | When | Effect |
|---|--------|------|--------|
| 0 | `wait` | any pool mid-transition | no-op; requeue |
| 1 | `reconcile` | a desired pool is `Failed` | re-PUT empty props to re-run AKS provisioning |
| 2 | `unfreeze` / `setMaxCount` / `freeze` / `reduce` | a **desired** pool is misconfigured | correct it toward spec |
| 3 | `updateConfig` | labels/taints drifted on a matched pool | full-replace to desired set |
| 4 | `create` / `setMaxCount` (grow) | a desired pool is missing or under target | add capacity, bounded by family headroom |
| 5 | squeeze / `freeze` / `reduce` / `delete` (shrink) | an **undesired** pool exists | drain and remove it |

### Drain lifecycle

An undesired pool cannot simply be deleted — its nodes may run workloads and its
autoscaler would fight any scale-down. Removal is a multi-reconcile lifecycle:

1. **Squeeze** — if the pool still autoscales and `maxCount > count`, drop
   `maxCount` to `count`. This releases reserved-but-unused ceiling **without
   evicting any node**. (Deliberately not floor-gated: it removes no running
   capacity, and gating it would deadlock a zero-slack same-family replace.)
2. **Freeze** — disable autoscaling, pinning the pool at its current `count`.
   The autoscaler can no longer fight back, and the count becomes a stable base
   for draining. Freeze is also used on a *desired* pool whose `count` sits above
   a lowered target, so it can be drained down.
3. **Drain** (`reduce`) — decrement `count` by one. AKS **gracefully cordons and
   drains** the removed node before deleting the VM. Repeated one node at a time
   (lowest-count pool first, to reach deletion sooner), each step guarded by the
   ceiling floor.
4. **Delete** — once a frozen pool reaches `count == 0`, remove the empty pool.

## Configuration

```yaml
fleet:
  nodePoolProvisioning:
    profile: "production"   # "" disables; ci | development | integration | production
    zones: ""               # CSV, e.g. "1,2,3"; empty → derived from the SKU API (first 3 zones)
```

