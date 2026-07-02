# E2E Subscription Onboarding

This document covers the procedure for onboarding new customer subscriptions for E2E testing across all environments.

- [DEV](#dev-e2e-subscription-onboarding) — no ARM integration; onboarding is CI-infrastructure-only
- [INT / STG / PROD](#intstgprod-e2e-subscription-onboarding) — ARM-integrated environments; requires AFEC flag registration plus CI infrastructure changes

---

## DEV E2E Subscription Onboarding

This section covers the procedure for adding another customer subscription to the DEV E2E slot fleet.

Today the canonical DEV slot inventory lives in `test/e2e-config/e2e-slots.yaml`, where the `dev` slot environment is consumed by the `prow` and `ci01` deploy environments.

### What This Onboarding Touches

Adding a new DEV customer subscription spans four different inventories:

- the canonical slot catalog in this repository
- the ARO-HCP-managed Boskos inventory in `openshift/release`
- the cluster profile secret inventory outside this repository
- the standalone `dev-ci` bootstrap RBAC rollout

It is not just a slot-catalog change.

### Current Model

The current implementation is split across two layers:

- **Runtime slot leasing**
  - `test/e2e-config/e2e-slots.yaml` defines the canonical slot inventory.
  - `aro-hcp-tests slot-manager` manages Boskos sync/validation, acquire/release, and slot-managed identity-container provisioning.
- **DEV bootstrap access**
  - `config/config-dev-ci.yaml` records the explicit DEV E2E customer subscriptions that receive shared bootstrap grants.
  - `Microsoft.Azure.ARO.HCP.DevCI.E2ESubscriptionRBAC` reconciles the custom roles and shared-principal assignments for those subscriptions.

The bootstrap layer is about the shared dev identities used by the DEV services and by local E2E provisioning, not the per-cluster managed identities created for a specific HCP during a test run.

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
          Microsoft.Storage Microsoft.KeyVault Microsoft.RedHatOpenShift \
          Microsoft.Quota; do
  az provider register --namespace "$ns" --subscription <subscription-id>
done
```

Registration is asynchronous; wait until every namespace reports `Registered`:

```sh
az provider show --namespace Microsoft.Compute \
  --subscription <subscription-id> --query registrationState -o tsv
```

`Microsoft.Compute` and `Microsoft.Network` in particular must be registered before the Standard DSv3 vCPU and public-IP quota requests can be filed. `Microsoft.Quota` backs the quota tooling and the `tenant-quota-collector` monitoring updated in step 6.

## Procedure

1. Add the new pool to `test/e2e-config/e2e-slots.yaml`.
   - Pick the next shard number and a unique `resource_type`.
   - Set `slot_count` to the intended concurrency for the new subscription.
   - Keep the existing DEV identity-container pattern aligned unless there is a deliberate reason to diverge.

2. Request the Azure quota increases for the new subscription.
   - File a quota request for every region the new pool runs in. The current per-region targets are:
     - Standard DSv3 Family vCPUs: `2000`
     - Public IP Addresses: `3000`
     - Role Assignments: `8000`
   - `Microsoft.Compute` and `Microsoft.Network` must already report `Registered` (see Prerequisites) before the DSv3 and public-IP requests can be filed.
   - Quota approvals are asynchronous and routed through Microsoft support, so file them early — they gate identity-container provisioning (step 5) and the Role Assignment limit asserted by the monitoring entry (step 6).

3. Sync the ARO-HCP-managed Boskos inventory in `openshift/release`.
   - Run:
     - `./test/aro-hcp-tests slot-manager sync-boskos-config --release-repo <release-checkout>`
   - In the release checkout, regenerate config:
     - `make update`
   - Validate that the generated Boskos inventory matches the slot catalog:
     - `./test/aro-hcp-tests slot-manager validate-boskos-config --release-repo <release-checkout>`
   - Open and merge the `openshift/release` PR, then wait for the Boskos config rollout.

4. Update the cluster profile secret inventory outside this repository.
   - Add:
     - `customer-shardN-subscription-id`
     - `customer-shardN-subscription-name`
   - `N` must match the intended shard number and should remain stable once jobs depend on that mapping.

5. Provision the slot-backed identity containers in the new subscription.
   - Run:
     - `go run ./test/cmd/aro-hcp-tests slot-manager apply-identity-pool --environment dev`
   - The built `aro-hcp-tests` binary can be used instead of `go run` if preferred.
   - Verify that the deployment stacks and identity-container resource groups are created in the new subscription.

6. Extend the DEV bootstrap RBAC and quota-monitoring inventory.
   - Add the subscription name and ID to `config/config-dev-ci.yaml` under `ci.dev.e2eSubscriptions`.
   - That list now feeds the `dev-ci` RBAC parameter templates directly, so a brand-new subscription does not require extra per-index template edits.
   - In the same `config/config-dev-ci.yaml`, also add the subscription to the `opstool.tenantQuota` tenant's `subscriptions` list so the `tenant-quota-collector` tracks it. Set `roleAssignmentLimit: 8000` and list the same `regions` the pool runs in, matching the Role Assignment quota requested in step 2.
   - In a normal onboarding flow, `homeSubscription`, `sharedPrincipals`, and `msiMockPool.principals` should not need to change.
   - Run the rollout from the repo root:
     - `make dev-ci-e2e-subscription-rbac-local-run`

7. Validate the end-to-end path.
   - Confirm `slot-manager acquire` can resolve the new pool using the updated cluster profile inventory.
   - Run a DEV rehearsal expected to target the new shard.
   - Verify customer-resource creation in the new subscription succeeds without Azure `AuthorizationFailed` errors.
   - Verify release and cleanup still return the leased slot correctly.

### What Usually Does Not Change

Adding a new DEV customer subscription normally does not require:

- rotating the shared dev bootstrap principals
- changing the pooled MSI mock principal IDs
- regenerating `dev-infrastructure/openshift-ci/msi-mock-pool.yaml`

Those steps only become necessary if the shared identities or the Boskos-backed MSI mock pool itself changes.

### Where To Look

- `test/e2e-config/e2e-slots.yaml`
- `test/cmd/aro-hcp-tests/slot-manager/DESIGN.md`
- `test/cmd/aro-hcp-tests/slot-manager/release_repo.go`
- `test/cmd/aro-hcp-tests/slot-manager/identity-pool/`
- `config/config-dev-ci.yaml`
- `dev-infrastructure/dev-ci/e2e-subscription-rbac/pipeline.yaml`
- `dev-infrastructure/configurations/dev-operator-roles.tmpl.bicepparam`
- `dev-infrastructure/configurations/mock-identity-roles.tmpl.bicepparam`
- `dev-infrastructure/configurations/e2e-subscription-rbac-assignments.tmpl.bicepparam`
- [Dev-CI Topology](dev-ci-topology.md)
- [CI Identity Leasing](identity-leasing.md)

### External (Unmanaged) Subscriptions

For subscriptions owned by a different team where our pipeline identity does **not** have access, use the external onboarding model instead. External subscriptions are **not listed** in `config/config-dev-ci.yaml` and are marked with `identity_provisioning: unmanaged` in the slot catalog.

The external team runs the RBAC setup and identity-pool provisioning themselves using our Bicep modules. See [External Subscription Onboarding](external-subscription-onboarding.md) for the full procedure and grant contract.

---

## INT/STG/PROD E2E Subscription Onboarding

INT, STG, and PROD are ARM-integrated environments. Each runs its own RP instance, and ARM routes `Microsoft.RedHatOpenShift` API calls to the correct RP based on AFEC (Azure Feature Exposure Control) flags registered on the customer subscription. Without the correct flags, API calls from a subscription will not reach the intended RP.

Onboarding a new E2E testing subscription requires two steps: registering the AFEC flags so ARM routes traffic to the correct RP, and setting up the CI infrastructure (service principal, Boskos slots, cleanup jobs).

### ARM Routing Flags

| AFEC Flag | Routes to |
| :-------- | :-------- |
| `HcpPrivatePreview` | Prod RP in GA regions (uksouth, switzerlandnorth, canadacentral, etc.) |
| `STAGING-APPROVED` | STG RP (uksouthstaging) |
| `INT-APPROVED` | INT RP (uksouth azure-test.net) |
| `InProgress` | EUAP/canary regions (centraluseuap, eastus2euap) + disabled future regions (westus, westus2) |

### Required AFEC Flags per Environment

| Environment | Required AFEC Flags |
| :---------- | :------------------ |
| INT         | `Microsoft.RedHatOpenShift/INT-APPROVED`, `Microsoft.RedHatOpenShift/ExperimentalReleaseFeatures` |
| STG         | `Microsoft.RedHatOpenShift/STAGING-APPROVED`, `Microsoft.RedHatOpenShift/ExperimentalReleaseFeatures` |
| PROD        | `Microsoft.RedHatOpenShift/HcpPrivatePreview`, `Microsoft.RedHatOpenShift/InProgress`, `Microsoft.RedHatOpenShift/ExperimentalReleaseFeatures` |

The routing flag controls which RP instance ARM sends requests to. `ExperimentalReleaseFeatures` gates experimental features used by E2E tests (non-stable channel groups, single-replica control planes, etc.). PROD additionally requires `InProgress` to enable EUAP/canary region access.

### Step 1: Register AFEC Flags

AFEC registration is a two-step process: first initiate the registration from the customer subscription, then approve it via a Geneva Action.

1. **Initiate registration** from the subscription's tenant. Run `az feature register` for each required flag:
   ```bash
   az feature register --namespace Microsoft.RedHatOpenShift --name <flag-name> \
     --subscription <subscription-id>
   ```
   For example, for STG:
   ```bash
   az feature register --namespace Microsoft.RedHatOpenShift --name STAGING-APPROVED \
     --subscription <subscription-id>
   az feature register --namespace Microsoft.RedHatOpenShift --name ExperimentalReleaseFeatures \
     --subscription <subscription-id>
   ```
   This puts the features into `Registering` state.

2. **Request JIT access** (in Teams):
   - Resource type: `acis`
   - ARO → `PlatformServiceAdministrator`

3. **Approve the registration** via Geneva Actions:
   - Azure Resource Manager → Feature Management → Approve Feature Registration
   - Namespace: `Microsoft.RedHatOpenShift`
   - Feature Names: all flags initiated in step 1 that are in "Pending" status
   - Subscription: the subscription ID to onboard

4. **Verify** (from the subscription's tenant):
   ```bash
   az feature list --namespace Microsoft.RedHatOpenShift -o table \
     --subscription <subscription-id>
   ```
   All flags should show `Registered`.

> [!NOTE]
> Step 1 can be performed by anyone with write access to the subscription. Steps 2-3 require Microsoft PlatformServiceAdministrator access.

### Step 2: CI Infrastructure Setup

1. Add the subscription to `config/config-dev-ci.yaml` under the appropriate `ci.<env>.e2eSubscriptions` section.

2. Run the `Microsoft.Azure.ARO.HCP.DevCI.E2ESubscriptionRBAC` pipeline to grant the environment's CI bot (e.g. `OpenShift Release Bot - STG`) the required RBAC on the new subscription.

3. Add the pool to `test/e2e-config/e2e-slots.yaml` under the environment's `pools` list.

4. Sync the Boskos inventory and update CI job configs in `openshift/release` (slot catalog, cleanup jobs, `make update`).

5. Update the Vault cluster profile secret (e.g. `kv/selfservice/hcm-aro/aro-hcp-<env>-rh`) with the new subscription's `customer-shard0-subscription-name` and `customer-shard0-subscription-id`.

6. Validate by running a rehearsal E2E job against the new subscription.

## See Also

- [CI Overview](README.md)
- [Dev-CI Topology](dev-ci-topology.md)
- [CI Identity Leasing](identity-leasing.md)
- [CI Operations](operations.md)
- [Environments](../environments.md)
