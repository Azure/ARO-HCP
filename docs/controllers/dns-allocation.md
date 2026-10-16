# Backend-owned cluster DNS allocation

The backend chooses and persists names before CS create. CS accepts
`domain_prefix` and `dns.base_domain`, and still owns Azure DNS provisioning and
cleanup. An omitted customer prefix defaults to `cluster`; supplied prefixes are
preserved. New claims use random five-character labels, disjoint from CS's legacy
four-character allocator.

## Controllers

**ClusterDNSAllocation** watches clusters, provider placement, and claim changes.
It waits for the existing ARO `ClusterUID` and pending CS ID. It checks the existing
CS adoption lookup before allocating: existing CS clusters are adopted by the CS
create controller and retain their actual names. For genuinely new clusters,
successive reconciles:

1. Persist `DNS.PendingBaseDomain` and default the customer prefix if omitted.
2. Create the Fleet claim for that candidate with the cluster's ARO UID as owner.
3. Observe the owned claim and promote the candidate to `DNS.BaseDomain`.

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

**DNSClaimCleanup** reconciles claims. It watches claims directly and cluster
changes by enqueueing the committed and pending domains from the event object.
It soft-deletes using the observed claim
ETag; it does not copy a read-only delete target. Cluster deletion does not wait
for claim release, and there is no release acknowledgement on the cluster.

All mutation decisions return complete payloads; executors perform one mutation
and return. Meaningful logs belong to the decisions. Ordinary Fleet CRUD,
informers, listers, and conditional writes handle cache lag through retries.

## Ownership and storage

Claims are keyed by
`/providers/Microsoft.RedHatOpenShift/dnsZones/<parent>/dnsClaims/<slug>` in the
Fleet partition `<parent>`. They contain the allocated base domain, owner
`ClusterUID`, owner ARM ID, and claim `CreatedAt`. Ownership has no dependency on
CS identifiers. There is no duplicate owner field inside the cluster DNS profile.

Creation is atomic create-if-absent. An observed competing claim causes another
candidate to be chosen. An invisible soft-deleted claim causes create conflicts
until ordinary Cosmos TTL expiration. No tombstone-specific read or immediate
reuse mechanism is needed.

## Cleanup and bounded cache skew

The cleanup decision uses these rules:

| Observed parent | Action |
| --- | --- |
| Same UID, active | Retain |
| Same UID, deleting | Retain until CS deletion is dispatched and both pending/confirmed CS IDs are cleared; then delete |
| Missing parent | Delete only when the claim is at least 30 minutes old |
| Different UID at the same ARM ID | Apply the same 30-minute claim-age guard |
| Parent UID not yet populated | Retain |
| Observation error | Retain and retry |

Young apparent orphans are requeued for their age cutoff. The timestamp is the claim's
creation time, including freshly backfilled claims for old clusters.

This intentionally tolerates bounded cache skew rather than proving cross-container
ordering. It assumes independent caches catch up within the 30-minute grace.
Matching-owner cleanup trusts the existing CS completion signals. Claim TTL may
delay name reuse after cleanup, which is acceptable.

## Tests

Pure decision tables compare complete payloads and cover UID ownership, fresh
backfill timestamps, age boundaries, missing/recreated parents, cleanup gates,
observation failures, and mismatch degradation. The create wiring test drives
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
