# External Subscription Onboarding

## DEV E2E Subscription Onboarding

This section covers the procedure for onboarding a DEV E2E customer subscription that is **not managed by the ARO-HCP pipeline identity** — i.e. a subscription owned by a different team within the same Entra tenant.

For subscriptions where the ARO-HCP team has Owner access, see the standard [E2E Subscription Onboarding](e2e-subscription-onboarding.md) procedure.

## Background: Identity Model

E2E tests involve two distinct classes of service principals acting on the customer subscription. Understanding this split is key to the onboarding steps below.

### CI Bot (test runner)

The **OpenShift Release Bot** is the identity under which the Prow CI system executes test code. It creates resource groups, deploys ARM templates, provisions AKS clusters, and assigns roles to per-cluster managed identities during each test run. It needs broad access (Contributor + RBAC Administrator + AKS RBAC Cluster Admin) because it orchestrates the entire test lifecycle — from infrastructure provisioning through teardown.

### DEV RP identities (request handlers)

In production, the ARO-HCP Resource Provider processes customer requests using Azure First Party Application identities. In DEV, these are emulated by shared service principals:

- **`aro-dev-first-party2`** — Simulates the first-party application that manages subnet service association links and resource groups in the customer subscription.
- **`aro-dev-arm-helper2`** — Simulates the ARM helper that performs Contributor-level and RBAC-level operations on behalf of the RP.
- **Pooled ARM helper principals** — Give each E2E Backend and Clusters Service instance independent ARM request budgets while performing the same Contributor-level and RBAC-level operations.
- **`aro-dev-msi-mock2`** and the **pooled MSI mock principals** — Simulate the managed-identity operations the RP performs (federated credential management, network configuration, Key Vault access for KMS encryption).

These RP identities are **not** used by the test code directly — they are used by the DEV RP instance to handle the API requests that the test code generates. Each needs custom role assignments on the customer subscription so the RP can fulfill those requests.

The pooled ARM helper and MSI mock principals exist so that parallel presubmit jobs can distribute ARM throttling across different identities.

### Identity containers

Each E2E test slot gets a set of pre-provisioned resource groups containing managed identities. These are deployed via ARM deployment stacks and sized according to the slot catalog. The number of identity containers limits the number of concurrent HCPs the E2E job can create within the slot.

### Cleanup jobs

Resource groups left behind by failed or timed-out tests are garbage-collected by periodic Prow jobs that delete expired groups.

## How It Differs From Internal Onboarding

For an **external** subscription:

- The subscription is **not listed** in `config/config-dev-ci.yaml` — the ARO-HCP pipeline does not interact with it at all
- The `apply-identity-pool` command **skips** it by default (controlled by `slot_assets.e2e_identities.provisioning: unmanaged` in the slot catalog)
- The subscription-owning team is responsible for running the RBAC setup and identity-pool provisioning themselves, using the ARO-HCP Bicep modules and tooling

Unmanaged provisioning does not bypass runtime admission. Slot acquisition uses
the selected cluster-profile credentials to clean and validate the leased E2E
identities; see the [credential contract](../../test/cmd/aro-hcp-tests/slot-manager/DESIGN.md#credentials).

## Responsibility Split

| Step | Responsible Team | Description |
| :--- | :--------------- | :---------- |
| Slot catalog entry | ARO-HCP (approves PR) | Add the pool to `test/e2e-config/e2e-slots.yaml` with `slot_assets.e2e_identities.provisioning: unmanaged` |
| Boskos sync | ARO-HCP (approves PR) | Run `slot-manager sync-boskos-config` and merge the `openshift/release` PR |
| Vault secret | ARO-HCP (manual) | Add `customer-<shard>-subscription-id` and `customer-<shard>-subscription-name` to the cluster profile secret |
| CI Bot grants | Subscription owner | Grant the CI bot (test runner) the required roles on the subscription (Step 1 below) |
| RP identity RBAC | Subscription owner | Deploy the Bicep module that grants the DEV RP identities access (Step 2 below) |
| Identity containers | Subscription owner | Run `make -C test apply-identity-pool ENVIRONMENT=dev SUBSCRIPTION=<name>` (Step 3 below) |
| Cleanup job | Subscription owner | Add a periodic cleanup job in `openshift/release` (Step 4 below) |

## Subscription Owner Steps

### Prerequisites

Step 2 requires [Mike Farah `yq`](https://github.com/mikefarah/yq) v4 to generate the Bicep array parameters from the checked-in identity catalogs. Confirm `yq --version` reports v4 before running the commands; the Python `yq` package is not compatible with the documented syntax.

Register the Azure resource providers required for E2E test operations:

```sh
for ns in \
  Microsoft.Compute \
  Microsoft.Insights \
  Microsoft.KeyVault \
  Microsoft.ManagedIdentity \
  Microsoft.Network \
  Microsoft.Quota \
  Microsoft.RedHatOpenShift \
  Microsoft.Storage; do
  az provider register --namespace "$ns" --subscription <subscription-id>
done
```

These are needed so the CI bot and RP identities can create/manage compute resources, networking, and managed identities within the subscription.

`Microsoft.Storage` (and `Microsoft.Authorization`, which is registered by default on every subscription) must additionally be `Registered` because the backend `AzureResourceProvidersRegistrationValidation` controller checks the registration state of `Microsoft.Authorization`, `Microsoft.Compute`, `Microsoft.Network`, and `Microsoft.Storage` on the customer subscription before a cluster can provision. If any of these is unregistered, the validation fails and the cluster never leaves the retry loop. Confirm the required providers report `Registered` before proceeding:

```sh
for ns in \
  Microsoft.Authorization \
  Microsoft.Compute \
  Microsoft.Insights \
  Microsoft.KeyVault \
  Microsoft.ManagedIdentity \
  Microsoft.Network \
  Microsoft.Quota \
  Microsoft.RedHatOpenShift \
  Microsoft.Storage; do
  echo "$ns: $(az provider show --namespace "$ns" \
    --subscription <subscription-id> --query registrationState -o tsv)"
done
```

Registration is asynchronous, so this is a point-in-time check. If any provider
still reports `Registering`, wait and re-run it until every provider reports
`Registered` before continuing.

### Step 1: Grant the CI Bot (Test Runner)

The CI bot (`OpenShift Release Bot`, appId `38335e22-716a-4a21-bf20-15ab141823f0`, objectId `c209f8df-52ae-48fb-98ea-380f58b04652`) is the identity that **executes the test code** — it provisions infrastructure, deploys the RP, runs assertions, and tears everything down. It needs the following roles at **subscription scope**:

| Role | Condition | Purpose |
| :--- | :-------- | :------ |
| Contributor | None | Create/manage resource groups, ARM deployments, and Azure resources during tests |
| Role Based Access Control Administrator | ABAC condition preventing assignment of Owner, UAA, and RBAC Administrator roles | Assign roles to per-cluster managed identities created during test runs |
| Azure Kubernetes Service RBAC Cluster Admin | None | Required for AKS infrastructure provisioning (service cluster creation), not for the tests themselves |

Deploy using the same Bicep module that manages CI bot RBAC for all environments:

```sh
SUBSCRIPTION_ID="<target-subscription-id>"
BOT_PRINCIPAL_ID="c209f8df-52ae-48fb-98ea-380f58b04652"

az deployment sub create \
  --subscription "${SUBSCRIPTION_ID}" \
  --location westus3 \
  --template-file dev-infrastructure/templates/ci-bot-rbac-subscription.bicep \
  --parameters \
    botPrincipalId="${BOT_PRINCIPAL_ID}" \
    isGlobalSubscription=false \
    grantAksRbac=true
```

> **Note:** Set `grantAksRbac=true` only if the subscription will also host AKS service and management clusters (the DEV RP). If the subscription is used solely for customer resources (HCPs), set it to `false`.

This deploys Contributor, RBAC Administrator (with ABAC condition), and optionally AKS RBAC Cluster Admin — identical to what the pipeline deploys for internally-managed subscriptions.

### Step 2: Grant the DEV RP Identities (Request Handlers)

The shared DEV RP identities are service principals that the **DEV Resource Provider uses to handle API requests** generated by the test code. They are not invoked by the test runner itself — they act on behalf of the RP when it processes cluster creation, deletion, and management operations in the customer subscription.

The Bicep module defines the custom roles (`dev-first-party-mock`, `dev-msi-mock`) locally in the target subscription and assigns them to these RP identities, together with the built-in `Key Vault Crypto User` role (for KMS/etcd encryption) for the MSI mocks and the built-in `Contributor` + `Role Based Access Control Administrator` roles for the static and pooled ARM helpers.

```sh
SUBSCRIPTION_ID="<your-subscription-id>"
ARM_HELPER_POOL_PRINCIPALS="$(
  yq -o=json -I=0 \
    '[.armHelperPool | to_entries[] | {"name": .key, "principalId": .value.principalId}]' \
    dev-infrastructure/openshift-ci/arm-helper-pool.yaml
)"
MSI_MOCK_POOL_PRINCIPALS="$(
  yq -o=json -I=0 \
    '[.miMockPool | to_entries[] | {"name": .key, "principalId": .value.principalId}]' \
    dev-infrastructure/openshift-ci/msi-mock-pool.yaml
)"

az deployment sub create \
  --subscription "${SUBSCRIPTION_ID}" \
  --location westus3 \
  --template-file dev-infrastructure/templates/e2e-subscription-rbac-assignment-subscription.bicep \
  --parameters \
    firstPartyPrincipalId="47f69502-0065-4d9a-b19b-d403e183d2f4" \
    armHelperPrincipalId="ddeffa11-e3d9-487d-8fc9-9a9e26f64975" \
    armHelperPoolPrincipals="${ARM_HELPER_POOL_PRINCIPALS}" \
    miMockPrincipalId="d6b62dfa-87f5-49b3-bbcb-4a687c4faa96" \
    msiMockPoolPrincipals="${MSI_MOCK_POOL_PRINCIPALS}"
```

### Step 3: Provision Identity Containers

Use the `apply-identity-pool` Make target with the `SUBSCRIPTION` variable to target only the relevant pool:

```sh
make -C test apply-identity-pool ENVIRONMENT=dev SUBSCRIPTION="Hypershift Managed Azure"
```

Always run through this Make target rather than `go run` or a hand-built binary. The target rebuilds `aro-hcp-tests` and, as part of that, regenerates the Bicep-derived ARM artifacts (e.g. `msi-pools.json`) from the source-of-truth Bicep in `test/e2e-setup/bicep/`. The generated artifacts under `test/e2e/test-artifacts/generated-test-artifacts/` are git-ignored build outputs, so bypassing the Make build can embed and apply a stale template — which manifests as resource groups being deleted and recreated instead of updated in place.

This deploys the MSI container deployment stacks into the target subscription based on the slot catalog configuration.

### Step 4: Add a Cleanup Job

Open a PR to `openshift/release` adding a periodic cleanup job for the subscription:

```yaml
- as: delete-expired-dev-ci-hypershift-resource-groups
  cron: 35 * * * *
  steps:
    env:
      CLEANUP_MODE: no-rp
      CUSTOMER_SUBSCRIPTION: Hypershift Managed Azure
    test:
    - ref: aro-hcp-deprovision-expired-resource-groups
```

## Maintenance

If the Bicep module `e2e-subscription-rbac-assignment-subscription.bicep` is updated (new RP principals, new roles, permission changes), the subscription-owning team must re-run the deployment in Step 2.

Changes to `dev-infrastructure/openshift-ci/msi-mock-pool.yaml` require re-running Step 2 so every current MSI mock pool principal receives the required subscription-scoped assignments.

Changes to `dev-infrastructure/openshift-ci/arm-helper-pool.yaml` require re-running Step 2 so every current ARM helper pool principal receives the subscription-scoped Contributor and Role Based Access Control Administrator assignments.

## Reference: Shared Principal IDs

### CI Bot (test runner)

| Identity | Principal ID | Purpose |
| :------- | :----------- | :------ |
| OpenShift Release Bot | `c209f8df-52ae-48fb-98ea-380f58b04652` | Executes test code, provisions infrastructure (app: `38335e22-716a-4a21-bf20-15ab141823f0`) |

### DEV RP identities (request handlers)

| Identity | Principal ID | Purpose |
| :------- | :----------- | :------ |
| `aro-dev-first-party2` | `47f69502-0065-4d9a-b19b-d403e183d2f4` | First-party application mock — manages subnet links and resource groups |
| `aro-dev-arm-helper2` | `ddeffa11-e3d9-487d-8fc9-9a9e26f64975` | ARM helper — Contributor + RBAC Admin operations on behalf of the RP |
| `aro-hcp-arm-helper-sp-dev-*` | *(generated from `dev-infrastructure/openshift-ci/arm-helper-pool.yaml` in Step 2)* | Pooled ARM helpers leased independently to Backend and Clusters Service |
| `aro-dev-msi-mock2` | `d6b62dfa-87f5-49b3-bbcb-4a687c4faa96` | MSI mock — federated credentials, networking, Key Vault access |
| `aro-hcp-msi-mock-cs-sp-dev-*` | *(generated from `dev-infrastructure/openshift-ci/msi-mock-pool.yaml` in Step 2)* | Pooled MSI mocks for parallel presubmit job isolation |

## See Also

- [E2E Subscription Onboarding](e2e-subscription-onboarding.md) — internal subscription procedure
- [Dev-CI Topology](dev-ci-topology.md)
- [CI Identity Leasing](identity-leasing.md)
