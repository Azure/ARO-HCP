# aks-cluster-create

Creates a management cluster's AKS `ManagedCluster` resource and all of its node
pools (system, infra, worker) during initial provisioning.

It plans pools with the **same** `fleet/pkg/compute` profiles the
[nodepool controller](../../../fleet/docs/nodepool) uses at steady state
(`compute.ResolveDesiredPools`). With the same profile, zones, quota limits, and
SKU metadata, both compute the same desired pool set. The tool covers the
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
the remaining system, infra, and worker pools concurrently. Required tiers must
allocate at least one pool, and a system pool must be available. Optional tier
allocation failures are logged; partial nonzero allocations can be provisioned.

### Idempotency & resume

An interrupted or failed pipeline step can rerun the tool to resume provisioning:

- The `ManagedCluster` carries an `aro-hcp-provisioning=true` **provisioning tag**
  set on the initial create and cleared after pool provisioning and capacity
  baseline initialization succeed. A rerun that finds the tag still set resumes;
  a rerun that finds it already gone initializes missing capacity baseline tags
  and logs the desired plan vs. live pools without modifying pools.
- A resumed run recomputes the desired plan and sends the cluster PUT again,
  preserving existing capacity tags. This also covers interruption after writing
  baselines but before removing the provisioning marker.
- `ensurePool` creates missing pools, re-issues `CreateOrUpdate` for pools in
  `Failed` state, and skips other existing pools. Baseline initialization requires
  successful ARM provisioning for the cluster and all observed pools. Transient
  HTTP retries are handled by the Azure SDK.

Once the tool exits successfully, the nodepool controller owns the cluster's
pools from then on.

## Capacity baseline adoption

The tool initializes missing `arohcp-capacity-system`, `arohcp-capacity-infra`,
and `arohcp-capacity-worker` tags from the live pool ceilings and SKU metadata.
Values are JSON objects with `vcpus`, `memoryGiB`, and `swiftNICs`. Autoscaled
pools use `maxCount`; static pools use `count`. Swift NIC capacity uses the
configured secondary-NIC tag on Swift-enabled workers.

Initialization requires successful ARM provisioning and complete capacity data.
Existing role tags are preserved, even if the newly computed desired plan is
smaller. Malformed baseline tags fail the run. New clusters receive their baseline
after pool provisioning and before the provisioning marker is removed. Tag writes
preserve unrelated tags and use the cluster ETag to reject concurrent updates.

The fleet controller requires these baselines before modifying pools and updates
them only after configuration convergence. A fully allocated configuration may
lower the baseline; a partial allocation must preserve it. Initial provisioning,
including retries, does not compare the desired plan against a baseline. The
baseline is established when provisioning finishes. Re-running the tool can
initialize missing baselines on finished clusters without applying the newly
computed plan.
