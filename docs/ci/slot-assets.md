# E2E Slot Assets

An E2E job does not lease a bare Boskos resource. It asks `slot-manager` for a *slot*, a complete E2E environment, and the slot comes with a set of *assets*: resources the environment needs in addition to the slot itself. This page explains what the three assets are, why each one exists, and how to operate them. The mechanics that apply to every asset (catalog format, acquire and release, admission, the runtime contract) are specified once in the [slot-manager design](../../test/cmd/aro-hcp-tests/slot-manager/DESIGN.md) and are not repeated here.

## The Three Assets

| Asset | What the job gets | Allocation | Where |
| :---- | :---------------- | :--------- | :---- |
| `e2e_identities` | Pre-created managed identities that tests reuse for every hosted cluster | Dedicated to the slot | Every pool |
| `mock_identities` | The service principals that stand in for the missing Microsoft first-party plumbing | Dedicated to the slot | DEV pools (`dev` and `dev-xs`) |
| `infrastructure_identities` | Pre-created managed identities for the on-demand service and management clusters | Leased from an asset pool | DEV pools |

A *dedicated* asset belongs to one slot for good and needs no lease of its own. A *leased* asset is its own Boskos inventory, acquired next to the slot and returned with it. The catalog in `test/e2e-config/e2e-slots.yaml` is the source of truth for which pool declares which asset and how many of each exist.

Every asset goes through *admission* when a slot is acquired. Admission verifies that the asset is fit for reuse and, for the two identity assets that accumulate leftovers, cleans what it can: role assignments for the E2E identities, and federated credentials and role assignments for the infrastructure identities. If admission fails, acquisition fails, so a job never starts on a broken asset. Releasing a slot returns leases but never deletes the persistent resources behind an asset.

## E2E Identities

Creating and deleting a full set of managed identities for every hosted cluster eats into the Azure directory quota. Deleted objects stay soft-deleted until a monthly purge, so heavy churn can leave a tenant unable to create new identities until the purge runs. To avoid that, CI keeps a fixed fleet of *identity containers*: resource groups, created ahead of time, each holding the complete identity set for one hosted cluster (the set itself is defined by the `managed-identities.bicep` module under `test/e2e-setup/bicep/`). Each slot owns a block of containers named from the pool's `resource_group_prefix`.

At runtime the slot publishes its containers to the job, and the test runner hands them out to specs according to the number each spec declares it needs. When a spec finishes, the runner removes the federated credentials and role assignments left on its containers and only then makes them available again. A container whose cleanup fails is not handed out again for the rest of that run, which keeps a bad container away from later specs in the same run. What it still holds is only partly recovered on the next acquire: admission deletes leftover role assignments but does not clean federated credentials, so a failed or aborted run can leave stale federated credentials on a container until someone removes them. Specs wait for a free container if none is available, which is why the number of containers, not the number of workers, bounds how many hosted clusters a run can have at once. Setting `POOLED_IDENTITIES=false` bypasses all of this and creates identities directly in each cluster's resource group; that is meant for local and ad-hoc runs.

Admission sweeps the same ground at the slot level: it checks that every container has its identities and deletes any role assignments left over from a previous run, including ones at child scopes. Because a leftover assignment counts against the subscription's role-assignment quota, this cleanup matters for [capacity](e2e-capacity.md#role-assignment-quota) as well as hygiene.

Containers are provisioned by `apply-pool-assets` (see [Managing Slot Assets](#managing-slot-assets)). A pool marks its E2E identities `provisioning: unmanaged` when another team owns the subscription and provisions the containers itself; that changes who runs the provisioning, not whether admission applies. The catalog also lets a pool provision more containers than it leases (`identity_container_provisioning_count`), which is how a reduction in leased capacity is staged safely; see [Changing Capacity](e2e-capacity.md#changing-capacity).

## Mock Identities

DEV has no Microsoft first-party application and no Managed Identities Data Plane, so CI substitutes ordinary Entra service principals for them. [DEV Mock Identities](dev-mock-identities.md) explains what each one stands in for and which roles it needs. This page covers how they attach to slots.

Each DEV slot owns three of them: one MSI mock and two ARM helpers, one for the backend and one for Clusters Service. Giving every slot its own set means concurrent jobs do not share the ARM read throttle of a single MSI mock or the CheckAccess request budget of a single helper. Earlier iterations achieved the same goal by leasing identities from Boskos pools; those pools have been retired, and nothing in the slot-managed path resolves them any more.

The identities are named after the customer subscription and the slot, not the region, so they stay the same when a pool's regions change. They are created ahead of time by an operator with subscription Owner, not by CI. Admission only reads Graph, the public certificates in Key Vault and the subscription role assignments, and fails if anything is missing or wrong; it never creates, repairs or rotates. The job receives a non-secret bundle with the client IDs, principal IDs and certificate names of the three principals, and the services read the certificates from the shared service Key Vault. Personal development environments are not slot-managed and keep using the shared identities.

Preparing and maintaining these identities is covered in [Slot Mock Identities](dev-mock-identities.md#slot-mock-identities).

The release-side `aro-hcp-local-e2e` workflow still declares the old MSI-mock and ARM-helper leases. They no longer select any identity; until they are removed they only act as a concurrency gate on DEV jobs.

## Infrastructure Identities

A DEV job provisions its own service cluster and one or more management clusters, and each of those needs AKS control-plane, workload and image-puller identities. Creating them per run would burn directory quota the same way per-cluster identities do. Instead, each slot leases an *identity bundle* (one resource group for the service cluster and one for each of two management stamps, with the identities already inside; a job uses only as many stamps as its environment deploys, and `dev-xs` (`ci02`) deploys one) from the `dev-infrastructure-identities` asset pool, which lives in the environment's infrastructure subscription. The pool's capacity is derived from the slots that consume it, so adding DEV slots grows the pool automatically.

The bundle reaches the deployment through the configuration override generator (`aro-hcp-tests infra build-config-override`, described in [CI Execution](execution.md#experimental-configuration-overrides)). When a bundle is present the generator turns on `infrastructureIdentities.useLeased`, and the deployment then references the existing identities rather than creating new ones. Without a bundle, which is the default for personal development and for the persistent environments, deployments create their identities as before. Two details are worth knowing. The AKS control-plane identity is named after the bundle, not the cluster, so changing a CI job's cluster name does not change which identity it uses. And the deployment still creates federated credentials for the new cluster's OIDC issuer and applies RBAC at the usual scopes, which is exactly what admission removes again before the bundle is reused.

## Managing Slot Assets

Three things have to agree: the catalog in `test/e2e-config/e2e-slots.yaml`, the Azure resources behind each asset, and the Boskos inventory in `openshift/release`. Different commands handle each, and none of them checks more than its own part.

**Azure resources.** `apply-pool-assets` reconciles the managed backing resources and `validate-pool-assets` checks them read-only.

```bash
make -C test apply-pool-assets    ENVIRONMENT=<dev|dev-xs|int|stg|prod> [POOL=<pool>] [SUBSCRIPTION=<display name>] [ASSET=<kind>]
make -C test validate-pool-assets ENVIRONMENT=<dev|dev-xs|int|stg|prod> [POOL=<pool>] [SUBSCRIPTION=<display name>] [ASSET=<kind>]
```

Both derive what to expect from the whole catalog and neither looks at Boskos, so they can succeed while the release inventory is stale. Unmanaged pools are skipped unless you select them explicitly with `POOL` or `SUBSCRIPTION`. Selecting by `ASSET` alone does not include them, and omitting `ASSET` runs every asset kind, which can touch the infrastructure subscription as well, so scope runs to the asset you mean to change. All of these commands need Owner on the E2E subscriptions, which are read-only for everyone else. The infrastructure identities live in the infrastructure subscription and are maintained by the ARO-HCP team.

Always go through the Make target. It rebuilds `aro-hcp-tests`, and the rebuild regenerates the ARM templates from the Bicep sources before embedding them. Those generated templates are git-ignored build output, so running a stale or hand-built binary applies an outdated template and shows up as resource groups being deleted and recreated instead of updated. (`apply-identity-pool` is an older alias that only handles E2E identities.)

**Boskos inventory.** `sync-boskos-config` rewrites the managed section of `generate-boskos.py` in a release checkout, but `validate-boskos-config` reads the generated `_boskos.yaml`, so the generator has to run in between:

```bash
./test/aro-hcp-tests slot-manager sync-boskos-config     --release-repo <release-checkout>
make -C <release-checkout> boskos-config
./test/aro-hcp-tests slot-manager validate-boskos-config --release-repo <release-checkout>
```

Commit the generator change together with the regenerated inventory.

**Order.** If none of the affected slots are in use, it does not matter. Otherwise it depends on what changes. The Boskos inventory follows the number of slots (`slot_count`), so for adding or removing slots Boskos is what makes a slot leasable: increases go catalog, then provisioning, then Boskos, and reductions go Boskos first, then the catalog, and deprovisioning the assets last and only if wanted. Changing the number of containers of existing slots (`resource_group_count`) leaves the inventory unchanged, so Boskos does not gate it: jobs follow whichever catalog they were started from. Provision before raising a leased count, and before deleting containers pause the affected jobs or let those on the old catalog drain. [Changing Capacity](e2e-capacity.md#changing-capacity) walks through both.

Container resource groups are managed through deployment stacks that delete anything they no longer declare. Reducing a pool or renaming its resource groups therefore deletes real resources, so check the catalog diff first.

Mock identities have additional requirements on top of Owner, because applying them also needs Microsoft Graph rights and Key Vault access; those are listed in [Slot Mock Identities](dev-mock-identities.md#slot-mock-identities).

## When Admission Fails

Acquire and release failures, partial state and manual lease recovery are covered by the design's [acquire, persist, fail, release](../../test/cmd/aro-hcp-tests/slot-manager/DESIGN.md#acquire-persist-fail-release) section; read it before returning a lease by hand. A few failures are specific to assets:

- **A container is missing or incomplete.** Reconcile just the affected subscription with `apply-pool-assets` (scoped by `SUBSCRIPTION` and `ASSET=e2e_identities`), then `validate-pool-assets`. If the apply itself fails, read the first nested Azure error rather than retrying; the usual causes are missing RBAC, an unregistered `Microsoft.ManagedIdentity` provider, quota exhaustion, or another deployment holding the stack.
- **Admission is throttled by ARM.** Cleaning hundreds of containers makes many Azure calls. As a temporary mitigation you can skip admission for one asset kind with `slot-manager acquire --disable-asset-admission=<kind>`. The binary also reads `ARO_HCP_DISABLE_ASSET_ADMISSION`, but the acquire step in `openshift/release` does not declare that parameter today, so using it from a job means adding it to the step first. Slot-manager logs a warning for each skipped asset, and skipped assets are not guaranteed to be clean, so remove the setting once the throttling passes. The persistent-environment workflow already lowers the number of cleanup workers for the same reason (`ARO_HCP_ASSET_ADMISSION_OPTIONS`).
- **Identities keep leaking role assignments or federated credentials.** Look at the container resource group directly in Azure. Admission removes leftover role assignments from E2E identity containers, so repeated leftovers there usually point to a permissions problem or an unexpected resource in the group. It does not remove federated credentials from them, so credentials left by an aborted run must be removed by hand.
- **A job fails with an empty `LEASED_MSI_CONTAINERS`.** The acquire step did not publish the runtime contract; start with the `aro-hcp-lease-acquire` logs.
