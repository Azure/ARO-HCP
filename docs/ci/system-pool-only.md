# Service Cluster Consolidation

`svc.aks.systemPoolOnly` is enabled only in `ci00`, `ci01` and `pers`. It
defaults to false, and schema validation rejects enabling it in other
environments or on management clusters. Personal DEV uses three to five
`Standard_D4ds_v5` nodes with 64 GB OS disks instead of the CI sizing below;
see [Service Cluster Node Pools](../personal-dev.md#service-cluster-node-pools)
for how existing personal environments are handled.

In this mode the service-cluster Bicep deployment:

- Retains the AKS System-mode pool and its `aro-hcp.azure.com/role=system` label.
- Removes the `CriticalAddonsOnly=true:NoSchedule` taint so application pods and
  Arobit can use the same nodes as AKS addons.
- Does not deploy worker or infra pools. Their configuration remains available
  for deployments with the mode disabled.
- Uses three `Standard_D4ds_v6` nodes initially, with autoscaling bounds of three
  to five nodes. The minimum is 12 vCPUs instead of the previous 18. One pool
  spanning the configured zones does not guarantee one node per zone.

Service PrometheusAgent and Prometheus Operator affinity selects `system` in
this mode; both still select `infra` otherwise. Management and opstool
placement, workload requests, resource limits, PDBs, and topology-spread rules
are unchanged. The system pool retains its 100-pod-per-node limit.

This intentionally removes workload isolation inside CI and personal DEV service
clusters. It does not establish node-loss or zone-loss capacity. A freshly
created cluster in each environment must validate AKS-managed addon placement,
storage attachment, and rollout capacity under load.

## Existing Clusters

Use a freshly created CI or personal DEV cluster to exercise this topology.
Incremental ARM deployments do not delete pools omitted by a conditional module.
Enabling the flag on an existing cluster makes system nodes eligible but does
not move all pods there or remove old nodes, and a changed system pool VM size
or OS disk cannot be applied in place. `make personal-dev-env` therefore keeps
existing personal service clusters on their dedicated pools and live system pool
settings until they are recreated. Retiring existing pools requires a separate,
reviewed drain and deletion procedure, including PDB and bound-PVC zone checks.
Disabling the flag on a consolidated cluster similarly requires planning for
the restored taint and monitoring placement; it is not a live migration tool.

## Validation

```sh
make -C config materialize
make test-helm-fixtures
go test ./tooling/templatize/internal -run TestSystemPoolOnlyConfigSchema
az bicep build --file dev-infrastructure/templates/svc-cluster.bicep --stdout
```

The tests cover CI and personal DEV opt-in, rejection in other environments and
on management clusters, the Bicep parameter path, conditional pool creation, and
both rendered Prometheus affinities for service, management, and opstool
clusters. Local compilation is not a substitute for a fresh deployment in each
environment.
