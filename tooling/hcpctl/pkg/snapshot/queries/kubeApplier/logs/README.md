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
- Repeated reconciles can be normal: applies resync every ten minutes; readers reevaluate every 60 seconds.
  Correlate errors, waiting reasons, conditions, and target-object changes before diagnosing a stuck desire.
- `SuccessfullyApplied=True` confirms apply success; check target-object conditions for readiness.
  Deletion requires `SuccessfullyDeleted=True`; never fall back to legacy `Successful`.
- Group by `controller_name` to separate apply (`applydesire`) from read (`readdesire*`) activity.

## Where to Go Next

- Check `state/backend/serviceProviderState.md` and `conditions/backend/` for the backend's view of
  the desires and their mirrored conditions.
- Check `events/hypershift/` and the HyperShift operator logs for the effect of the applied objects
  on the hosted control plane.
