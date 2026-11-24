# E2E Capacity And Quotas

How much E2E work CI can run at once is set by the [slot catalog](../../test/e2e-config/e2e-slots.yaml) and bounded by limits in the Azure subscriptions behind it. This page explains those limits, shows how to check that a catalog still fits within them, and describes how to change capacity safely. The catalog holds the actual numbers, so this page does not repeat them, and a capacity-only change needs no documentation update.

## What Limits Capacity

Capacity has two dimensions, and the catalog controls both. The number of slots in the pools a job may use (`slot_count`) bounds how many runs can be in flight at the same time. The number of identity containers leased to each slot (`resource_group_count`, see [E2E Identities](slot-assets.md#e2e-identities)) bounds how many hosted clusters a single run can have at once. Everything else is a constraint on how far those two numbers can be pushed:

- the **role-assignment quota** of each subscription, which applies in every environment
- the **deny-assignment limit** of each subscription, which only applies in the AME tenant (STG and PROD)
- **regional vCPU and public IP quota** for the node shapes the tests use, requested when a subscription is onboarded (see [E2E Subscription Onboarding](e2e-subscription-onboarding.md))

The usual way to get more throughput is to add subscriptions, because each one brings its own role-assignment budget and its own fleet of identity containers.

DEV has live dashboards and alerts for directory and role-assignment quota; see [DEV CI Monitoring and Alert Response](dev-ci-monitoring.md). Look there for current usage before resizing anything.

## Role-Assignment Quota

Every hosted cluster that a test creates consumes role assignments in its subscription, and a subscription has a finite quota (8,000 for a subscription onboarded as documented). Two parts add up: the assignments made by the test setup Bicep (`test/e2e-setup/bicep/modules/`) and the assignments the Resource Provider makes in the cluster's managed resource group. The cost depends on the RBAC scope the test uses:

- With `resourceGroupScope`, which almost all specs use, a cluster costs about 26 assignments: 13 from the test setup and 13 from the RP-managed resource group.
- With `resourceScope`, which specs request through `framework.RBACScopeResource`, a cluster costs about 41: 28 from the test setup and the same 13.

These figures were correct when this page was written and have gone stale before. Re-derive them instead of trusting them: count the `Microsoft.Authorization/roleAssignments` resources that apply for each `rbacScope` value in `non-msi-scoped-assignments.bicep` and `msi-scoped-assignments.bicep` (unconditional ones count for both), and for the RP's share inspect the role assignments on managed resource groups in a test subscription. Count the resource-scoped specs with `git grep -o "framework.RBACScopeResource," -- test/e2e/ | wc -l`. A test path that deploys a pinned older copy of the setup Bicep is always resource-scoped, because those copies only ever grant resource-scoped RBAC.

The cost of a run depends on how many hosted clusters it holds at the same time, which is not the number of Ginkgo workers: some specs lease more than one container, so a few workers can hold many clusters. The peak is the smallest of three bounds: the total container demand of the specs the run selects (each spec declares its demand with `labels.MIContainers`), the number of containers leased to the slot, and the sum of the largest per-spec demands that the available workers could run at once. Written out:

```text
hcp-concurrency = min(
    sum of demand(spec) over the selected specs,
    leased container count of the slot,
    sum of the P largest demand(spec) values        # P = suite parallelism
)

res-scoped      = min(number of resource-scoped specs, hcp-concurrency)   # worst case
run-cost        = (hcp-concurrency - res-scoped) * RG_SCOPE_COST + res-scoped * RES_SCOPE_COST
```

The formula assumes pooled identities, the default in CI. With `POOLED_IDENTITIES=false` every cluster creates its own identities and role assignments, so nothing bounds the cost by the container count and the formula undercounts; do not size a catalog for runs like that.

To check that a subscription can support everything the catalog allows, add up the cost of every pool that shares it. Pools are separate in the catalog but share one quota, and `run-cost` differs between pools because their leased container counts differ:

```text
subscription-cost = sum over the subscription's pools of (run-cost(pool) * slot_count(pool))
assert subscription-cost + persistent-baseline + headroom <= role-assignment quota
```

The `persistent-baseline` is what the subscription already holds when idle: grants for people and automation, long-lived infrastructure, and the provisioned containers that are not leased. Measure it on an idle subscription (`az role assignment list --subscription <id> --all -o json | jq length`) instead of estimating it, since assignments that belong to running clusters cannot be told apart by scope. Leave about 100 assignments of headroom for other activity and for specs that create extra assignments of their own.

This is a worst case: it assumes every slot is running a suite at peak concurrency at the same moment. A result over quota means the catalog *permits* a breach, not that one has happened. The comment on the first DEV pool in the catalog shows the same calculation worked through for a real pool.

## Deny Assignments

STG and PROD live in the AME tenant, where the Resource Provider protects every cluster's managed resource group with deny assignments, and each of them excludes that cluster's own identities. Azure allows at most 2,000 deny assignments per subscription and the RP currently creates about 21 per cluster, so a subscription cannot hold more than roughly 92 clusters at once, whatever its role-assignment quota or its container count. For PROD this is the limit that sets the slot count:

```text
max-slots-per-sub = floor(92 / leased container count per slot)
```

Using the leased container count is deliberate. It overstates the clusters a run really creates, which errs on the side of safety. The catalog comment on the PROD pools records the result.

The unit that matters is the cluster. If Azure raises its limit on excluded principals, the RP can merge the deny assignments of a cluster into fewer, which raises the number of clusters a subscription can hold. Every cluster still needs at least one, so keep sizing per cluster even after that happens.

## Parallelism Within A Run

Suite parallelism limits how many specs run concurrently, not how many clusters exist. When pooled identities are on (the default in CI), a scheduler admits a spec only while the containers it declares are still available, so specs wait and run in waves, and a spec needing two containers consumes two. Adding workers therefore cannot get past the container ceiling, and specs that need no containers do not consume any. By default a suite's worker count follows the number of containers leased to the job, not the physical inventory. The details of how an explicit `ARO_HCP_SUITE_PARALLELISM` or `--max-concurrency` combines with that default are in the slot-manager design, under [suite parallelism](../../test/cmd/aro-hcp-tests/slot-manager/DESIGN.md#suite-parallelism-from-leased-identities).

## Changing Capacity

The catalog, the Boskos inventory in `openshift/release` and the Azure resources behind the assets must change together. The order depends on the direction and the kind of change. For slots, a slot can be leased only when Boskos lists it and the resources have to exist for as long as anything can lease them. If the slots involved are not yet in use by any job, the order does not matter.

**Increasing capacity.** This is for adding slots. Edit the pool in `test/e2e-config/e2e-slots.yaml`, provision, and publish to Boskos last.

1. Edit the pool. For anything that raises role-assignment or deny-assignment consumption, run the checks above first, and confirm the regional vCPU quota for the node shapes the tests use.
2. Provision the new capacity and validate it as described in [Managing Slot Assets](slot-assets.md#managing-slot-assets). Adding slots also grows the shared infrastructure identity pool, so include that asset.
3. Generate, merge and roll out the Boskos inventory.
4. After the first jobs run, confirm in the job artifacts that the slot, region and exported container list match the pool, and that the effective worker count is what you intended.

**Changing containers per slot.** This applies to `resource_group_count` on existing slots, which does not change the Boskos inventory, so nothing in Boskos stops a job from leasing containers that are not provisioned yet or are being deleted. A job uses the catalog of the commit it was started from. To raise the count, provision the extra containers and validate them before merging the catalog change. To lower it, merge the catalog change first, let jobs started on the old catalog drain (or pause the affected jobs), and only then deprovision, subject to the physical count notes below.

**Reducing capacity.** This is for removing slots. Boskos goes first, so that nothing can lease what is about to disappear; then the catalog changes; deprovisioning the assets is optional and comes last.

1. Merge and roll out the Boskos inventory generated from the reduced catalog, and let jobs that hold the old slots drain.
2. Merge the catalog change.
3. Only if you want the resources gone, deprovision them. Container resource groups are managed through deployment stacks that delete whatever the catalog no longer declares, so applying a lower count deletes real containers. Check that nothing still uses them first.

A pool can provision more containers than it leases (`identity_container_provisioning_count`). When that field is omitted, the physical count follows `resource_group_count`, so lowering the leased count in the catalog also lowers the physical target and the next apply would delete the extra containers. To keep them, write the previous physical count into `identity_container_provisioning_count` in the same catalog change that lowers the leased count, and lower it only after jobs on the old count have drained. Whole-pool validation checks the physical target, while admission and runtime use the leased count.

When the change affects an environment with other users of the subscription (the HyperShift pools, for example), check the combined demand of all the pools that share the quota, not just the pool you are editing.
