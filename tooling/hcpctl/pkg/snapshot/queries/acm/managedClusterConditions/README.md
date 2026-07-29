# acm / managedClusterConditions

## Summary

Extracts a point-in-time snapshot of ManagedCluster conditions from the last mgmt-agent ResourceWatcher
emission within the current phase's time window. The ManagedCluster CR is cluster-scoped and named by the
CS cluster ID.

## What to Look For

A healthy ManagedCluster should have:

| type                    | status | reason              |
|-------------------------|--------|---------------------|
| HubAcceptedManagedCluster | True | HubClusterAdminAccepted |
| ManagedClusterConditionAvailable | True | ManagedClusterAvailable |
| ManagedClusterJoined    | True   | ManagedClusterJoined |

During deletion, watch for:

- `ManagedClusterConditionAvailable` changing to `Unknown` — indicates the klusterlet has lost contact,
  which can prevent addon pre-delete hooks from executing.
- The ManagedCluster entering `Detaching` state — check whether `ManagedClusterAddon` pre-delete hook
  pods are running or being evicted.

## Where to Go Next

- If conditions show the cluster stuck in `Detaching`, check `logs/clustersService/logs.md` for repeated
  `Not continuing to the next destructor` messages at the `hypershift-managed-cluster-destructor` step.
- Review `events/acm/klusterletEvents.md` for pod eviction or scheduling failures in the klusterlet
  namespace.
- See `docs/ops/cleanup-stuck-cluster-deletion.md`, Scenario 6, for the manual fix (clearing
  `ManagedClusterAddon` finalizers).
