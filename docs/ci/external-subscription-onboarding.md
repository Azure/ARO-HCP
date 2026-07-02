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
- **`aro-dev-msi-mock2`** and the **pooled MSI mock principals** — Simulate the managed-identity operations the RP performs (federated credential management, network configuration, Key Vault access for KMS encryption).

These RP identities are **not** used by the test code directly — they are used by the DEV RP instance to handle the API requests that the test code generates. Each needs custom role assignments on the customer subscription so the RP can fulfill those requests.

The pooled MSI mock principals exist so that parallel presubmit jobs each get their own identity, distributing the ARM throttling budget across different principals.

### Identity containers

Each E2E test slot gets a set of pre-provisioned resource groups containing managed identities. These are deployed via ARM deployment stacks and sized according to the slot catalog. The number of identity containers limits the number of concurrent HCPs the E2E job can create within the slot.

### Cleanup jobs

Resource groups left behind by failed or timed-out tests are garbage-collected by periodic Prow jobs that delete expired groups.

## How It Differs From Internal Onboarding

In a standard DEV subscription onboarding, the ARO-HCP pipeline identity (`aro-hcp-owners` group) has Owner access and can deploy RBAC, custom role assignments, and identity containers directly.

For an **external** subscription:

- The subscription is **not listed** in `config/config-dev-ci.yaml` — the ARO-HCP pipeline does not interact with it at all
- The `apply-identity-pool` command **skips** it by default (controlled by `identity_provisioning: unmanaged` in the slot catalog)
- The subscription-owning team is responsible for running the RBAC setup and identity-pool provisioning themselves, using the ARO-HCP Bicep modules and tooling

## Responsibility Split

| Step | Responsible Team | Description |
| :--- | :--------------- | :---------- |
| Slot catalog entry | ARO-HCP (approves PR) | Add the pool to `test/e2e-config/e2e-slots.yaml` with `identity_provisioning: unmanaged` |
| Boskos sync | ARO-HCP (approves PR) | Run `slot-manager sync-boskos-config` and merge the `openshift/release` PR |
| Vault secret | ARO-HCP (manual) | Add `customer-<shard>-subscription-id` and `customer-<shard>-subscription-name` to the cluster profile secret |
| CI Bot grants | Subscription owner | Grant the CI bot (test runner) the required roles on the subscription (Step 1 below) |
| RP identity RBAC | Subscription owner | Deploy the Bicep module that grants the DEV RP identities access (Step 2 below) |
| Identity containers | Subscription owner | Run `apply-identity-pool --subscription` (Step 3 below) |
| Cleanup job | Subscription owner | Add a periodic cleanup job in `openshift/release` (Step 4 below) |

## Subscription Owner Steps

### Prerequisites

Register the Azure resource providers required for E2E test operations:

```sh
for ns in Microsoft.Compute Microsoft.Network Microsoft.ManagedIdentity; do
  az provider register --namespace "$ns" --subscription <subscription-id>
done
```

These are needed so the CI bot and RP identities can create/manage compute resources, networking, and managed identities within the subscription.

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

The Bicep module defines the custom roles (`dev-first-party-mock`, `dev-msi-mock`) locally in the target subscription and assigns them to these RP identities, together with the built-in `Key Vault Crypto User` role (for KMS/etcd encryption) for the MSI mocks and the built-in `Contributor` + `Role Based Access Control Administrator` roles for the ARM helper.

```sh
SUBSCRIPTION_ID="<your-subscription-id>"

az deployment sub create \
  --subscription "${SUBSCRIPTION_ID}" \
  --location westus3 \
  --template-file dev-infrastructure/templates/e2e-subscription-rbac-assignment-subscription.bicep \
  --parameters \
    firstPartyPrincipalId="47f69502-0065-4d9a-b19b-d403e183d2f4" \
    armHelperPrincipalId="ddeffa11-e3d9-487d-8fc9-9a9e26f64975" \
    miMockPrincipalId="d6b62dfa-87f5-49b3-bbcb-4a687c4faa96" \
    msiMockPoolPrincipals='[{"name":"aro-hcp-msi-mock-cs-sp-dev-0","principalId":"db27175c-5bd0-48b4-929a-41de9a53ffbf"},{"name":"aro-hcp-msi-mock-cs-sp-dev-1","principalId":"cd39c606-1f6a-4062-a5b9-497cd04c39fc"},{"name":"aro-hcp-msi-mock-cs-sp-dev-2","principalId":"3871b527-fb1e-4123-b38b-3cb2445a9fc8"},{"name":"aro-hcp-msi-mock-cs-sp-dev-3","principalId":"e92b9f76-b040-4cf6-a4dd-5c8bdc759e69"},{"name":"aro-hcp-msi-mock-cs-sp-dev-4","principalId":"3d8c36e1-ae7a-42ef-b7d1-ea4667708d30"},{"name":"aro-hcp-msi-mock-cs-sp-dev-5","principalId":"3015be55-a361-4a86-8439-0abc7860a4ef"},{"name":"aro-hcp-msi-mock-cs-sp-dev-6","principalId":"f8be0ede-39df-41cf-aed7-7f3626f23a5a"},{"name":"aro-hcp-msi-mock-cs-sp-dev-7","principalId":"07aa0e83-353e-444c-9f1d-d023ac3c5396"},{"name":"aro-hcp-msi-mock-cs-sp-dev-8","principalId":"0d25d885-2cd8-4972-b610-4d85881c1ec4"},{"name":"aro-hcp-msi-mock-cs-sp-dev-9","principalId":"5157a63c-87dc-4680-8f46-954b46399bdc"},{"name":"aro-hcp-msi-mock-cs-sp-dev-10","principalId":"5a34d2fd-f7db-460a-a272-334006a8a3b8"},{"name":"aro-hcp-msi-mock-cs-sp-dev-11","principalId":"fd35ac5f-6493-40cb-9131-d669a70114c3"},{"name":"aro-hcp-msi-mock-cs-sp-dev-12","principalId":"a76148d4-cb11-4b93-9363-54ba8239b0b1"},{"name":"aro-hcp-msi-mock-cs-sp-dev-13","principalId":"b117cf0f-0276-4486-9691-4f00f5a90e1b"},{"name":"aro-hcp-msi-mock-cs-sp-dev-14","principalId":"8d1ff6d0-aadd-4331-9171-9443ac5f4337"},{"name":"aro-hcp-msi-mock-cs-sp-dev-15","principalId":"3e3ad333-db6b-46df-b57d-6f0989dda8b2"},{"name":"aro-hcp-msi-mock-cs-sp-dev-16","principalId":"b26c2343-0863-4a55-bb7e-dfb2544999c1"},{"name":"aro-hcp-msi-mock-cs-sp-dev-17","principalId":"39eefc4f-5e24-491d-9f09-bcc0ad573bcf"},{"name":"aro-hcp-msi-mock-cs-sp-dev-18","principalId":"ccbf438f-08ec-4e2b-ae4e-219ce7dc3762"},{"name":"aro-hcp-msi-mock-cs-sp-dev-19","principalId":"12eafc4e-f869-4b62-8198-816b7d6d0876"}]'
```

### Step 3: Provision Identity Containers

Use the `apply-identity-pool` command with the `--subscription` flag to target only the relevant pool:

```sh
go run ./test/cmd/aro-hcp-tests slot-manager apply-identity-pool \
  --environment dev \
  --subscription "Hypershift Managed Azure"
```

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

Changes to the pooled MSI mock principal list (additions or removals in `config/config-dev-ci.yaml` → `msiMockPool.principals`) require re-running Step 2 with the updated `msiMockPoolPrincipals` parameter.

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
| `aro-dev-msi-mock2` | `d6b62dfa-87f5-49b3-bbcb-4a687c4faa96` | MSI mock — federated credentials, networking, Key Vault access |
| `aro-hcp-msi-mock-cs-sp-dev-{0..19}` | *(see Step 2 command)* | Pooled MSI mocks for parallel presubmit job isolation |

## See Also

- [E2E Subscription Onboarding](e2e-subscription-onboarding.md) — internal subscription procedure
- [Dev-CI Topology](dev-ci-topology.md)
- [CI Identity Leasing](identity-leasing.md)
