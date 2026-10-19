# acm / klusterletEvents

## Summary

Lists Kubernetes API events from the klusterlet namespace (`klusterlet-<cluster-id>`) for this cluster.
These are K8s Event objects ingested by the kube-events collector into `ServiceLogs.kubernetesEvents`.

Rows with `countSemantics=max-cumulative-per-event` retain one event identity
(`cluster`, involved-object `objectUID`, and `eventUID` from `log.metadata.uid`).
Their `count` is the maximum cumulative count among exports selected by `timestamp`,
not a sum of exports. Distinct Event UIDs and recreated objects remain separate;
only sum these per-event maxima when a cumulative summary is needed.

If any identity field is missing, every selected observation remains a separate row
marked `undeduplicated-cumulative-observation`. Its `count` is an exported cumulative
value, not an independent event total; do not include it in an exact deduplicated total.

`firstSeen` and `lastSeen` are the earliest/latest reported event times (falling back
to `log.event_time`); `firstObserved` and `lastObserved` bound the selected export
timestamps. Neither pair proves continuous observation, continuous failure, or recovery.
Cumulative counts may include occurrences before the window. Counting increments
within the window requires suitable boundary observations. Exported rows, event
occurrences, and actual operation attempts are different denominators.

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
