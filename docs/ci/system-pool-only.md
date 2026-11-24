# Service Cluster Consolidation

CI and personal DEV service clusters run on a single node pool instead of the usual split into system, worker and infra pools. This reduces the minimum node footprint of each environment, which matters because CI runs many of them in parallel. The mode is controlled by `svc.aks.systemPoolOnly`, which defaults to false. The configuration schema only accepts it for `ci00`, `ci01`, `ci02` and `pers`, and rejects it on management clusters; the current node sizing for each of those environments is in [`config/config.yaml`](../../config/config.yaml). For the personal DEV side of this, see [Service Cluster Node Pools](../personal-dev.md#service-cluster-node-pools).

## What Changes

With the flag on, the service-cluster Bicep deployment keeps only the AKS system pool and drops the `CriticalAddonsOnly` taint from it, so application pods and Arobit share nodes with the AKS addons. The worker and infra pools are not created, although their configuration stays available for deployments that leave the flag off. Anything that normally selects the `infra` pool, such as the service Prometheus agent and operator, selects `system` instead. Management and opstool clusters, and workload requests, limits, disruption budgets and spread rules, are unchanged.

The trade-off is deliberate: there is no workload isolation inside these clusters, and the mode says nothing about surviving the loss of a node or a zone. One pool spanning several zones does not guarantee a node per zone either. Each environment should validate addon placement, storage attachment and rollout behaviour under load on a freshly created cluster.

## Existing Clusters

The flag is meant for newly created clusters and cannot be turned on for one that already exists. Incremental ARM deployments do not delete pools that a conditional module stops emitting, so the old worker and infra pools stay. Removing the taint lets new pods land on system nodes but does not move running ones. And where the environment also changes the system pool's VM size or OS disk, as `pers` does, the deployment fails outright with `PropertyChangeNotAllowed`, because AKS cannot change either property on an existing pool.

Recreate the environment instead. Retiring pools in place would need a separate, reviewed drain-and-delete procedure that checks PDBs and the zones of bound volumes, and turning the flag off on a consolidated cluster needs the same planning for the restored taint and monitoring placement.

## Validation

```sh
make -C config materialize
make test-helm-fixtures
go test ./tooling/templatize/internal -run TestSystemPoolOnlyConfigSchema
```

These cover the schema rules, the rendered Prometheus placement, and the Bicep parameter path. They are not a substitute for deploying a fresh cluster in each environment.
