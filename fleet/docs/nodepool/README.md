# Management Cluster NodePool Controller

The nodepool controller replaces static, hand-defined management-cluster agent
pools with a controller that continuously converges a cluster's AKS agent pools
toward a computed desired state. It queries Azure for live quota and SKU
metadata, computes a zone-balanced set of pools for the cluster's profile, and
moves reality toward that plan **one safe step at a time, with no forced
eviction**.

The same planning code (`fleet/pkg/compute`) is used by the
[`aks-cluster-create`](../../../dev-infrastructure/scripts/aks-cluster-create)
tool at cluster-creation time. Both compute the same desired pool set for the
same profile, zones, quota limits, and SKU metadata.

## Concepts

### Profile

A **profile** is a named bundle of tiers plus the budget strategy used to size
system, infra, and worker pools. It is selected per environment via the
`fleet.nodePoolProvisioning.profile` config value (`ci`, `development`,
`production`). Profiles are
defined in code (`pkg/compute/tiers.go`, `LookupProfile` /
`ValidProfileNames`); config only chooses which one applies.

An empty profile disables the controller for that cluster.

### Tier

A **tier** (`TierConfig`) describes one desired VM size class:

| Field | Meaning |
|-------|---------|
| `Name` | Stable 1–5 char symbolic id (`^[a-z][a-z0-9]{0,4}$`), unique within a profile; leads every pool name |
| `Role` | `system`, `infra`, or `worker` — kept apart by taints; capacity is not fungible across roles |
| `PoolMode` | `PerZone` creates `min(PoolCount, len(zones))` pools per SKU in the first eligible zones; a SKU must cover that many zones. `Regional` creates one pool per SKU with no zones set. Fallback families can add further pool sets until the tier's node target is filled |
| `Cores` | Exact vCPU count required per node |
| `FamilyPriority` | Ordered VM family fallback list. Regional tiers prefer families with narrower zone coverage, preserving broader coverage for zonal tiers |
| `MaxNodes` | Node target per effective pool position, shared across fallback families; each emitted pool receives its allocated portion as its autoscaler ceiling |
| `Labels` / `Taints` | Applied to nodes; the controller owns these and corrects drift |
| `EnableSwift` | Swift v2 multi-tenant NIC support (worker pools) |
| `Required` | During bootstrap, the tier must allocate some pools. The steady-state controller rejects any tier allocation failure |

Tiers are processed **in order**: tier 0 draws from the shared per-family quota
budget first, tier 1 from the remainder, and so on.

### Protected capacity

The AKS cluster stores one JSON baseline tag per role: `arohcp-capacity-system`,
`arohcp-capacity-infra`, and `arohcp-capacity-worker`. Each value contains
`vcpus`, `memoryGiB`, and `swiftNICs`, calculated from configured pool ceilings:
`maxCount` for autoscaled pools and `count` for frozen or static pools.
Swift NICs use the configured secondary-NIC count on Swift-enabled worker pools.
Each dimension is protected independently; extra CPU cannot compensate for lost
memory or NIC capacity.

The `cluster-create` pipeline step initializes missing tags from observed pools
after provisioning succeeds, or when adopting an already finished cluster. It
preserves existing baselines but does not use them to constrain initial
provisioning or reruns of an interrupted provisioning step. The controller
requires all three tags and rejects malformed values or incomplete ARM/SKU
capacity data. A tier that allocates no pools stops reconciliation, including
an optional tier. Partial nonzero allocation remains valid only if its desired
capacity meets every role's baseline.

A fully allocated configuration is authoritative, even when it reduces capacity
or removes a tier. Full allocation means every configured tier reaches its node
target across its effective pool count, including any fallback families. Finding
some pools for every tier is not sufficient. Full allocation describes the
computed plan; Azure provisioning must still succeed. For each role and resource,
the transition floor is the lower of the stored baseline and the fully allocated
desired capacity. Partial plans retain the stored baseline as their floor.

The planner checks every capacity-reducing candidate against that floor,
including bounds corrections, squeezes, freezes, drains, and deletion. Unsafe
candidates are skipped so other safe corrections or replacement growth can
proceed. Temporary replacement overlap does not raise the floor. A replacement
without enough quota to preserve the floor stops until capacity becomes available.

Stored baselines change only after explicit ARM configuration convergence: all desired
pools exist, no managed undesired pools remain, provisioning has succeeded,
autoscaling and ceilings match, and managed configuration matches. An absence of
available actions can mean blocked progress and does not establish convergence.
Blocked reconciliation returns an error, so the shared controller reports
`Degraded=True` and retries through its rate-limited queue. Running counts need
not equal autoscaler maxima. This protects configured
capacity, not Kubernetes node readiness, workload health, or per-zone placement.
Capacity reporting and placement behavior are unchanged.

### Pool identity

Each pool's AKS name is `<Name><zone><hash>` (≤12 chars, the AKS limit): the
tier's symbolic `Name`, the availability-zone digit, and a 6-hex-char SHA-256 of
the tier's identity fields (`Role`, `VMSize`, `OSDiskSizeGB`, `MaxPods`,
`EnableSwift`). Role changes replace pools; they never relabel existing pools. The controller matches desired against live pools by this name.

Consequences:

- **Mutable** changes (`MaxNodes`, `Labels`, `Taints`) keep the name — the pool
  is reconciled in place.
- **Role or immutable field** changes flip the hash → a new name → the reconciler creates the
  new pool and drains the old one (a **rolling replace**, never a forced
  eviction). This is the only correct response, since AKS cannot change these
  fields on an existing pool.
- `Name` is a **permanent identifier**: renaming a tier replaces all its pools.
  Unique tier names separate tiers; the hash distinguishes identities within
  each tier and zone.

### Budget strategy

Pool sizing across all roles is bounded by a per-VM-family vCPU **budget**:

- `SubscriptionQuotaBudget` — desired capacity uses the quota limit; available capacity for growth is quota limit − live usage (production/integration).
- `UnlimitedBudget` — unconstrained, for environments where quota is externally guaranteed (CI/dev).

Subscription usage is assumed to belong to pools managed by this planner.
Current usage constrains growth without changing the desired pool set. The
allocator reserves surge from the quota limit, and action planning deducts
unused autoscaler reservations from available capacity to determine headroom.

## Reconcile pipeline

Each `SyncOnce` runs four phases for a single management cluster.

1. **Gather** — read the AKS cluster and baseline tags, require successful
   cluster provisioning, and list live agent pools. Only pools carrying a
   nonempty `aro-hcp.azure.com/role` label are managed; unknown roles fail capacity
   validation.
2. **Resolve** (`compute.ResolveDesiredPools`) — fetch cached SKU metadata and
   the profile's quota budget, then call the deterministic `ComputeDesiredPools`
   allocator. The result includes desired pools, allocation failures, allocation
   completeness, SKU metadata, and available quota for growth.
3. **Validate and diff** — reject unacceptable plans or incomplete observed
   capacity data and derive the transition floor. If ARM configuration has
   converged, update the baseline tags and return; unchanged tags require no
   write. Otherwise, `findNextAction` selects one safe action. No available
   action before convergence is an error, not successful idle reconciliation.
4. **Act** — executes the selected safe action against the AKS ARM API, then requeues the
   key after a delay sized to how long AKS needs to settle that change.

### Invariants

- **One action per reconcile.** Progress is deliberate and observable.
- **One in-flight operation.** If any pool is transitioning, the only action is
  `wait`.
- **Optimistic concurrency.** Existing pool mutations and baseline writes use
  ETag / If-Match; new pool creation uses `If-None-Match: *`.
- **Grow before shrink.** New/target capacity is added before undesired capacity
  is removed whenever quota permits.
- **Capacity floor.** Every reduction must preserve the accepted CPU, memory,
  and Swift NIC floor for the affected role. Only a fully allocated configuration
  can lower the floor below the stored baseline. Growth does not raise it before
  convergence. VM families share capacity within a role; their quota headroom
  remains separate.
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
   evicting any node**. It must preserve the accepted transition floor, so a replacement
   with insufficient spare quota can block.
2. **Freeze** — disable autoscaling, pinning the pool at its current `count`.
   The autoscaler can no longer fight back, and the count becomes a stable base
   for draining. Freeze is also used on a *desired* pool whose `count` sits above
   a lowered target, so it can be drained down.
3. **Drain** (`reduce`) — decrement `count` by one. AKS **gracefully cordons and
   drains** the removed node before deleting the VM. Repeated one node at a time
   (lowest-count pool first, to reach deletion sooner), each step guarded by the
   accepted transition floor.
4. **Delete** — once a frozen pool reaches `count == 0`, remove the empty pool.

## Configuration

```yaml
fleet:
  nodePoolProvisioning:
    profile: "production"   # "" disables; ci | development | production
    zones: ""               # CSV, e.g. "1,2,3"; empty → derived from the SKU API (first 3 zones)
```

## Golden action traces

Fixtures in [`pkg/controllers/nodepool/testdata`](../../pkg/controllers/nodepool/testdata)
show pool roles in the initial, desired, and final states. They record allocation
completeness, the stored baseline, the accepted transition floor, initial
capacity, and capacity after each action. Capacity totals cover CPU, memory in
GiB, and Swift NICs per role; action rows show the affected role's total.

Signed margins compare capacity with the **stored baseline**, including when a
fully allocated configuration permits a lower transition floor. A negative margin
can therefore be valid; every simulated action must still meet the transition
floor. Zero baseline values can represent absent roles or a fresh cluster;
synthetic scenarios can also start above an earlier baseline. Per-family quota
headroom is tracked separately from these capacity floors.

The [production migration fixtures](../../pkg/controllers/nodepool/testdata/TestProductionScenario_MigrationToDesired)
cover the real snapshot rejected for partial allocation below baseline, a
synthetic capacity-preserving migration with additional quota, and a fully
allocated downsize under an explicitly smaller configuration.
