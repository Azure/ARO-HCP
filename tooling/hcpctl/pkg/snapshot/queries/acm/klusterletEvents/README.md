# acm / klusterletEvents

## Summary

Lists Kubernetes API events from the klusterlet namespace (`klusterlet-<cluster-id>`) for this cluster.
These are K8s Event objects ingested by the kube-events collector into `ServiceLogs.kubernetesEvents`.

## What to Look For

- `Evicted` reason on addon pre-delete pods — indicates node resource pressure (MemoryPressure,
  DiskPressure) evicted the pods before completion, blocking ManagedCluster Detaching.
- `FailedScheduling` reason — the pod could not be placed on any node.
- `FailedMount`, `BackOff`, `Unhealthy` reasons — container-level failures that prevent
  addon hooks from completing.

## Where to Go Next

- If addon pre-delete pods are being evicted, check `conditions/acm/managedClusterConditions.md` to
  confirm the ManagedCluster is stuck in Detaching.
- Review `logs/clustersService/logs.md` for the destruct chain state.
- See `docs/ops/cleanup-stuck-cluster-deletion.md`, Scenario 6, for manual remediation.
