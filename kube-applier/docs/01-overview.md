# 01 &mdash; Overview, goals, key decisions

## What problem is being solved

The ARO-HCP backend, which runs in *service clusters*, needs to influence
arbitrary Kubernetes resources in *management clusters*. Direct cross-cluster
API access is not desirable (credentials, blast radius, network topology).

Today, parts of this gap are closed by purpose-built controllers (e.g. the
`Controller` resource pattern, Maestro's bundle controllers, etc.). The
`kube-applier` generalises that bridge by introducing three minimal,
declarative "desire" resources stored in Cosmos. The backend writes them; the
kube-applier reconciles them against the local kube-apiserver.

## Components

```
                                            +-----------------------------+
                          (write spec)      |  Cosmos container           |
   +---------+   +--------------------+     |  "kube-applier"             |
   | Backend |-->| GlobalLister/CRUD  |---->|                             |
   +---------+   |   (cross-partition)|     |  partition = MgmtCluster    |
                 +--------------------+     |  docs:                      |
                                            |   - ApplyDesire             |
                                            |   - DeleteDesire            |
                                            |   - ReadDesire              |
                                            +--------------+--------------+
                                                           |
                                            (single-partition CRUD,
                                             status writes)
                                                           v
   +-------------+   +-------------------------------------+--+
   | Mgmt        |   |  kube-applier binary (per mgmt cluster) |
   | Kube API    |<--|  - ApplyDesireController               |
   |             |   |  - DeleteDesireController              |
   |             |   |  - ReadDesireInformerManagingController|
   |             |   |     spawns/destroys                    |
   |             |   |       ReadDesireKubernetesController N |
   +-------------+   +-----------------------------------------+
```

## Key design decisions

### Partition key is the management cluster name

The README is explicit: `kube-applier` is partitioned by `managementCluster`
to provide isolation &mdash; a kube-applier in cluster *A* must only have
credentials for partition *A*.

This **deviates** from the existing convention in
`internal/database/database.go:NewPartitionKey`, which lower-cases the
subscription ID. Implications:

- We need a partition-key helper local to the kube-applier CRUD code (e.g.
  `func newKubeApplierPartitionKey(managementCluster string) azcosmos.PartitionKey`).
- The cosmos role assignments for kube-applier pods must scope writes to a
  single partition (separate from the backend's broader role).
- The `ResourceID` already encodes subscription/resource group/cluster &mdash;
  we are not losing addressing information.

### One Cosmos document per `*Desire`; no list/select APIs at the kube layer

Per the readme: each `*Desire` references exactly one Kubernetes object. We
are intentionally not adding `ApplyManyDesire`, `ReadManyDesire`, label
selectors, or list-all variants. This keeps every `.status` story
unambiguous.

### Status is communicated entirely via `metav1.Condition` slices

Existing patterns in this repo use the same `meta.SetStatusCondition` helper
(see `controllerutils/util.go:201-220` for the `Degraded` condition pattern).
We reuse that idiom on each `*Desire.Status.Conditions`.

The well-known condition types per the readme:

- `Successful` &mdash; was the desired effect achieved (with reasons
  `KubeAPIError` or `PreCheckFailed` when not).
- `Degraded` &mdash; controller-level health (replaces the existing
  `Controller` resource's `Degraded`).

### `kube-applier` is a controller binary, not a webhook or sidecar

It runs continuously, leader-elected, alongside other ARO-HCP
management-cluster services. It uses `rest.InClusterConfig()` like the
`admin` server does.

### Cross-partition access lives only in `GlobalListers`

Following the existing split (`internal/database/global_lister.go`):

- The kube-applier uses single-partition CRUD scoped to its
  management-cluster partition.
- The backend uses the new `GlobalListers().*Desires()` methods to list
  across all management clusters.

## Out of scope (for the initial implementation)

- Versioned ARM API types &mdash; `*Desire` is internal-only, no
  `v202xMonthDay` packages.
- A `ReadManyDesire` variant &mdash; the readme explicitly defers this.
- Generic CRD authoring on management clusters &mdash; we are *applying*
  arbitrary content, not generating typed Go clients.
- Schema validation of `.spec.kubeContent` beyond what the kube-apiserver
  enforces during apply.
- Garbage collection of orphaned `*Desire` documents &mdash; assumed to be
  the backend's responsibility.

## Open questions to resolve before/while implementing

1. **Does the existing Cosmos role model already support per-partition writer
   roles?** If not, that needs design with the platform team. The bicep
   role-assignments live in `dev-infrastructure/`.
2. **Workload-identity wiring for the kube-applier pod.** The backend wires
   FPA/MI; the kube-applier needs a similar but *narrower* identity. Look at
   `dev-infrastructure/modules/managed-identity/*` and existing service
   helm charts.
3. **Field-manager name for SSA.** Suggest `kube-applier` plus the resource
   name as the manager. Confirm there is no collision with hypershift or
   ACM-pull controllers writing the same fields.
4. **Will the backend ever need to read the live `.status.kubeContent` of a
   `ReadDesire`?** If yes, the `KubeContent` field must be `omitempty: false`
   so absent data is distinguishable from empty, and we want a
   `LastObservedTime` annotation alongside.

These are flagged again in [08-rollout.md](08-rollout.md) at the appropriate
stage.
