# mgmtAgent / podEvents

## Summary

Lists all pod lifecycle events observed by the mgmt-agent PodWatcher for pods in the klusterlet namespace of this cluster. Includes addon pre-delete hook pods, controller pods, and any other pods managed by ACM/MCE in the klusterlet namespace.

## What to Look For

- Pods with `reason` of `Evicted` and `message` containing `MemoryPressure` or `DiskPressure` — these indicate node resource pressure caused pod eviction before completion.
- Pods with `phase` of `Failed` — especially addon pre-delete hook pods (`*-uninstall`) that failed before the ManagedCluster could complete Detaching.
- `Delete` events for pods that should still be running during cluster deletion.

## Where to Go Next

If addon pre-delete pods were evicted, check which node evicted them and whether the node had resource pressure conditions. Review Clusters Service logs to confirm the destruct chain is blocked at `hypershift-managed-cluster-destructor`.
