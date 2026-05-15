# CI Identity Leasing

ARO HCP E2E uses two related Boskos-backed leasing mechanisms:

- a **managed identity container pool** used by the test framework when creating HCP-related managed identities
- a **DEV-only MSI mock service-principal pool** used during local-e2e provisioning to spread ARM read traffic across multiple actors

The high-level execution flow is summarized in [CI Execution](execution.md). This document preserves the deeper mechanics that matter when you need to reason about parallelism, pool sizing, workflow wiring, or lease-related failures.

## Why Identity Leasing Exists

Identity leasing exists to solve two different scaling problems:

- **Managed identity reuse:** creating the full set of Azure managed identities for every test would be slow and would consume subscription role-assignment quota too aggressively.
- **Mock service-principal reuse:** if every DEV CI run used the same MSI mock SP, ARM reads would all share the same throttle budget.

The result is a split model:

- the test framework reuses pre-created **identity-container resource groups**
- DEV provisioning reuses a pool of **mock service principals**

Both pools are backed by Boskos resource types, but they are consumed by different parts of the workflow.

## Managed Identity Container Pool

The managed identity container pool is the deeper mechanism behind the short summary in [CI Execution / Identity And Lease Mechanisms](execution.md#identity-and-lease-mechanisms).

### Design And Runtime Behavior

- **Two modes of operation**
  - **Pooled mode** (default in CI) is enabled when `POOLED_IDENTITIES=true`. In this mode tests reuse pre-created identity containers, which are resource groups that hold the well-known managed identities for a single HCP cluster.
  - **Non-pooled mode** (`POOLED_IDENTITIES=false`) creates identities directly in the cluster resource group using suffixed names. This is mainly for local or ad-hoc runs.
- **Per-spec leasing protocol**
  - The implementation lives in `test/util/framework/identities_helper.go`.
  - On startup, the test binary reads the `LEASED_MSI_CONTAINERS` environment variable, which contains a space-separated list of resource group names provided by Boskos for the current job.
  - Those resource groups are written into a YAML state file as a list of entries, each with a three-state lease lifecycle:
    - `free`: container is available to any test
    - `assigned`: container has been reserved for a specific Ginkgo spec but is not yet in use
    - `busy`: container is actively being used by that spec
  - Each spec is identified by a stable `specID()`, derived from the Ginkgo spec text and the OS process ID.
  - At the start of a spec, `AssignIdentityContainers()` atomically reserves the required number of containers by transitioning `free -> assigned`. If there are not enough free entries, it returns `ErrNotEnoughFreeIdentityContainers` and retries with backoff until containers become available or the context is cancelled.
  - When a spec actually needs a container, `ResolveIdentitiesForTemplate()` or `DeployManagedIdentities()` calls `useNextAssigned(specID)`, which transitions a single entry from `assigned -> busy` and returns its resource group name.
  - During cleanup, `releaseLeasedIdentities()` transitions all containers leased by that spec back to `free` and performs best-effort cleanup of federated identity credentials and role assignments in the identity-container resource group.
- **Identity naming**
  - The set of managed identities in each container is fixed and defined in `NewDefaultIdentities()` in `identities_helper.go`, including names such as `cluster-api-azure`, `control-plane`, `cloud-controller-manager`, `image-registry`, and `service`.
  - In pooled mode these canonical names are reused as-is in every identity-container resource group.
  - In non-pooled mode the same base names are suffixed with the cluster name to ensure uniqueness within the cluster resource group.

### Worker Coordination And State Files

The [openshift-tests-extension](https://github.com/openshift-eng/openshift-tests-extension) parallelization model runs a parent test process that spawns multiple OS worker processes for Ginkgo specs.

These workers coordinate identity leases via:

- a shared YAML state file
- a separate lock file

Each leasing operation follows the same pattern:

1. take the lock
2. load state from disk
3. modify it in memory
4. persist the updated state back to disk
5. release the lock

The YAML state file is created on first use from `LEASED_MSI_CONTAINERS` and then treated as the single source of truth for the lifetime of the job.

### Prow, Ci-Operator, And Boskos Configuration

For background on how leases work in OpenShift CI, see:

- [Quota and Leases](https://docs.ci.openshift.org/docs/architecture/quota-and-leases/)
- [Step Registry - Leases](https://docs.ci.openshift.org/docs/architecture/step-registry/#leases)

The Boskos configuration is generated from `openshift/release: core-services/prow/02_config/generate-boskos.py`.

It defines four resource types that back the identity containers:

- `aro-hcp-test-msi-containers-dev`
- `aro-hcp-test-msi-containers-int`
- `aro-hcp-test-msi-containers-stg`
- `aro-hcp-test-msi-containers-prod`

Each Boskos resource name corresponds 1:1 to an Azure resource group that contains the managed identities needed to create a single HCP cluster.

E2E jobs request identity container leases from Boskos via ci-operator `leases:` sections, which populate `LEASED_MSI_CONTAINERS` with a space-separated list of resource names:

- presubmit jobs in `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml`
- EV2 gating jobs in `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__e2e.yaml`
- DEV local E2E in `openshift/release: ci-operator/step-registry/aro-hcp/local-e2e/aro-hcp-local-e2e-workflow.yaml`

A typical leasing stanza looks like:

```yaml
leases:
  - resource_type: aro-hcp-test-msi-containers-dev
    env: LEASED_MSI_CONTAINERS
    count: 20
```

The `LEASED_MSI_CONTAINERS` environment variable is then consumed by the test framework. If it is empty while `POOLED_IDENTITIES=true`, the test run fails fast with a clear error.

### Toggling Pooled Vs Non-Pooled Identities

The test steps that actually run the `aro-hcp-tests` binary define `POOLED_IDENTITIES`:

- `ci-operator/step-registry/aro-hcp/test/local/aro-hcp-test-local-ref.yaml`
- `ci-operator/step-registry/aro-hcp/test/persistent/aro-hcp-test-persistent-ref.yaml`

Both set `POOLED_IDENTITIES` with default `"true"`.

In the test framework:

- `true` uses the Boskos-backed identity containers and the lease state machine
- `false` skips Boskos and creates identities directly in the cluster resource group

### Pool Sizing And Subscription Constraints

The `identity-pool` defaults are defined in `test/cmd/aro-hcp-tests/identity-pool/pools.go`.

That file encodes the quota model directly in comments:

- resource-group-scope HCPs consume fewer role assignments
- one suite path still uses the higher resource-scope cost
- the pool must leave quota headroom for other activity in the subscription

Current defaults:

- DEV: size 300, westus3
- INT: size 150, uksouth
- STG: size 150, uksouth
- PROD: size 150, uksouth

The `identity-pool apply` code also validates a `SubscriptionIDHash` prefix before applying a pool.

### Managing The Identity Pools

The maintenance CLI lives in `test/cmd/aro-hcp-tests/identity-pool/`.

Typical usage:

```bash
./test/aro-hcp-tests identity-pool apply --environment dev
./test/aro-hcp-tests identity-pool apply --environment int --pool-size 150
```

The apply path:

- reads the embedded `msi-pools.json` template generated from `test/e2e-setup/bicep/msi-pools.bicep`
- applies it as a deployment stack named `aro-hcp-msi-pool`
- validates the current subscription against the expected `SubscriptionIDHash`

Any time you change either:

- the Boskos counts in `generate-boskos.py`, or
- the default pool sizing logic in `identity-pool/pools.go`,

you must do both:

1. regenerate the Boskos configuration in `openshift/release`
2. reapply the identity pool in each affected subscription

### Operational Notes And Troubleshooting

- when the pool is saturated, specs block inside `AssignIdentityContainers()`
- the framework records dedicated timing steps such as `Assign N identity containers`, `Lease identity container`, and `Release leased identities`
- this lets you separate infra wait time from actual test logic when reviewing artifacts

Common failure modes:

- **`expected envvar LEASED_MSI_CONTAINERS to not be empty`**
  - the job did not request Boskos leases or the leases failed to be assigned
- **`no assigned identity containers available for <specID>`**
  - the spec tried to consume more containers than it reserved, or skipped the normal reservation path
- **persistent FIC or role-assignment leakage in identity-container resource groups**
  - investigate the container resource group directly in Azure; repeated leftovers usually mean permission issues or unexpected extra resources

## MSI Mock Service Principal Pool

The MSI mock SP pool is DEV-only and solves a different problem from the managed identity container pool.

### Pooled MSI Mock SPs With Boskos

A pool of 20 MSI mock service principals is distributed across concurrent CI jobs via Boskos leasing. Each local E2E job leases one SP from the pool so ARM read traffic is spread across different actors.

Personal development environments continue using the existing single `miMockClientId` / `miMockPrincipalId` / `miMockCertName` configuration unchanged, so they share one ARM throttle budget.

### Infrastructure Setup

The number of identities is controlled by `MSI_MOCK_POOL_SIZE` in `dev-infrastructure/Makefile` (default 20).

Typical setup flow:

```bash
cd dev-infrastructure/

# Create certificates in Key Vault, app registrations and role assignments
make create-msi-mock-pool

# Grant the pool SPs access to the E2E test subscription
make grant-msi-mock-pool-e2e-access

# Populate the pool manifest with the real IDs
make populate-msi-mock-pool
```

This populates `dev-infrastructure/openshift-ci/msi-mock-pool.yaml` with the live client IDs and principal IDs.

### Boskos Configuration

To change the naming or number of MSI mock SPs, update `openshift/release: core-services/prow/02_config/generate-boskos.py`:

```python
for i in range(20):
    CONFIG['aro-hcp-msi-mock-cs-sp-dev']['aro-hcp-msi-mock-cs-sp-dev-{}'.format(i)] = 1
```

### Lease Configuration

To change the naming or number of MSI mock SP leases in job configuration, update `openshift/release: ci-operator/step-registry/aro-hcp/local-e2e/aro-hcp-local-e2e-workflow.yaml`. Each job requests a single lease from the pool:

```yaml
leases:
  - resource_type: aro-hcp-msi-mock-cs-sp-dev
    env: LEASED_MSI_MOCK_SP
    count: 1
```

The leased SP is then consumed during environment provisioning in `openshift/release: ci-operator/step-registry/aro-hcp/provision/environment/aro-hcp-provision-environment-commands.sh`, overriding the default mock SP values:

```bash
MSI_MOCK_CLIENT_ID=$(yq ".miMockPool.\"${LEASED_MSI_MOCK_SP}\".clientId" dev-infrastructure/openshift-ci/msi-mock-pool.yaml)
MSI_MOCK_PRINCIPAL_ID=$(yq ".miMockPool.\"${LEASED_MSI_MOCK_SP}\".principalId" dev-infrastructure/openshift-ci/msi-mock-pool.yaml)
MSI_MOCK_CERT_NAME=$(yq ".miMockPool.\"${LEASED_MSI_MOCK_SP}\".certName" dev-infrastructure/openshift-ci/msi-mock-pool.yaml)
```

## Where To Look

When you need to change or debug identity leasing, start here:

- ARO HCP test framework: `test/util/framework/identities_helper.go`
- identity-pool CLI: `test/cmd/aro-hcp-tests/identity-pool/`
- release-side local workflow: `openshift/release: ci-operator/step-registry/aro-hcp/local-e2e/aro-hcp-local-e2e-workflow.yaml`
- release-side persistent workflow: `openshift/release: ci-operator/step-registry/aro-hcp/e2e/aro-hcp-e2e-workflow.yaml`
- release-side provision step: `openshift/release: ci-operator/step-registry/aro-hcp/provision/environment/`
- Boskos inventory: `openshift/release: core-services/prow/02_config/generate-boskos.py`
- mock-SP pool setup: `dev-infrastructure/Makefile` and `dev-infrastructure/openshift-ci/msi-mock-pool.yaml`

## See Also

- [CI Overview](README.md)
- [CI Execution](execution.md)
- [CI Operations](operations.md)
- [CI EV2 Integration](ev2-integration.md)
