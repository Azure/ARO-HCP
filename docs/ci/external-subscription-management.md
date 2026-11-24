# External Subscription Management

Use this procedure to onboard and maintain an externally owned subscription used by ARO HCP DEV E2E jobs in the Red Hat tenant. The HyperShift team's subscription is an example. For subscriptions owned by ARO-HCP, use [E2E Subscription Onboarding](e2e-subscription-onboarding.md). Cross-tenant PROD subscriptions are not supported by this command.

External subscriptions deliberately stay outside `ci.dev.e2eSubscriptions`: our privileged rollout does not have Owner on them. Their catalog pools declare `slot_assets.e2e_identities.provisioning: unmanaged`. That changes who maintains the backing resources, not the requirement to pass slot admission. The owning team runs one command from an updated, trusted checkout instead of translating template and tooling changes into a new sequence of manual steps.

## Before You Start

These steps do not depend on the subscription being prepared. Ask for them before anything else is configured or provisioned:

1. Propose the pool in the [slot catalog](../../test/e2e-config/e2e-slots.yaml), with the external E2E identity asset marked unmanaged. Budget quota for every workload sharing the subscription; see [E2E Capacity And Quotas](e2e-capacity.md) and [Quota Requests](e2e-subscription-onboarding.md#quota-requests).
2. Ask an ARO-HCP operator to create the slot mock identities, which needs Entra application and Key Vault certificate rights that subscription Owner does not provide. For an unmanaged pool the operator's apply creates only the identities and grants no roles on the subscription, so no access to it is needed: the operator cannot see the subscription. Give the operator the subscription's UUID, which is not a secret; the slot identity names are derived from it, so the operator takes it instead of resolving the display name:

   ```bash
   AROHCP_ENV=development make -C test apply-pool-assets ENVIRONMENT=dev-xs POOL=hypershift SUBSCRIPTION="<subscription display name>" EXTERNAL_SUBSCRIPTION_ID=<subscription UUID> ASSET=mock_identities
   ```

   See [Preparing A Slot's Identities](dev-mock-identities.md#preparing-a-slots-identities). Credential or owner changes are disruptive; follow [Slot Identity Certificates](dev-mock-identities.md#slot-identity-certificates).
3. Ask an ARO-HCP operator to provision and validate the shared infrastructure identity capacity. It lives in the infrastructure subscription, which this command never modifies. See [Managing Slot Assets](slot-assets.md#managing-slot-assets).
4. Ask an ARO-HCP maintainer to add the subscription's exact display name and UUID to the DEV cluster profile's `customer-<shard>-subscription-name` and `customer-<shard>-subscription-id` entries, and to confirm that the profile contains the configured DEV bot's credentials.

## Prepare Or Repair The Subscription

The subscription owner runs the command once the mock identities exist. Prerequisites:

- Owner on the subscription, or an equivalent that can create role definitions and role assignments. If your access is eligible through PIM, activate it first.
- Microsoft Graph read access and read access to the shared mock identities' public certificates in the service Key Vault. Owner does not grant them; arrange them with the ARO-HCP operators.
- The latest `main` of this repository.

From a developer workstation, with `az login`:

- `az login --tenant 64dc69e4-d083-49fc-9569-ebece1dd1408`

From automation, with a client secret:

- `export AZURE_CLIENT_ID=<id> AZURE_CLIENT_SECRET=<secret> AZURE_TENANT_ID=64dc69e4-d083-49fc-9569-ebece1dd1408`

Then, from the repository root:

```bash
make -C test reconcile-external-subscription SUBSCRIPTION="<subscription display name>"
```

`SUBSCRIPTION` is the display name declared in the catalog, as for `apply-pool-assets`. The Make target compiles the canonical subscription RBAC Bicep templates and rebuilds the command so the embedded templates are fresh; always use it rather than an independently built binary. The command discovers every `dev` and `dev-xs` pool that uses the subscription, so HyperShift's pools in both environments are handled by one invocation. It then runs, in order:

1. Registers the resource providers listed in `ci.e2eSubscriptionProviders` and waits for them.
2. Deploys the CI bot grants and the shared mock RBAC (`ci-bot-rbac-subscription.bicep`, `e2e-subscription-rbac-assignment-subscription.bicep`). These are the same modules the dev-ci rollout uses for subscriptions it owns, and they deploy extra custom roles alongside the mock identity RBAC. Deployments are incremental and never remove an existing assignment.
3. Applies the E2E identity containers of each pool with the same code as `apply-pool-assets ASSET=e2e_identities`.
4. Grants the existing slot mock identities their subscription roles.
5. Validates the containers and the grants.

Every step is idempotent, so rerun the command after a failure. The CI bot never receives Owner. The command does not create Entra applications, certificates or owners; if a slot's mock identities do not exist yet it fails at step 4 and points at the operator workflow in [Before You Start](#before-you-start).

Not every step is additive. Each slot has its own identity container deployment stack, named `aro-hcp-slot-msi-pool-<slot resource name>`, and that stack deletes whatever it no longer declares. A run after a catalog change that lowers the number of containers of a slot that still exists (`resource_group_count`, or `identity_container_provisioning_count` when set) therefore deletes containers. Reduce that count only through the drained procedure in [Changing Capacity](e2e-capacity.md#changing-capacity), before the catalog change reaches the checkout you run from.

Removing or renaming slots is different: the command only visits the slots the catalog declares. Lowering `slot_count` or renaming a slot never touches the stack of a slot that is no longer declared; its containers stay in Azure and keep using quota. A rename also creates a new stack with new containers next to the old one. Nothing removes them automatically. After the Boskos and catalog changes of [Reducing capacity](e2e-capacity.md#changing-capacity) have drained, find the stale stacks with `az stack sub list` and delete each one deliberately with `az stack sub delete --name <stack name> --action-on-unmanage deleteAll`.

## Publish A Pool

The command prepares Azure; publishing CI capacity still needs coordination:

1. Generate and validate Boskos inventory and propose the `openshift/release` changes, including the subscription display name in the relevant jobs' `ALLOWED_SUBSCRIPTIONS`. Publish inventory only after backing assets are ready; see [DEV E2E Subscription Onboarding](e2e-subscription-onboarding.md#dev-e2e-subscription-onboarding).
2. Add the owning team's cleanup periodic in `openshift/release`. Use `aro-hcp-deprovision-expired-resource-groups` with `CLEANUP_MODE: no-rp` and `CUSTOMER_SUBSCRIPTION` set to the display name. HyperShift keeps this in its `arohcp-cleanup` variant. Because the subscription is not in the managed inventory, do not assume the managed subscriptions' tag-policy sweeper covers it; see [CI Cleanup](cleanup.md).
3. Run a rehearsal E2E job against the pool. Confirm slot acquisition, resource creation and clean release.

The command does not edit cluster-profile secrets, publish Boskos inventory, deploy cleanup jobs or request quota.

## Retirement

To retire or rename a pool, stop new leases and drain running jobs before deliberately removing its backing resources and obsolete grants. Releasing a slot retains its containers, mock identities and role assignments. Renaming changes identity names and can orphan the old resources. Incremental RBAC deployment does not revoke obsolete grants; review removals separately.
