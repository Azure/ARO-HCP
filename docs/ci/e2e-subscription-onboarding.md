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
  - The **Owner-only, on-demand** `Microsoft.Azure.ARO.HCP.DevCI.Privileged` entrypoint reconciles the custom roles and shared-principal assignments for those subscriptions. Because those are subscription-scoped role definitions and role assignments, it is run by an OWNERS-group member (`make dev-ci-privileged-local-run`) — it is deliberately kept out of the unattended `dev-ci` postsubmit. The non-privileged CI bot identities + Key Vault secrets are reconciled automatically by the `Microsoft.Azure.ARO.HCP.DevCI.Unprivileged` entrypoint.

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
   - Quota approvals are asynchronous and routed through Microsoft support, so file them early — they gate identity-container provisioning (step 5) and determine the Role Assignment limit reported by monitoring (step 6).

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
   - In the same `config/config-dev-ci.yaml`, also add the subscription to the `opstool.tenantQuota` tenant's `subscriptions` list so the `tenant-quota-collector` tracks it. List the same `regions` the pool runs in; the collector retrieves the Role Assignment quota limit directly from Azure.
   - In a normal onboarding flow, `homeSubscription`, `sharedPrincipals`, and `msiMockPool.principals` should not need to change.
   - Apply the **privileged** customer-subscription grants (custom roles + shared-principal role assignments on the new subscription). This requires **Owner** on the target subscription, so it is **not** run by the `dev-ci` postsubmit — ask an OWNERS-group member to run it from the repo root:
     - `make dev-ci-privileged-local-run`

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
- `dev-infrastructure/dev-ci/e2e-subscription-rbac-grants/pipeline.yaml`
- `dev-infrastructure/configurations/mock-identity-rbac.tmpl.bicepparam`
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
| PROD (canary/EUAP regions only) | additionally `Microsoft.Resources/EUAPParticipation` — see [EUAP/Canary Region Access](#euapcanary-region-access) |

The routing flag controls which RP instance ARM sends requests to. `ExperimentalReleaseFeatures` gates experimental features used by E2E tests (non-stable channel groups, single-replica control planes, etc.). PROD additionally requires `InProgress` to route canary/EUAP traffic to the right RP.

> [!IMPORTANT]
> `InProgress` only controls ARM *routing*. It does not grant the subscription access to a EUAP region. Deploying into `eastus2euap` or `centraluseuap` additionally requires `Microsoft.Resources/EUAPParticipation`, which lives in a different namespace and uses a different approval path — see [EUAP/Canary Region Access](#euapcanary-region-access).

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

### EUAP/Canary Region Access

Subscriptions that run E2E in a canary/EUAP region (`eastus2euap`, `centraluseuap`) need `Microsoft.Resources/EUAPParticipation` registered on top of the `Microsoft.RedHatOpenShift` flags above. Without it, any resource group create in the region fails with `LocationNotAvailableForResourceGroup`.

This flag is **ManualApprove**: `az feature register` only moves it to `Pending` and it never self-completes. Something has to approve it — either LionRock fulfillment or a Geneva Action. **File the LionRock request first; fall back to Geneva Actions if it stalls.**

#### Step A: Initiate the registration

```bash
az feature register --namespace Microsoft.Resources --name EUAPParticipation \
  --subscription <subscription-id>
```

This leaves the flag in `Pending`. That is expected.

#### Step B: File a LionRock region-access request (preferred)

This is the sanctioned self-service path and the one to try first.

- File from a **b-account** (elevated / Service Tree owner).
- One request per subscription, for the canary region.
- Make it **region-access only**. Do not bundle SKU or quota line items — a single gated line item (GPU families in particular) holds up the whole request, including the region-access line.

#### Step C: Approve via Geneva Actions (fallback)

Switch to this path once the LionRock request lands in *Action Required* or is Backlogged. For subscriptions outside the Microsoft tenant, `Microsoft.RedHatOpenShift` is not treated as in-scope 1P, so LionRock handles them as **external subscriptions** and routes fulfillment to manual RDQuota, where it typically stalls. For Red Hat tenant subscriptions, treat the stall as the expected outcome rather than the exception — do not wait weeks on it.

Recognisable failure signature in the request activity log:

- `AutoApprove`: *"Requestor service is not in scope. Auto approve ARM requests for external subscriptions. Grant WAEAP approval."* — WAEAP approval looks like progress, but it is not the flag registration.
- Repeated `CheckAutoComplete`: *"AFEC flag for region access is not registered. Proceeding to fulfillment.."*
- `Fulfill` → manual RDQuota/ICM → *Action Required*, often with a generic *"The requested SKU/Region combination is not supported"* bot comment, and eventually Backlogged with an unrelated *"high demand for virtual machines in this region"* message.

To register the flag directly:

1. **Request JIT access** (in Teams) — note this is a *different* claim from the ARO one in Step 1:
   - Resource type: `acis`
   - Scope: `AFEC`
   - Access level: `PlatformServiceOperator`

   The claim auto-approves for members of the `TM-AFEC` security group. If you are not a member, ask someone who is to run the approval or to add you.

2. **Approve the registration** via Geneva Actions:
   - Azure Resource Manager → Feature Management → Approve Feature Registration
   - Resource Provider: `Microsoft.Resources`
   - Feature Name: `EUAPParticipation`
   - Subscription: the subscription ID to onboard

The Geneva Action registers the flag regardless of the LionRock request's state, so there is no need to cancel it first. Do close it out afterwards though — an open request blocks new ones from being filed for the same subscription and region, and you will need those for quota.

> [!NOTE]
> Most RPs have migrated to AFEC 2.0, where the owning RP holds the approver claim. `Microsoft.Resources` has not, which is why the `TM-AFEC` path still auto-approves — expect this procedure to need revisiting if that changes. See [AFEC 2.0 updates](https://eng.ms/docs/products/arm/rp_onboarding/afec/afec_20_updates).

#### Step D: Verify and register the provider

```bash
az feature show --namespace Microsoft.Resources --name EUAPParticipation \
  --subscription <subscription-id> --query properties.state -o tsv   # expect: Registered
az provider register --namespace Microsoft.Resources --subscription <subscription-id>
```

#### Quota

Region access is separate from quota. Once the flag is `Registered`, file the per-region quota increases for the canary region as described in step 2 of the DEV procedure above (Standard DSv3 Family vCPUs, Public IP Addresses, Role Assignments). File any GPU SKU (e.g. `NCasT4v3`) as its own request — GPU families are HPC-gated and need product-manager pre-approval, which will otherwise block the whole bundle.

### Step 2: CI Infrastructure Setup

1. Add the subscription to `config/config-dev-ci.yaml` under the appropriate `ci.<env>.e2eSubscriptions` section.

2. Run the `Microsoft.Azure.ARO.HCP.DevCI.Privileged` entrypoint to grant the environment's CI bot (e.g. `OpenShift Release Bot - STG`) the required RBAC on the new subscription (`make dev-ci-privileged-local-run`). This creates subscription-scoped role assignments and therefore requires **Owner** on the target subscription — run it on demand via an OWNERS-group member, not the `dev-ci` postsubmit.

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
