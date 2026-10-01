# CI Service Cluster Consolidation

`svc.aks.systemPoolOnly` is enabled only in `ci00` and `ci01`. It defaults
to false, and schema validation rejects enabling it outside these environments
or on management clusters.

In this mode the service-cluster Bicep deployment:

- Retains the AKS System-mode pool and its `aro-hcp.azure.com/role=system` label.
- Removes the `CriticalAddonsOnly=true:NoSchedule` taint so application pods and
  Arobit can use the same nodes as AKS addons.
- Does not deploy worker or infra pools. Their configuration remains available
  for deployments with the mode disabled.
- Uses two `Standard_D4ds_v6` nodes at minimum, with autoscaling bounds of two
  to five nodes. The minimum is 8 vCPUs instead of the previous 18. One pool
  spanning the configured zones does not guarantee one node per zone.

Service PrometheusAgent and Prometheus Operator affinity selects `system` in
this mode; both still select `infra` otherwise. Management and opstool
placement, workload requests, resource limits, PDBs, and topology-spread rules
are unchanged. The system pool retains its 100-pod-per-node limit.

This intentionally removes workload isolation for ephemeral CI service clusters.
It does not establish node-loss or zone-loss capacity. Live CI must validate
AKS-managed addon placement, storage attachment, and rollout capacity under load.

CI management workers use six to twenty-eight `Standard_D8ds_v6` nodes per
zonal pool, with the 225-pod node limit unchanged. Two consecutive D8 runs grew
every mgmt-1 worker pool from four to five on `Insufficient cpu` scheduling
events; neither grew to six. Mgmt-2 stayed at four in both runs. The six-node
minimum adds headroom for the [selected HCP request increases](hcp-request-increases.md),
not a claim that the earlier runs demonstrated a six-node requirement.

Each zone starts with 48 vCPUs, 192 GiB, 1,350 pod slots, and eighteen SWIFT
secondary NIC slots. Across two management clusters and three zones each, the
worker baseline is 288 vCPUs, up from 240 with five D8s per zone and below the
older 480-vCPU five-D16-per-zone baseline. These are gross capacities, before
DaemonSet and node overhead. More nodes repeat that overhead. Two D8s
cost the same as one D16 at the checked Central US Linux retail compute rate;
the experiment targets avoiding pod-slot-driven scale-ups, not cheaper cores.
Autoscaling remains enabled and responds to pending requests, not CPU usage alone.

## Existing Clusters

Use a freshly created CI cluster to exercise this topology. Incremental ARM
deployments do not delete pools omitted by a conditional module. Enabling the
flag on an existing cluster makes system nodes eligible but does not move all
pods there or remove old nodes. Retiring existing pools requires a separate,
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

The tests cover CI opt-in, non-CI and management rejection, the Bicep parameter
path, conditional pool creation, and both rendered Prometheus affinities for
service, management, and opstool clusters. Local compilation is not a substitute
for a fresh CI deployment.
