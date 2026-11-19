# External Subscription Onboarding

This is the runbook for a team that owns its own Azure subscription and wants to run ARO HCP E2E jobs in it. The HyperShift team's `Hypershift Managed Azure` subscription, which backs the `hypershift` pools in [`e2e-slots.yaml`](../../test/e2e-config/e2e-slots.yaml), is the existing example. It covers DEV pools in the Red Hat tenant. For subscriptions the ARO-HCP team owns, use [E2E Subscription Onboarding](e2e-subscription-onboarding.md) instead; a subscription in another tenant, such as the Test Tenant PROD subscription, is only touched on at the end of that page.

## How An External Subscription Differs

For a subscription we own, the privileged `dev-ci` rollout does the Azure-side work: it registers providers, grants the CI bot its roles, deploys the shared mock RBAC. Operators provision the slot assets separately. That rollout needs Owner on every subscription it manages, which an external team has no reason to hand to our automation, so an external subscription is deliberately left out of `ci.dev.e2eSubscriptions`. The pipeline never touches it, and its pools mark `slot_assets.e2e_identities.provisioning: unmanaged` in the catalog so that broad `apply-pool-assets` runs skip it as well.

The owning team therefore performs those steps itself, using the same Bicep templates and Make targets. The catalog entry still follows the usual [contract](../../test/cmd/aro-hcp-tests/slot-manager/DESIGN.md#catalog), and `unmanaged` changes who provisions the E2E identity containers and dedicated mock identities; it does not add a provisioning field to the mock asset. It does not exempt the pool from [admission](slot-assets.md#e2e-identities): every acquire still cleans and validates the leased identities, using the credentials of the cluster profile that owns the subscription, and a missing or broken asset fails the job.

| Piece | Who does it |
| :---- | :---------- |
| Catalog pool, Boskos inventory and cluster profile entries | ARO-HCP maintainers, in a pull request the subscription owner proposes or reviews |
| Quota requests, provider registration | Subscription owner |
| CI bot grants and the shared subscription RBAC template | Subscription owner |
| E2E identity containers and slot mock identities | Subscription owner, with the extra rights the mock identities need |
| Cleanup job for expired test resources | Subscription owner, in `openshift/release` |

## Before You Start

You need Owner on the subscription, the Azure CLI signed in as yourself, and a checkout of this repository at a recent revision, since the commands below use its templates and Make targets. Owner is needed because the templates create custom role definitions and role assignments. The CI bot itself deliberately never receives Owner.

The pool has to exist in the catalog before you provision anything, because `apply-pool-assets` derives what to create from it. Propose the entry in a pull request, modelled on the existing `hypershift` pools: the three DEV [slot assets](slot-assets.md), with `provisioning: unmanaged` on `e2e_identities`. That one setting is also what makes the slot mock identities count as externally prepared. See the [catalog contract](../../test/cmd/aro-hcp-tests/slot-manager/DESIGN.md#catalog) and, for sizing, [E2E Capacity And Quotas](e2e-capacity.md).

Quota is requested the same way as for any E2E subscription; see [Quota Requests](e2e-subscription-onboarding.md#quota-requests). If other workloads share the subscription, budget role assignments for the combined demand and not just for the pool, as described in [E2E Capacity And Quotas](e2e-capacity.md#role-assignment-quota).

Two of the commands need principal object IDs for service principals that live in the Red Hat tenant. The repository does not store those IDs (it records application names and resolves IDs through Microsoft Graph when it needs them), so look them up rather than copying values from a document. This helper reads the names from the DEV configuration and resolves them the same way the pipeline does:

```bash
CFG=config/config-dev-ci.yaml
cfg() { yq ".clouds.dev.defaults.ci.dev.$1" "$CFG"; }

# Entra applications are looked up by a normalized unique name: lowercase, spaces replaced with dashes.
principal_id() {
  local unique_name app_id
  unique_name="$(tr '[:upper:] ' '[:lower:]-' <<< "$1")"
  app_id="$(az ad app list --filter "uniqueName eq '${unique_name}'" --query '[0].appId' -o tsv)"
  az ad sp show --id "${app_id}" --query id -o tsv
}
```

It needs [Mike Farah's `yq`](https://github.com/mikefarah/yq) v4; the Python package of the same name uses different syntax. Use the object ID of the service principal, not the application (client) ID.

## 1. Register The Resource Providers

A new subscription has few providers registered, and later steps fail in ways that do not obviously point at registration. The providers E2E needs are listed under `ci.e2eSubscriptionProviders` in `config/config-dev-ci.yaml`, which is also what the privileged rollout registers for subscriptions we own, so register that same list:

```bash
SUBSCRIPTION_ID="<target-subscription-id>"
for ns in $(yq '.clouds.dev.defaults.ci.e2eSubscriptionProviders' "$CFG" | tr ',' ' '); do
  az provider register --namespace "$ns" --subscription "$SUBSCRIPTION_ID"
done
```

Registration is asynchronous. Poll each provider with `az provider show --namespace "<ns>" --subscription "$SUBSCRIPTION_ID" --query registrationState -o tsv` until every one reports `Registered`, and include `Microsoft.Authorization`, which is registered by default but also has to be in that state. The backend refuses to provision a cluster until `Microsoft.Authorization`, `Microsoft.Compute`, `Microsoft.Network` and `Microsoft.Storage` are registered on the customer subscription, and the cluster stays in its retry loop until they are.

## 2. Grant The CI Bot

The DEV `OpenShift Release Bot` is the identity that runs the tests: it creates resource groups, deploys templates and assigns roles to the managed identities of each cluster. Give it its roles with the same template the pipeline uses for subscriptions it owns, which grants Contributor, Role Based Access Control Administrator with a condition that stops it from handing out Owner, User Access Administrator or RBAC Administrator, and, as the DEV configuration does for every E2E subscription, Azure Kubernetes Service RBAC Cluster Admin:

```bash
az deployment sub create \
  --subscription "$SUBSCRIPTION_ID" \
  --location westus3 \
  --template-file dev-infrastructure/templates/ci-bot-rbac-subscription.bicep \
  --parameters \
    botPrincipalId="$(principal_id "$(cfg bot.applicationName)")" \
    isGlobalSubscription=false \
    grantAksRbac=true
```

The slot's cluster profile must hold this bot's credentials, which the ARO-HCP maintainers confirm when they add the profile entries in step 5. These ARM grants do not supply Graph reads or shared-vault certificate/secret access. Coordinate those read permissions and runtime certificate delivery with the tenant and vault maintainers before publishing the pool; do not weaken the RBAC condition to enable operator-only repair.

## 3. Deploy The Subscription RBAC Template

`e2e-subscription-rbac-assignment-subscription.bicep` defines custom roles inside the subscription and assigns them to the DEV mock identities. This matters for slot jobs because the slot mock identities in the next step reuse the subscription-local `dev-msi-mock-<subscription-id>` role from this template and do not create it, so apply fails until the template has been deployed. [DEV Mock Identities](dev-mock-identities.md#how-the-shared-identities-are-set-up) explains what each role is for.

The template declares the object IDs of the three shared mock identities as required parameters, so all three are passed. E2E jobs use the slot-bound ARM helpers and MSI mock described in [DEV Mock Identities](dev-mock-identities.md#slot-mock-identities), not the shared ones, so only the shared first-party mock matters for them. The shared ARM helper and MSI mock are what personal development environments authenticate as. Keep the optional Clusters Service helper and role-name parameters at their defaults for this DEV bootstrap.

```bash
az deployment sub create \
  --subscription "$SUBSCRIPTION_ID" \
  --location westus3 \
  --template-file dev-infrastructure/templates/e2e-subscription-rbac-assignment-subscription.bicep \
  --parameters \
    firstPartyPrincipalId="$(principal_id "$(cfg mockIdentities.firstParty.applicationName)")" \
    armHelperPrincipalId="$(principal_id "$(cfg mockIdentities.armHelper.applicationName)")" \
    miMockPrincipalId="$(principal_id "$(cfg mockIdentities.msiMock.applicationName)")"
```

Keep the role names at their defaults. The custom role names carry the subscription ID, which keeps them unique across the tenant, and the slot code finds the MSI mock role by that exact name.

## 4. Provision The Slot Assets

Each pool needs its E2E identity containers and its slot mock identities. Run both scoped to the pool and its subscription, with an explicit `ASSET` each time. Selecting by pool or subscription is what includes an unmanaged pool, and omitting `ASSET` would also run the infrastructure identity asset against the infrastructure subscription, which the ARO-HCP team maintains. See [Managing Slot Assets](slot-assets.md#managing-slot-assets) for the details and for why the Make target is mandatory.

```bash
AROHCP_ENV=development make -C test apply-pool-assets    ENVIRONMENT=dev-xs POOL=hypershift SUBSCRIPTION="<display name>" ASSET=e2e_identities
AROHCP_ENV=development make -C test validate-pool-assets ENVIRONMENT=dev-xs POOL=hypershift SUBSCRIPTION="<display name>" ASSET=e2e_identities
```

A subscription can back pools in more than one slot environment, as the HyperShift one does in both `dev` and `dev-xs`, and each pool has its own containers, so repeat the commands with the other `ENVIRONMENT` and `POOL` values.

The mock identities are a different kind of operation. They create Entra applications and Key Vault certificates, which Owner on the subscription does not authorize, so the person running them needs Microsoft Graph rights and access to the shared service Key Vault as well. Apply also makes the Entra owners of those applications match the configured owner list, which can remove the operator's own access. Read [Preparing A Slot's Identities](dev-mock-identities.md#preparing-a-slots-identities) for the preconditions and the command, and run it with `POOL` and `SUBSCRIPTION` both set. In practice the owning team and an ARO-HCP operator work through this step together. Owner is never granted to the CI bot to get around the permission requirements.

Only the customer subscription receives role assignments from these commands. The infrastructure identity bundles slots lease live in the environment's infrastructure subscription and are maintained by the ARO-HCP team. Because each new slot raises the demand on that shared pool, an ARO-HCP operator must apply and validate `ASSET=infrastructure_identities` before the pool is published. Otherwise Boskos would offer bundles that have no resource groups behind them, and every DEV job sharing the pool could fail admission.

## 5. Publish The Pool

Once the assets validate, the ARO-HCP side makes the pool leasable. A maintainer adds two entries to the cluster profile secret that holds the DEV bot's credentials: `customer-<shard>-subscription-name` with the subscription's exact display name, which is how slot-manager matches the pool to credentials, and `customer-<shard>-subscription-id`, which the cleanup sweepers use to discover subscriptions. Then they generate and validate the Boskos inventory (`sync-boskos-config`, the release checkout's `make boskos-config`, then `validate-boskos-config`, see [Managing Slot Assets](slot-assets.md#managing-slot-assets)) and merge the `openshift/release` change, which also adds the subscription's exact display name to the `ALLOWED_SUBSCRIPTIONS` of the jobs that should use it (without that, the new pool stays idle); [E2E Subscription Onboarding](e2e-subscription-onboarding.md#dev-e2e-subscription-onboarding) describes both steps. Only the inventory change makes slots leasable, so it goes last.

## 6. Add A Cleanup Job

Resource groups left behind by failed or timed-out tests are deleted by periodic jobs, one per customer subscription. For an external subscription the owning team adds its own in `openshift/release`; HyperShift defines theirs in its own ci-operator configuration, in the `arohcp-cleanup` variant of `openshift/hypershift`. The job runs under the DEV profile, which is why the bot needs the grants from step 2:

```yaml
- as: delete-expired-arohcp-e2e-resource-groups
  cron: 35 * * * *
  steps:
    env:
      CLEANUP_MODE: no-rp
      CUSTOMER_SUBSCRIPTION: <subscription display name>
    test:
    - ref: aro-hcp-deprovision-expired-resource-groups
```

`no-rp` is the right mode for DEV, where the job that created a resource group may already have torn down its RP. [CI Cleanup](cleanup.md#why-dev-cleanup-actively-deletes-managed-resource-groups-but-public-cloud-cleanup-does-not) explains the difference. Because the subscription is not in `ci.dev.e2eSubscriptions`, the `createdAt` tag policy that the policy-driven `rg-ordered` sweeper relies on is not assigned for you, so this per-subscription job is the primary cleanup.

Finish with a rehearsal job that targets the pool. It should acquire a slot without admission errors, create resources in the subscription, and release the slot cleanly.

## Maintenance

Redeploy the templates from steps 2 and 3 when they change, for instance when a new role or mock principal is added, and then rerun `validate-pool-assets` for the pool, because the slot mock identities' role assignments depend on the roles that template defines. Rerun it too after any catalog, owner-group or certificate change, and before putting drained capacity back into service. Certificate rotation for slot identities is disruptive and is described in [Slot Identity Certificates](dev-mock-identities.md#slot-identity-certificates).

Changing a pool's size touches resources in your subscription, and the identity containers are managed through deployment stacks that delete whatever they no longer declare. Raise capacity by provisioning first and publishing second. To lower it, follow [Changing Capacity](e2e-capacity.md#changing-capacity), which stages the reduction so that containers are not deleted while a job may still be using them.

To retire a pool, stop new leases and let running jobs drain first. Then remove the slot's backing resources and grants deliberately: releasing a slot keeps its containers, mock identities and role assignments, and lowering a slot count or renaming a slot does not delete what it leaves behind. Renaming a slot changes the names of its mock identities, which orphans the old ones. Do not delete an identity or certificate while a lease still depends on it.

## See Also

- [E2E Subscription Onboarding](e2e-subscription-onboarding.md) for the procedure for subscriptions the pipeline owns
- [E2E Slot Assets](slot-assets.md) and [DEV Mock Identities](dev-mock-identities.md) for what the assets are and how to provision them
- [Dev-CI Topology](dev-ci-topology.md#the-privileged-entrypoint) for the rollout that does this work for subscriptions we own
