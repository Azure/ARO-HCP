# DEV E2E Subscription Onboarding

This document covers the current procedure for adding another customer subscription to the DEV E2E slot fleet.

Today the canonical DEV slot inventory lives in `test/e2e-config/e2e-slots.yaml`, where the `dev` slot environment is consumed by the `prow` and `ci01` deploy environments. This onboarding flow is DEV-specific; INT, STG, and PROD use different access models.

## What This Onboarding Touches

Adding a new DEV customer subscription spans seven different inventories:

- the canonical slot catalog in this repository
- the ARO-HCP-managed Boskos inventory in `openshift/release`
- the cluster profile secret inventory outside this repository
- the OpenShift Release Bot subscription grants
- the standalone `dev-ci` bootstrap RBAC rollout
- the tenant-quota collector subscription inventory
- the periodic cleanup jobs in `openshift/release`

It is not just a slot-catalog change.

## Current Model

The current implementation is split across two layers:

- **Runtime slot leasing**
  - `test/e2e-config/e2e-slots.yaml` defines the canonical slot inventory.
  - `aro-hcp-tests slot-manager` manages Boskos sync/validation, acquire/release, and slot-managed identity-container provisioning.
- **DEV bootstrap access**
  - `config/config-dev-ci.yaml` records the explicit DEV E2E customer subscriptions that receive shared bootstrap grants.
  - `Microsoft.Azure.ARO.HCP.DevCI.E2ESubscriptionRBAC` reconciles the custom roles and shared-principal assignments for those subscriptions.

The bootstrap layer is about the shared dev identities used by the DEV services and by local E2E provisioning, not the per-cluster managed identities created for a specific HCP during a test run.

## Existing-Assignment Caveat

The `ci.dev.e2eSubscriptions` list in `config/config-dev-ci.yaml` now fans out into the three `dev-ci` RBAC parameter templates, so adding a brand-new third DEV customer subscription no longer requires per-index template edits first.

A separate caveat still applies when you are adopting pre-existing role assignments instead of creating fresh ones: `legacyAssignmentIdsBySubscription` in `dev-infrastructure/configurations/e2e-subscription-rbac-assignments.tmpl.bicepparam` must contain the Azure-generated assignment IDs for any subscription whose existing grants need to be adopted in place. A brand-new subscription normally does not need that map.

## Shared Bootstrap Identities

The DEV bootstrap layer currently grants access for these shared identities:

- `aro-dev-first-party2`
- `aro-dev-arm-helper2`
- `aro-dev-msi-mock2`
- the pooled `aro-dev-msi-mock-pool-<i>` identities used by presubmit jobs

For the current mixed-management model of the pooled MSI mock identities, see [CI Identity Leasing](identity-leasing.md).

## Prerequisites

A brand-new subscription typically has no Azure resource providers registered beyond `Microsoft.Authorization`. The Azure portal quota blade reports *"The selected provider is not registered for some of the selected subscriptions"*, and later provisioning and RBAC steps fail until the providers used by ARO-HCP are registered.

Register the required providers on each new subscription before requesting quota or running any provisioning step:

```sh
for ns in Microsoft.Compute Microsoft.Network Microsoft.ManagedIdentity \
          Microsoft.Storage Microsoft.KeyVault Microsoft.RedHatOpenShift; do
  az provider register --namespace "$ns" --subscription <subscription-id>
done
```

Registration is asynchronous; wait until every namespace reports `Registered`:

```sh
az provider show --namespace Microsoft.Compute \
  --subscription <subscription-id> --query registrationState -o tsv
```

`Microsoft.Compute` and `Microsoft.Network` in particular must be registered before the Standard DSv3 vCPU and public-IP quota requests can be filed.

## Procedure

1. Add the new pool to `test/e2e-config/e2e-slots.yaml`.
   - Pick the next shard number and a unique `resource_type`.
   - Set `slot_count` to the intended concurrency for the new subscription.
   - Keep the existing DEV identity-container pattern aligned unless there is a deliberate reason to diverge.

2. Sync the ARO-HCP-managed Boskos inventory in `openshift/release`.
   - Run:
     - `./test/aro-hcp-tests slot-manager sync-boskos-config --release-repo <release-checkout>`
   - In the release checkout, regenerate config:
     - `make update`
   - Validate that the generated Boskos inventory matches the slot catalog:
     - `./test/aro-hcp-tests slot-manager validate-boskos-config --release-repo <release-checkout>`
   - Open and merge the `openshift/release` PR, then wait for the Boskos config rollout.

3. Update the cluster profile secret inventory outside this repository.
   - Add:
     - `customer-shardN-subscription-id`
     - `customer-shardN-subscription-name`
   - `N` must match the intended shard number and should remain stable once jobs depend on that mapping.

4. Grant the OpenShift Release Bot access to the new subscription.
   - Add the subscription name to the `SUBSCRIPTIONS` array in `dev-infrastructure/openshift-ci/grant-openshift-release-bot-dev.sh`.
   - Run the script:
     - `./dev-infrastructure/openshift-ci/grant-openshift-release-bot-dev.sh`
   - This grants the CI service principal (`OpenShift Release Bot`) the necessary role assignments on the new subscription so that CI jobs can authenticate and create resources there.

5. Provision the slot-backed identity containers in the new subscription.
   - Run:
     - `go run ./test/cmd/aro-hcp-tests slot-manager apply-identity-pool --environment dev`
   - The built `aro-hcp-tests` binary can be used instead of `go run` if preferred.
   - Verify that the deployment stacks and identity-container resource groups are created in the new subscription.

6. Extend the DEV bootstrap RBAC inventory.
   - Add the subscription name and ID to `config/config-dev-ci.yaml` under `ci.dev.e2eSubscriptions`.
   - That list now feeds the `dev-ci` RBAC parameter templates directly, so a brand-new subscription does not require extra per-index template edits.
   - In a normal onboarding flow, `devMockIdentities.homeSubscriptionId`, `devMockIdentities.sharedPrincipals`, and `devMockIdentities.msiMockPool.principals` should not need to change.
   - If you are adopting pre-existing role assignments instead of creating new ones, also extend `legacyAssignmentIdsBySubscription` in `e2e-subscription-rbac-assignments.tmpl.bicepparam`. A brand-new subscription normally does not need that shim.
   - Run the rollout from the repo root:
     - `make dev-ci-e2e-subscription-rbac-local-run`

7. Register the new subscription in the tenant-quota collector.
   - Add the subscription name to the `SUBSCRIPTIONS` array in `tooling/tenant-quota/scripts/manage-service-principals.sh` under the `setup_redhat()` function.
   - Add the subscription to `config/config-dev-ci.yaml` under `opstool.tenantQuota.tenants[].subscriptions` with the appropriate `roleAssignmentLimit` and `regions`.
   - Run the script to grant the collector SP Reader access:
     - `./tooling/tenant-quota/scripts/manage-service-principals.sh setup redhat`
   - This ensures Azure quota (role assignments, compute, network) is monitored for the new subscription. See [CI Quota Monitoring](quota-monitoring.md).

8. Add periodic cleanup jobs for the new subscription in `openshift/release`.
   - In `ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__periodic-cleanup.yaml`, add a `delete-expired-dev-ci-shardN-resource-groups` periodic job targeting the new subscription.
   - Set `CUSTOMER_SUBSCRIPTION` to the subscription display name and use the same `CLEANUP_MODE: no-rp`, cron schedule, and step reference (`aro-hcp-deprovision-expired-resource-groups`) as existing shard jobs. Since each job targets a different subscription, they can safely run at the same time.
   - Run `make update` in the release checkout to regenerate Prow job manifests.
   - See [openshift/release#80292](https://github.com/openshift/release/pull/80292) for a reference implementation.

9. Validate the end-to-end path.
   - Confirm `slot-manager acquire` can resolve the new pool using the updated cluster profile inventory.
   - Run a DEV rehearsal expected to target the new shard.
   - Verify customer-resource creation in the new subscription succeeds without Azure `AuthorizationFailed` errors.
   - Verify release and cleanup still return the leased slot correctly.

## What Usually Does Not Change

Adding a new DEV customer subscription normally does not require:

- rotating the shared dev bootstrap principals
- changing the pooled MSI mock principal IDs
- regenerating `dev-infrastructure/openshift-ci/msi-mock-pool.yaml`

Those steps only become necessary if the shared identities or the Boskos-backed MSI mock pool itself changes.

## Where To Look

- `test/e2e-config/e2e-slots.yaml`
- `test/cmd/aro-hcp-tests/slot-manager/DESIGN.md`
- `test/cmd/aro-hcp-tests/slot-manager/release_repo.go`
- `test/cmd/aro-hcp-tests/slot-manager/identity-pool/`
- `config/config-dev-ci.yaml`
- `dev-infrastructure/dev-ci/e2e-subscription-rbac/pipeline.yaml`
- `dev-infrastructure/configurations/dev-operator-roles.tmpl.bicepparam`
- `dev-infrastructure/configurations/mock-identity-roles.tmpl.bicepparam`
- `dev-infrastructure/configurations/e2e-subscription-rbac-assignments.tmpl.bicepparam`
- `dev-infrastructure/openshift-ci/grant-openshift-release-bot-dev.sh`
- `tooling/tenant-quota/scripts/manage-service-principals.sh`
- `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__periodic-cleanup.yaml`
- [Dev-CI Topology](dev-ci-topology.md)
- [CI Identity Leasing](identity-leasing.md)
- [CI Quota Monitoring](quota-monitoring.md)
- [CI Cleanup](cleanup.md)

## See Also

- [CI Overview](README.md)
- [Dev-CI Topology](dev-ci-topology.md)
- [CI Identity Leasing](identity-leasing.md)
- [CI Operations](operations.md)
