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
- `ManagedClusterImportSucceeded` showing reason `ManagedClusterForceDetaching` — the hub is attempting
  a forced detach, indicating the ManagedCluster is stuck pending deletion.

## Where to Go Next

- If conditions show the cluster stuck pending deletion, check `logs/clustersService/logs.md` for repeated
  `Not continuing to the next destructor` messages at the `hypershift-managed-cluster-destructor` step.
- Review `events/acm/klusterletEvents.md` for K8s events and `logs/acm/klusterletLogs.md` for pod-level
  detail on evictions or scheduling failures in the klusterlet namespace.
- See `docs/ops/cleanup-stuck-cluster-deletion.md`, Scenario 6, for the manual fix (clearing
  `ManagedClusterAddon` finalizers).
