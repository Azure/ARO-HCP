# Backend-owned cluster DNS allocation

The backend chooses and persists names before CS create. CS accepts
`domain_prefix` and `dns.base_domain`, and still owns Azure DNS provisioning and
cleanup. CREATE admission defaults an omitted customer prefix to `cluster`; supplied prefixes are
preserved. New claims use random five-character labels, disjoint from CS's legacy
four-character allocator.

## Controllers

**ClusterDNSAllocation** watches clusters, provider placement, and claim changes.
It uses the admission-assigned ARO `ClusterUID` and waits for the pending CS ID. It checks the
CS adoption lookup before allocating: existing CS clusters are adopted by the CS
create controller and retain their actual names. For genuinely new clusters,
successive reconciles:

1. Persist `ServiceProviderCluster.Spec.DesiredDNSBaseDomain`.
2. Create the Fleet claim for that candidate with the cluster's ARO UID as owner.
3. Observe the owned claim and publish `Cluster.ServiceProviderProperties.DNS.BaseDomain`.

Provider intent remains set after publication. The committed base domain stays on
Cluster because ARM reads return it without a provider join. For unallocated
clusters accepted before admission defaulting, publication also defaults an empty
prefix. Existing CS clusters take the adoption path before any naming mutation.

The CS create controller only consumes completed naming inputs; it no longer
performs allocation. A successful POST is adopted in the same reconciliation,
preserving the CS create controller's original behavior. A later reconciliation
can discover a CS cluster after a lost response while creation is still active.
Pending-CS deletion recovery is a separate race-fix PR, not part of DNS allocation.

**ClusterDNSBackfill** watches clusters, mirrored HyperShift resources, and claims.
It fills missing legacy base domains, verifies API/ingress names against persisted
intent, and establishes claims for existing allocations. Legacy prefix backfill
continues to obtain the actual prefix from CS. Mismatches return errors through
the standard cluster-controller wrapper, which persists `Degraded=True` and
retries. The general properties controller continues updating URLs and issuer
information, but no longer owns DNS backfill or verification.

**DNSClaimCleanup** watches claims with an hourly resync. It records a deletion
timestamp after live confirmation that the owning cluster incarnation is gone,
holds the name for seven days, then soft-deletes using the observed claim
ETag. Cluster deletion does not wait
for claim release, and there is no release acknowledgement on the cluster.

Controllers call listers and clients directly from context-based reconciliation
methods. Each reconciliation performs at most one persistence operation. Allocation
and backfill return write errors for retry. Cleanup treats stale-write conflicts
as benign and relies on claim events or periodic resync to reconsider them.

## Ownership and storage

Claims are keyed by
`/providers/Microsoft.RedHatOpenShift/dnsZones/<parent>/dnsClaims/<slug>` in the
Fleet partition `<parent>`. They contain the allocated base domain, owner
`ClusterUUID` (the ARO `ClusterUID`), typed owner ARM ID, claim `CreatedAt`, and
optional `DeletionTimestamp`. Ownership has no dependency on
CS identifiers. There is no duplicate owner field inside the cluster DNS profile.

Creation is atomic create-if-absent. An observed competing or retiring claim causes another
candidate to be chosen. An invisible soft-deleted claim causes create conflicts
until ordinary Cosmos TTL expiration. No tombstone-specific read or immediate
reuse mechanism is needed.

## Cleanup and name retention

Cleanup uses these rules:

| Observed parent | Action |
| --- | --- |
| Same UID, active | Retain |
| Same UID, deleting | Retain until the cluster document is gone |
| Missing parent | Confirm with live GET, then record claim deletion timestamp |
| Different UID at the same ARM ID | Confirm the old incarnation is gone with live GET, then record claim deletion timestamp |
| Observation error | Retain and retry |

The claim-local deletion timestamp is persisted once; retries and restarts do not
restart the hold. An hourly resync eventually observes the seven-day cutoff and
reconfirms owner absence with a live GET before deletion. Claim creation time does
not control release. The claim remains visible and exclusive during retention;
its content timestamp is distinct from the Cosmos envelope's soft-delete timestamp.
The ordinary short soft-delete TTL starts only after the hold has elapsed.

## Tests

Data-driven `SyncOnce` tables compare complete client write payloads and cover UID
ownership, fresh backfill timestamps, retention boundaries, missing/recreated
parents, cached/live disagreement, observation and write failures, and mismatch
degradation. An in-memory client test covers restart persistence and a stale delete
after claim replacement. The create wiring test drives
allocation separately and inspects the persisted cluster and Fleet claim inside
CS POST, including lost-response recovery.

```sh
go test ./backend/pkg/controllers/cluster/creation ./backend/pkg/controllers/cluster/dns ./backend/pkg/controllers/cluster/properties ./backend/pkg/controllers/cluster/deletion
```

## Rollout

Drain in-flight cluster creates from the old backend before enabling backend-owned
DNS allocation. A rolling leader handoff alone does not fence a CS request that
has already been sent. This change does not reconcile a legacy CS create that
finishes after the new backend has persisted naming intent. At current creation
volume a controlled cutover is sufficient; no mixed-version migration protocol
is included.
