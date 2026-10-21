# acm / klusterletEvents

## Summary

Lists Kubernetes API events from the klusterlet namespace (`klusterlet-<cluster-id>`) for this cluster.
These are K8s Event objects ingested by the kube-events collector into `ServiceLogs.kubernetesEvents`.

## What to Look For

- `Evicted` reason on addon pre-delete pods — indicates node resource pressure (MemoryPressure,
  DiskPressure) evicted the pods before completion, blocking ManagedCluster deletion.
- `FailedScheduling` reason — the pod could not be placed on any node.
- `FailedMount`, `BackOff`, `Unhealthy` reasons — container-level failures that prevent
  addon hooks from completing.

## Where to Go Next

- If addon pre-delete pods are being evicted, check `logs/acm/klusterletLogs.md` for pod-level detail
  (status reason, phase, node placement) and `conditions/acm/managedClusterConditions.md` to confirm
  the ManagedCluster is stuck pending deletion.
- Review `logs/clustersService/logs.md` for the destruct chain state.
- See `docs/ops/cleanup-stuck-cluster-deletion.md`, Scenario 6, for manual remediation.
