# Delete a Stuck Node Pool via Geneva Action

## Problem Description

A management cluster's reconcile loop can get stuck when a stale or quota-constrained node pool
can't reach its configured `minCount`. This happens for example when a node pool's VM SKU
family has no quota headroom left in a region (physically constrained, not just a limit that
can be raised), and the reconcile loop keeps retrying that pool instead of bringing up a
replacement pool on a different SKU. This procedure covers stale pools that the
active provisioning path cannot remove automatically.

For clusters managed by the [Fleet nodepool controller](../../fleet/docs/nodepool),
undesired pools are drained and deleted automatically when the capacity floor
allows it. A blocked replacement reports `Degraded=True`; direct deletion bypasses
that protection. Check the rejected plan or blocked-action error and the role's
CPU, memory, and Swift NIC floor before considering this procedure. A pool still
present in the desired plan will be recreated by the controller.

## When to Use

- A management cluster rollout is stuck because an old node pool can't scale to its `minCount`
  due to exhausted/unavailable quota for its SKU family in that region
- You've already confirmed (and ideally already requested/received) quota for the replacement
  SKU family, and the only remaining blocker is the stale pool itself
- Scope is limited to the specific node pool; this should not be used to remove pools that are
  still in active use

## Prerequisites

JIT access to the target subscription, requesting:

- **Azure Kubernetes Service RBAC Cluster Admin** (Elevated/Unrestricted)
- **Azure Kubernetes Service RBAC Admin** (Elevated/Unrestricted)
- **Contributor** (Elevated/Unrestricted)
- **Change Safety Contributor** (Unclassified), required to run the Geneva Action itself

Example JIT justification:

> Clean up quota-constrained node pools. \<tracking ticket\>

## Procedure

### Step 1: Identify the node pool's ARM resource ID

```text
/subscriptions/<subscription-id>/resourceGroups/<mgmt-rg>/providers/Microsoft.ContainerService/managedClusters/<cluster-name>/agentPools/<pool-name>
```

### Step 2: Run the Geneva Action

Click path in the Geneva portal:

`Azure-UnifiedGASO > ChangeSafety Operations > Delete Azure Resource`

Operation details:

- **Operation Id:** `DeleteAzureResourceChangeSafety`
- **Idempotent:** true
- **Risk Level:** High
- **Touch Types:** Delete

Inputs:

- **Input Mode:** Single
- **Endpoint:** `AzureUnifiedGASOEndpoint`
- **ResourceType:** `Microsoft.ContainerService/managedClusters/agentPools`
- **ResourceId:** the ARM resource ID from Step 1

Run the action and confirm the result tab shows a success message with a deletion timestamp.

> **Note**: this operation's own metadata lists "Operation Type: Read Only" despite performing a
> delete. That field describes the operation category, not its actual effect. Treat this action
> as destructive.

### Step 3: Verify from a cluster shell

Use a **fresh** shell session (not a reconnected one) to avoid stale scrollback:

```bash
kubectl get nodes -l kubernetes.azure.com/agentpool=<pool-name> --no-headers   # expect no output
kubectl get pods -A --no-headers | awk '{n=split($3,r,"/")} $4!="Completed" && (r[1]!=r[2] || $4!="Running")'  # investigate any output
```

If a reconnected/old terminal session shows the deleted pool's nodes still present, check the
node ages first: a reconnected shell can replay old scrollback, so a listing with younger node
ages than a prior listing is stale output, not the current state. Re-run the check in a new
shell to confirm.

### Step 4: Confirm the reconcile loop unblocks

Watch the affected rollout to confirm the replacement node pool comes up on the now-available
quota, and that the control plane's Deployments land on the new nodes as expected.

- **Where to watch:** the EV2 rollout/pipeline run (or equivalent CI job) that triggered the
  reconcile, plus `kubectl get nodes` / `kubectl get machines` on the management cluster for
  the replacement pool's nodes joining.
- **Expected healthy state:** the replacement node pool's nodes reach `Ready`, and the
  platform Deployments that were pending reschedule land on those nodes and go `Running`.
- **Approximate timeout:** node pool scale-up and platform pod rescheduling typically
  complete within 10-15 minutes of the Geneva Action succeeding. If the rollout is still
  stuck after that:
  - Re-check `kubectl get nodes` and the node pool's `minCount`/`maxCount` to confirm the
    stale pool is actually gone and the replacement pool is progressing.
  - Confirm the replacement SKU family's quota is genuinely available in the region
    (`az vm list-usage`), not just requested.
  - If the reconcile is still blocked for a different reason, escalate to the rollout owner
    with the specific error from the reconcile/controller logs rather than repeating this
    procedure.

## Related

- [docs/ops/override-cluster-size.md](override-cluster-size.md)
