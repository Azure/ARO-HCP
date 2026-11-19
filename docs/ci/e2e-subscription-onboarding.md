# E2E Subscription Onboarding

This page is the procedure for adding a customer subscription to the E2E fleet: where the subscription is registered, what Azure needs to know about it, and what has to exist before CI can lease slots from it. It covers [DEV](#dev-e2e-subscription-onboarding), where onboarding is purely CI infrastructure, and [INT, STG and PROD](#intstgprod-e2e-subscription-onboarding), which are ARM-integrated and additionally need AFEC feature flags. Subscriptions owned by another team follow [External Subscription Onboarding](external-subscription-onboarding.md) instead.

## What Onboarding Touches

A subscription is usable for E2E when several systems that live in different places agree about it:

- the [slot catalog](../../test/e2e-config/e2e-slots.yaml) in this repository, which declares the pool and its [assets](slot-assets.md)
- the `dev-ci` configuration (`config/config-dev-ci.yaml`), which is the inventory the privileged rollout uses to register providers, grant the CI bot access and monitor quota
- the Azure subscription itself: quota, registered providers, role assignments and, outside DEV, AFEC flags
- the asset resources that back the slots, which are created with `apply-pool-assets`
- `openshift/release`, which holds the Boskos inventory, the cleanup job for the subscription, and (in the CI secret store) the cluster profile secret that tells slot-manager which credentials own the subscription

The order matters in one respect. A slot becomes leasable the moment the Boskos inventory change merges, so that change goes last, after the Azure side and the asset resources exist. [Changing Capacity](e2e-capacity.md#changing-capacity) explains the same ordering for an existing pool.

## Quota Requests

Every environment needs the same Azure quota before a pool can run at its intended size. These are requests to Azure rather than values derived from the catalog, so ask for them up front, since approval can take days. In every region the pool runs in, request:

- total regional vCPUs: 2000
- vCPUs for a worker VM family the CI configuration supports: 2000. DSv6 and DSv5 are the defaults; a pool's `vm_family_policy` can name others (see [VM sizes](../../test/e2e/README.md#selecting-vm-sizes-skus)). Limits are per family and are not interchangeable.
- Standard IPv4 public IP addresses: 3000

Then request a subscription-wide role assignment limit of 8000, which is the quota [E2E Capacity And Quotas](e2e-capacity.md#role-assignment-quota) assumes. If a self-service request fails, open a quota support request that lists the affected regions and the failed request IDs. Submitting through the support API needs an eligible support plan; without one, use the Azure portal.

## DEV E2E Subscription Onboarding

DEV has no ARM integration, so a new subscription needs no feature flags and no routing. Its slots belong to the `dev` environment in the catalog, which maps to the `ci01` deployment environment (`dev-xs` maps to `ci02`).

1. **Add the pool to the catalog.** In `test/e2e-config/e2e-slots.yaml`, add a pool to the environment following the [catalog contract](../../test/cmd/aro-hcp-tests/slot-manager/DESIGN.md#catalog): a unique pool name and `resource_type`, the subscription's display name under `subscriptions.e2e`, and the three [slot assets](slot-assets.md) every DEV pool declares. Copy the shape of an existing DEV pool, keep the shard number in the name and the identity-container prefix consistent with the others, and choose `slot_count` and `resource_group_count` with [E2E Capacity And Quotas](e2e-capacity.md) open, since a new subscription brings its own role-assignment budget.

2. **Request the quota** described [above](#quota-requests).

3. **Register the subscription with `dev-ci` and run the privileged rollout.** Append `<subscription-id>=<display name>` to the comma-separated `ci.dev.e2eSubscriptions` string in `config/config-dev-ci.yaml`. The display name cannot contain commas, equals signs, quotes, backslashes, `$` or backticks, and the config schema rejects such names. Check that `ci.dev.regions` covers the regions the pool runs in, because the tenant-quota collector derives its subscriptions and regions from the same keys and needs no entry of its own.

   Then ask an OWNERS-group member to run the [privileged entrypoint](dev-ci-topology.md#the-privileged-entrypoint) from the repository root:

   ```bash
   make dev-ci-privileged-local-run
   ```

   For the new subscription this registers the resource providers listed in `ci.e2eSubscriptionProviders`, grants the CI bot its roles, assigns the `createdAt` resource-group tag policy that the periodic cleanup depends on, turns on Cost Management tag inheritance, and deploys the subscription RBAC for the [shared mock identities](dev-mock-identities.md#how-the-shared-identities-are-set-up). That last template also defines the subscription-local `dev-msi-mock-<subscription-id>` role, which the slot mock identities in step 5 reuse and cannot create themselves. The operator needs Owner on the subscription and, for tag inheritance, permission to edit Cost Management settings there.

   Provider registration deserves a word, because forgetting it produces confusing failures. A brand-new subscription has almost nothing registered. The portal's quota blade says the selected provider is not registered, and provisioning and role assignment then fail in ways that do not point at registration. The rollout registers the providers idempotently, so there is nothing to do by hand for a subscription we own. When a new Azure dependency appears, add its provider to `ci.e2eSubscriptionProviders` and rerun the rollout.

4. **Add the subscription to the cluster profile secret.** Slot-manager finds the credentials that own a subscription by looking for the subscription's display name in the `customer-<shard>-subscription-name` entry of the selected cluster profile, and the cleanup sweepers discover their targets from the matching `customer-<shard>-subscription-id` entry. Add both entries to the `aro-hcp-dev` profile in the `hcm-aro` collection of the CI secret store, with `<shard>` matching the pool's shard. The mapping from shard to subscription must not change once jobs rely on it.

5. **Provision the assets.** Create the E2E identity containers and the slot mock identities for the new pool, then validate them. Adding slots also raises the demand on the shared infrastructure identity pool, so an ARO-HCP operator applies and validates that asset too (`ASSET=infrastructure_identities`) before anything is published. Otherwise Boskos would offer bundles whose resource groups do not exist and jobs sharing the pool would fail admission. See [Managing Slot Assets](slot-assets.md#managing-slot-assets) for the commands and why they must go through `make`:

   ```bash
   make -C test apply-pool-assets    ENVIRONMENT=dev POOL=<pool> ASSET=e2e_identities
   make -C test validate-pool-assets ENVIRONMENT=dev POOL=<pool> ASSET=e2e_identities
   ```

   Use `ENVIRONMENT=dev-xs` for a `dev-xs` pool. The mock identities are a separate, privileged run with its own prerequisites (Owner, Microsoft Graph rights and Key Vault access) and are run one pool at a time; follow [Preparing A Slot's Identities](dev-mock-identities.md#preparing-a-slots-identities) rather than reusing the command above with a different `ASSET`.

6. **Publish the Boskos inventory and the cleanup job in `openshift/release`.** Generate and validate the inventory as described in [Managing Slot Assets](slot-assets.md#managing-slot-assets): `sync-boskos-config`, then the release checkout's `make boskos-config`, then `validate-boskos-config`. Run `make update` in the release checkout too. Slots are only used by jobs that are allowed to select them, and the E2E jobs list their subscriptions explicitly in `ALLOWED_SUBSCRIPTIONS`, so add the subscription's exact display name to the lists of the jobs that should consume it, in the same pull request. In the same pull request, add a periodic expired-resource-group job for the subscription next to the existing ones in `Azure-ARO-HCP-main__periodic-cleanup.yaml`; see [Identifying the cleanup job for a subscription](cleanup.md#identifying-the-cleanup-job-for-a-subscription). Merge, then wait for the Boskos configuration to roll out.

7. **Validate end to end.**
   - Read back the Cost Management setting with `GET /subscriptions/<id>/providers/Microsoft.CostManagement/settings/taginheritance?api-version=2025-03-01` and check that `properties.preferContainerTags` is `false`. A missing setting means onboarding is incomplete. Inherited tags show up in Cost Management usage records, not on the Azure resources, and the records can take 8 to 24 hours to update.
   - Run a rehearsal job that targets the new pool and confirm in its artifacts that the slot, region and container list are the pool's, that customer resources are created without `AuthorizationFailed` errors, and that release returns the slot.

Tag inheritance is applied through the REST API by `dev-infrastructure/scripts/e2e-enable-cost-tag-inheritance.sh` instead of an ARM deployment because the settings API stores the setting but omits `kind` from its GET response, which makes ARM report a failed deployment after the write has succeeded. The script writes the setting and then verifies it. Only DEV hosted-cluster subscriptions and the opted-in E2E infrastructure subscription (`ci.dev.infrastructureSubscriptionsCostTags`) get tag inheritance.

## INT/STG/PROD E2E Subscription Onboarding

INT, STG and PROD are ARM-integrated. Each runs its own RP, and ARM decides which one receives a `Microsoft.RedHatOpenShift` request from the AFEC (Azure Feature Exposure Control) flags registered on the customer subscription. Without the right flags a subscription's calls never reach the RP the tests are meant to exercise. [Which environment does my subscription use?](../environments.md#which-environment-does-my-subscription-use) explains the routing and how to inspect a subscription's flags.

Onboarding therefore has two parts: registering the flags, which is Azure-side and needs Microsoft approval, and the CI infrastructure setup.

### Required AFEC Flags

| Environment | Flags to register |
| :---------- | :---------------- |
| INT | `INT-APPROVED`, `ExperimentalReleaseFeatures` |
| STG | `STAGING-APPROVED`, `ExperimentalReleaseFeatures` |
| PROD | `ExperimentalReleaseFeatures` only; PROD is selected by registering neither environment flag |
| PROD, canary regions only | additionally `InProgress` and `Microsoft.Resources/EUAPParticipation`, see [EUAP/Canary Region Access](#euapcanary-region-access) |

All of these are in the `Microsoft.RedHatOpenShift` namespace except `EUAPParticipation`. `INT-APPROVED` and `STAGING-APPROVED` select the environment, `HcpPrivatePreview` does not (it only exposes older preview API versions), and `ExperimentalReleaseFeatures` makes the RP honour the `aro-hcp.experimental.*` tags the E2E tests use, so without it tests that rely on those tags fail.

> [!IMPORTANT]
> `InProgress` only controls ARM routing. It does not grant the subscription access to a canary region. Deploying into `eastus2euap` or `centraluseuap` also needs `Microsoft.Resources/EUAPParticipation`, which has a different approval path.

### Registering AFEC Flags

Registration is two-sided: you ask for the flag from the subscription, and someone with Microsoft-side authority approves it. Only the first half needs access to the subscription.

1. From the subscription's tenant, register each required flag. This leaves it pending approval:

   ```bash
   az feature register --namespace Microsoft.RedHatOpenShift --name <flag-name> \
     --subscription <subscription-id>
   ```

2. Make sure you have access to run Geneva Actions for this feature. Access is restricted; request it through the internal access process rather than from this page.

3. In Geneva Actions, open Azure Resource Manager, Feature Management, Approve Feature Registration. Enter namespace `Microsoft.RedHatOpenShift`, the subscription ID, and the names of the flags you registered in step 1.

4. Verify from the subscription's tenant that every flag reads `Registered`:

   ```bash
   az feature list --namespace Microsoft.RedHatOpenShift -o table --subscription <subscription-id>
   ```

### EUAP/Canary Region Access

A subscription that runs E2E in a canary region (`eastus2euap`, `centraluseuap`) needs `Microsoft.Resources/EUAPParticipation` registered on top of the flags above. Without it, creating any resource group in the region fails with `LocationNotAvailableForResourceGroup`.

This flag is `ManualApprove`. `az feature register` only moves it to `Pending`, and nothing completes it on its own: either a LionRock fulfillment or a Geneva Action approves it. File the LionRock request first and fall back to Geneva Actions if it stalls.

#### Step A: Initiate The Registration

The flag stays `Pending` afterwards, which is expected.

```bash
az feature register --namespace Microsoft.Resources --name EUAPParticipation \
  --subscription <subscription-id>
```

#### Step B: File A LionRock Region-Access Request

This is the supported self-service path. File it from a b-account (an elevated account that is a Service Tree owner), one request per subscription and canary region, and ask for region access only. Do not bundle SKU or quota line items: a single gated item, GPU families in particular, holds up the whole request, including the region-access line.

#### Step C: Approve Through Geneva Actions If The Request Stalls

Move to this when the request reaches *Action Required* or is backlogged. `Microsoft.RedHatOpenShift` is not treated as in-scope first-party for subscriptions outside the Microsoft tenant, so LionRock handles them as external subscriptions and routes fulfillment to a manual RDQuota queue where it typically stalls. For Red Hat tenant subscriptions, treat that stall as the expected outcome and do not wait weeks for it. The request's activity log shows the pattern:

- `AutoApprove` reports *"Requestor service is not in scope. Auto approve ARM requests for external subscriptions. Grant WAEAP approval."* WAEAP approval looks like progress, but it is not the flag registration.
- `CheckAutoComplete` repeats *"AFEC flag for region access is not registered. Proceeding to fulfillment.."*
- `Fulfill` goes to manual RDQuota and ICM and ends in *Action Required*, often with a generic *"The requested SKU/Region combination is not supported"* comment and eventually a backlog note about *"high demand for virtual machines in this region"* that has nothing to do with the real problem.

To register the flag directly, a Microsoft operator with the required access runs the Geneva Action Azure Resource Manager, Feature Management, Approve Feature Registration, with resource provider `Microsoft.Resources`, feature name `EUAPParticipation` and the subscription ID. Access to that action is restricted, so find out who holds it and how to request it from the internal access documentation rather than from this page.

The Geneva Action registers the flag whatever state the LionRock request is in, so there is no need to cancel the request first. Do close it afterwards: an open request blocks new ones for the same subscription and region, and you need those for quota.

#### Step D: Verify, Then Register The Provider

```bash
az feature show --namespace Microsoft.Resources --name EUAPParticipation \
  --subscription <subscription-id> --query properties.state -o tsv   # expect: Registered
az provider register --namespace Microsoft.Resources --subscription <subscription-id>
```

Region access is separate from quota. Once the flag is registered, request the per-region [quota](#quota-requests) for the canary region, and file any GPU SKU (for example `NCasT4v3`) as its own request. GPU families are gated by product-manager pre-approval, which otherwise blocks the whole bundle.

### CI Infrastructure Setup

1. **Register the subscription with `dev-ci`.** Append `<subscription-id>=<display name>` to the comma-separated `ci.<env>.e2eSubscriptions` string in `config/config-dev-ci.yaml`, and make sure `ci.<env>.regions` covers the pool's runtime regions. The same inventory drives provider registration, the CI bot's role assignments and the quota-collector's Reader grant, so no other list needs editing. Display name restrictions are the same as for DEV.

2. **Run the privileged rollout.** An OWNERS-group member with Owner on the target subscriptions runs `make dev-ci-privileged-local-run`, as described for [DEV](#dev-e2e-subscription-onboarding) and in [the privileged entrypoint](dev-ci-topology.md#the-privileged-entrypoint). For INT, STG and PROD it registers the providers, grants the environment's bot (for example `OpenShift Release Bot - STG`) its roles on the subscription, and assigns the `createdAt` tag policy to DEV, STG and PROD subscriptions. For INT it also deploys the subscription RBAC for the INT mock identities. Neither tag inheritance nor slot mock identities apply outside DEV.

3. **Request the quota** from [above](#quota-requests). In STG and PROD, which run in the AME tenant, also check the [deny-assignment limit](e2e-capacity.md#deny-assignments) when you choose the pool's slot count.

4. **Add the pool to the catalog.** Add it under the environment's `pools` in `test/e2e-config/e2e-slots.yaml` following the [catalog contract](../../test/cmd/aro-hcp-tests/slot-manager/DESIGN.md#catalog), with a unique pool name, `resource_type` and `resource_group_prefix`. These environments declare only `e2e_identities`. A pool with `region_mode: runtime-selected` uses `region` as the default and lets the job's location override pick the runtime region; a `regions` list is valid only for weighted pools.

5. **Add the subscription to the cluster profile secret.** Add the `customer-<shard>-subscription-name` and `customer-<shard>-subscription-id` entries described in the [DEV procedure](#dev-e2e-subscription-onboarding) to the environment's profile in the `hcm-aro` collection (`aro-hcp-int-rh`, `aro-hcp-stg-rh` or `aro-hcp-prod-rh`).

6. **Provision the E2E identity containers.** Scope the run to the new pool and validate it:

   ```bash
   make -C test apply-pool-assets    ENVIRONMENT=<int|stg|prod> POOL=<pool> ASSET=e2e_identities
   make -C test validate-pool-assets ENVIRONMENT=<int|stg|prod> POOL=<pool> ASSET=e2e_identities
   ```

7. **Publish the Boskos inventory and the cleanup job.** As in step 6 of the DEV procedure, run `sync-boskos-config` and `validate-boskos-config`, add the subscription's exact display name to the `ALLOWED_SUBSCRIPTIONS` of the jobs that should use it, add its expired-resource-group job to `Azure-ARO-HCP-main__periodic-cleanup.yaml` and merge. The INT, STG and PROD cleanup jobs select the environment's profile with `VAULT_SECRET_PROFILE`; copy an existing job for the same environment.

8. **Validate** with a rehearsal E2E job that targets the new subscription.

A subscription in a different tenant, such as the Test Tenant PROD subscription, is outside what the privileged rollout can reach. Its owners register providers and grant access themselves, its E2E identities are `provisioning: unmanaged`, and its quota monitoring is declared under `opstool.tenantQuota.tenants.testTestAro` in the config, with its Reader grant created by `tooling/tenant-quota/scripts/manage-service-principals.sh`. The flags above still apply to it.

## See Also

- [E2E Slot Assets](slot-assets.md) and [DEV Mock Identities](dev-mock-identities.md) for provisioning the assets a pool needs
- [E2E Capacity And Quotas](e2e-capacity.md) for sizing the pool and changing it later
- [Dev-CI Topology](dev-ci-topology.md) for what the privileged rollout owns
- [CI Cleanup](cleanup.md) for the cleanup jobs
- [Tenants and Environments](../environments.md) for how AFEC flags select an environment
