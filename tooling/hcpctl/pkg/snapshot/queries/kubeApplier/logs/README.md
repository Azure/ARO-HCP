# kubeApplier / logs

## Summary

kube-applier logs for the cluster, from the `kubeApplierLogs` table, filtered to the
cluster's `subscription_id`, `resource_group`, and `cluster_name` (the HCP cluster name).
The kube-applier runs on the management cluster and reconciles the cluster's ApplyDesire /
ReadDesire documents against the management-cluster apiserver (server-side apply and delete).
Each row carries the `controller_name` (e.g. `applydesire`, `readdesirekubernetes`) and the
`resource_id` of the desire being reconciled, so this includes both cluster-scoped and
nodepool-scoped desire activity for the cluster.

## What to Look For

- `level == 'error'` entries and the `msg` around a stuck apply/delete — e.g. server-side-apply
  rejections (`KubeAPIError`) or a delete waiting on finalizers (`WaitingForDeletion`).
- Repeated reconciles for the same `resource_id` without progress — indicates a desire that never
  reaches its successful condition.
- Group by `controller_name` to separate apply (`applydesire`) from read (`readdesire*`) activity.

## Where to Go Next

- Check `state/backend/serviceProviderState.md` and `conditions/backend/` for the backend's view of
  the desires and their mirrored conditions.
- Check `events/hypershift/` and the HyperShift operator logs for the effect of the applied objects
  on the hosted control plane.
