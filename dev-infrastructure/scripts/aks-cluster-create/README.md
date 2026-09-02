# aks-cluster-create

Creates a management cluster's AKS `ManagedCluster` resource and all of its node
pools (system, infra, worker) during initial provisioning.

It plans pools with the **same** `fleet/pkg/compute` profiles the
[nodepool controller](../../../../fleet/docs/nodepool) uses at steady state
(`compute.ResolveDesiredPools`), so a freshly created cluster and a
controller-reconciled one are identical by construction. The tool covers the
one thing the controller cannot: the initial `ManagedCluster` and its first
system pool, which must exist before any controller can reconcile.

## Why a Go tool (not bicep)

The management pipeline runs this as a `Shell` step (`mgmt-pipeline.yaml`,
step `cluster-create`) rather than an ARM/bicep deployment because pool planning
needs live quota and SKU data and must reuse the controller's allocation logic —
not something bicep can express. The EV2 Shell runner image has no Go toolchain,
so the pipeline `buildStep` compiles the binary ahead of time and ships it in the
step's working directory (same pattern as `scripts/grafana-group-roles`).

## Behavior

`run` creates the cluster with its first system pool inline (AKS requires at
least one system-mode pool in the initial `ManagedCluster` payload), then creates
the remaining system, infra, and worker pools in that order.

### Idempotency & resume

Every step is safe to resend, so a pipeline retry after a partial failure just
continues where it left off:

- The `ManagedCluster` carries an `aro-hcp-provisioning=true` **provisioning tag**
  set on the initial create and cleared only once the cluster and all pools
  exist. A retry that finds the tag still set resumes; a retry that finds it
  already gone treats the run as a no-op success (and still logs the desired plan
  vs. live pools, without applying anything).
- `ensurePool` skips pools that already exist and are healthy, re-issues
  `CreateOrUpdate` for pools left in `Failed` state, and retries transient
  failures.

Once the tool exits successfully, the nodepool controller owns the cluster's
pools from then on.
