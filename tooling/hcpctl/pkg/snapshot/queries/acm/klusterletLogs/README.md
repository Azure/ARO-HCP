# acm / klusterletLogs

## Summary

Lists pod lifecycle events from the klusterlet namespace (`klusterlet-<cluster-id>`) as observed by the
mgmt-agent PodWatcher. These are container log entries (not K8s Event objects) from the `mgmt-agent`
namespace where `log.msg == 'pod event'`, providing pod-level detail including status reason, phase,
and node placement.

## What to Look For

- Pods with `reason == 'Evicted'` and messages mentioning `MemoryPressure` or `DiskPressure` —
  indicates node resource pressure evicted addon pods before they could complete.
- `phase == 'Failed'` on addon pre-delete hook pods — confirms the pod did not run to completion.
- Multiple eviction entries for the same ReplicaSet hash — suggests repeated reschedule-then-evict
  cycles that exhaust the controller's retry budget.
- Evictions across different nodes — indicates cluster-wide resource pressure, not a single-node issue.

## Where to Go Next

- Check `events/acm/klusterletEvents.md` for the corresponding K8s Event objects (Evicted, FailedScheduling).
- Check `conditions/acm/managedClusterConditions.md` to see if the ManagedCluster is stuck pending deletion.
- Review `logs/clustersService/logs.md` for the destruct chain state.
- See `docs/ops/cleanup-stuck-cluster-deletion.md`, Scenario 6, for manual remediation.
