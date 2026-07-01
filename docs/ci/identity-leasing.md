# CI Identity Leasing

ARO HCP E2E uses two related Boskos-backed leasing mechanisms:

- a **managed identity container pool** used by the test framework when creating HCP-related managed identities
- a **DEV-only MSI mock service-principal pool** used during local E2E provisioning to spread ARM read traffic across multiple actors

The important operational distinction today is that the managed identity container pool is acquired in two different ways:

- DEV `e2e-parallel` uses `slot-manager` through the `aro-hcp-local-e2e` workflow
- all other E2E jobs still use the older ci-operator `leases:` path directly

The high-level execution flow is summarized in [CI Execution](execution.md). This document preserves the deeper mechanics that matter when you need to reason about parallelism, pool sizing, workflow wiring, or lease-related failures.

## Why Identity Leasing Exists

Identity leasing exists to solve two different scaling problems:

- **Managed identity reuse:** heavy identity creation and deletion churn consumes Azure directory quota. When the limit is reached, no more identities can be created until the monthly Azure process runs and soft-deleted objects are permanently purged, releasing quota.
- **Mock service-principal reuse:** if every DEV CI run used the same MSI mock SP, ARM reads would all share the same throttle budget.

The result is a split model:

- the test framework reuses pre-created **identity-container resource groups**
- DEV provisioning reuses a pool of **mock service principals**

Both pools are backed by Boskos resource types, but they are consumed by different parts of the workflow. Both the directory quota and the role-assignment quota are actively monitored — see [CI Quota Monitoring](quota-monitoring.md).

## Managed Identity Container Pool

The managed identity container pool is the deeper mechanism behind the short summary in [CI Execution / Identity And Lease Mechanisms](execution.md#identity-and-lease-mechanisms).

### Shared Test-Framework Behavior

Regardless of how CI acquired the pool, the runtime behavior inside the test binary is the same.

- **Two modes of operation**
  - **Pooled mode** (default in CI) is enabled when `POOLED_IDENTITIES=true`. In this mode tests reuse pre-created identity containers, which are resource groups that hold the well-known managed identities for a single HCP cluster.
  - **Non-pooled mode** (`POOLED_IDENTITIES=false`) creates identities directly in the cluster resource group using suffixed names. This is mainly for local or ad-hoc runs.
- **Per-spec leasing protocol**
  - The implementation lives in `test/util/framework/identities_helper.go`.
  - On startup, the test binary reads the `LEASED_MSI_CONTAINERS` environment variable, which contains a space-separated list of resource group names made available to the current job.
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

### How CI Acquires Identity Containers Today

For background on how leases work in OpenShift CI, see:

- [Quota and Leases](https://docs.ci.openshift.org/docs/architecture/quota-and-leases/)
- [Step Registry - Leases](https://docs.ci.openshift.org/docs/architecture/step-registry/#leases)

#### DEV `e2e-parallel`: slot-managed acquisition

The only live slot-manager consumer today is the DEV `e2e-parallel` job in `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml`.

That job uses `openshift/release: ci-operator/step-registry/aro-hcp/local-e2e/aro-hcp-local-e2e-workflow.yaml`, whose pre-steps start with:

- `aro-hcp-lease-acquire`
- `aro-hcp-write-config`
- `aro-hcp-provision-environment`

The acquire step calls `./test/aro-hcp-tests slot-manager acquire`, which:

- maps `ARO_HCP_DEPLOY_ENV` to a slot-catalog environment
- resolves candidate pools from `test/e2e-config/e2e-slots.yaml`
- acquires one slot from Boskos
- exports a non-secret runtime contract into `${SHARED_DIR}/aro-hcp-slot.env`

That runtime contract includes:

- `CUSTOMER_SUBSCRIPTION`
- `SELECTED_LOCATION`
- `LEASED_MSI_CONTAINERS`
- `ARO_HCP_E2E_SLOT_NAME`
- `ARO_HCP_E2E_SLOT_RESOURCE_TYPE`

Downstream steps then source that file and map `SELECTED_LOCATION` to the runtime `LOCATION` they consume. The test framework still sees `LEASED_MSI_CONTAINERS`; the difference is that slot-manager now decides which subscription, slot, and identity-container set back that variable.

#### Higher environments: legacy ci-operator leases

INT, STG, and PROD E2E jobs still use the legacy acquire model.

Those jobs run the persistent workflow in `openshift/release: ci-operator/step-registry/aro-hcp/e2e/aro-hcp-e2e-workflow.yaml`, which does not call slot-manager acquire or release. Instead, the job definitions in:

- `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml`
- `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__e2e.yaml`
- `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__periodic.yaml`

still request environment-specific identity-container resource types through ci-operator `leases:`. Those leases populate `LEASED_MSI_CONTAINERS` directly, and the test framework consumes them exactly as it did before the DEV slot-manager rollout.

### Subscription Sharding And Region Selection

The slot-manager path is what lets DEV CI shard `e2e-parallel` across multiple customer subscriptions without forking the workflow or the test binary.

The current model is:

- the canonical DEV slot inventory lives in `test/e2e-config/e2e-slots.yaml`
- each slot pool has a Boskos `resource_type`, a customer `subscription_name`, slot count, and identity-container settings
- `slot-manager acquire` maps `ARO_HCP_DEPLOY_ENV` to the catalog environment and builds an ordered candidate pool list
- `ALLOWED_SUBSCRIPTIONS` narrows the candidate pool set when a job needs to pin or restrict shard selection
- when `region_mode: runtime-selected` is used, the concrete runtime region is driven by the job's runtime override and exported as `SELECTED_LOCATION`

The current DEV rollout intentionally keeps the implementation details in [slot-manager design](../../test/cmd/aro-hcp-tests/slot-manager/DESIGN.md). For day-to-day CI understanding, the important points are:

- subscription sharding is driven by the slot catalog and slot-manager candidate pool selection
- candidate pools are tried in catalog order when more than one pool is eligible
- the active runtime region is controlled by the live `openshift/release` job configuration

This document intentionally does not freeze the current region value in prose. If you need the current runtime override for a job, inspect the live `openshift/release` config rather than relying on a doc snapshot.

### Toggling Pooled Vs Non-Pooled Identities

The test steps that actually run the `aro-hcp-tests` binary define `POOLED_IDENTITIES`:

- `ci-operator/step-registry/aro-hcp/test/local/aro-hcp-test-local-ref.yaml`
- `ci-operator/step-registry/aro-hcp/test/persistent/aro-hcp-test-persistent-ref.yaml`

Both set `POOLED_IDENTITIES` with default `"true"`.

In the test framework:

- `true` uses the leased identity containers and the lease state machine
- `false` skips pooled leasing and creates identities directly in the cluster resource group

### Pool Sizing And Subscription Constraints

The key limiting factor for identity pool sizing is **Azure role assignments per subscription**. To check current quota usage before resizing pools, see [CI Quota Monitoring](quota-monitoring.md).

Each HCP cluster created during E2E consumes role assignments in its identity container. The cost depends on the RBAC scope mode:

- **`resourceGroupScope`**: 24 role assignments per HCP (11 from E2E test bicep + 13 from the RP-managed resource group)
- **`resourceScope`**: 41 role assignments per HCP (28 from E2E test bicep + 13 from the RP-managed resource group)

The E2E suite runs all tests in `resourceGroupScope` mode except one test path that uses `resourceScope`.

The test-side role-assignment count comes from the managed-identity deployment bicep at `test/e2e-setup/bicep/modules/managed-identities.bicep`, which delegates to:

- `non-msi-scoped-assignments.bicep`: role assignments on customer resources (subnet, vnet, nsg, key vault)
- `msi-scoped-assignments.bicep`: role assignments on the MSI identities themselves (reader, federated credentials)

Each file contains conditional resources gated on the `rbacScope` parameter. To verify the current test-side count for each mode, count the `Microsoft.Authorization/roleAssignments` resources that fire for each `rbacScope` value (including unconditional ones). The RP-managed resource group contributes additional role assignments that are not visible in the test bicep but still consume subscription quota. Since these are created by the RP rather than the test suite, the easiest way to verify the current count is to inspect the role assignments in managed resource groups created in test subscriptions.

Individual test specs may also create additional role assignments beyond this baseline. At the time of writing this is not the common case, but if it grows, the headroom in the formula below may need to be adjusted.

Given a target suite parallelism and a subscription's role-assignment quota, the maximum identity-pool size for the flat legacy model is:

```text
RG_SCOPE_COST  = 24   (current resourceGroupScope cost per HCP)
RES_SCOPE_COST = 41   (current resourceScope cost per HCP)

max-concurrency = floor((role-assignment-quota - 100) / (((suite-parallelism - 1) * RG_SCOPE_COST) + RES_SCOPE_COST))
pool-size       = max-concurrency * suite-parallelism
```

The 100 subtracted from quota is headroom reserved for other activity in the subscription and for any additional role assignments created by individual specs.

For the current live capacity model:

- DEV `e2e-parallel` capacity is determined by the sum of available slots across the eligible shard pools in `test/e2e-config/e2e-slots.yaml`
- higher-environment capacity is still determined by the legacy Boskos pool sizes in `openshift/release: core-services/prow/02_config/generate-boskos.py`
- the active job wiring and runtime-region overrides are defined in the live `openshift/release` ci-operator config

### Scaling Constraints

Two bottlenecks still matter:

**Bottleneck 1: maximum concurrent E2E runs.**

- In the legacy flat-pool model, each E2E job leases a fixed number of identity containers, so:

```text
max-concurrent-runs = floor(pool-size / per-job-lease-count)
```

- In the slot-managed DEV model, concurrency is instead bounded by the number of available slots across the shard pools that the job is allowed to consume.

**Bottleneck 2: parallelism within a single run.** The per-job identity-container set still caps how many HCP clusters a single suite execution can run simultaneously. When the suite has more specs requiring HCPs than available leased containers, specs run in waves — the first wave runs, and the remaining specs block inside `AssignIdentityContainers()` until containers are released. This means adding more test specs increases total suite runtime even if the specs themselves are fast.

The path to higher throughput is still adding subscription capacity, because each additional customer subscription brings its own role-assignment budget and its own managed identity container fleet. In DEV, slot-manager is what lets CI consume that extra capacity through one job family rather than through separate workflows.

### Managing Identity-Container Capacity

For the live DEV slot-managed path:

- update `test/e2e-config/e2e-slots.yaml`
- sync or validate the release-side Boskos inventory with `./test/aro-hcp-tests slot-manager sync-boskos-config` and `./test/aro-hcp-tests slot-manager validate-boskos-config`
- apply the identity pool with `./test/aro-hcp-tests slot-manager apply-identity-pool --environment dev`
- follow [E2E Subscription Onboarding](e2e-subscription-onboarding.md) for the full operator runbook when adding another customer subscription

For higher environments, the identity-container acquisition path is still the older ci-operator `leases:` model. Those jobs are not yet wired to slot-manager acquire or release, so changes there still have to respect the existing `openshift/release` Boskos inventory and job configuration.

### Operational Notes And Troubleshooting

- when the pool is saturated, specs block inside `AssignIdentityContainers()`
- the framework records dedicated timing steps such as `Assign N identity containers`, `Lease identity container`, and `Release leased identities`
- this lets you separate infra wait time from actual test logic when reviewing artifacts

Common failure modes:

- **`expected envvar LEASED_MSI_CONTAINERS to not be empty`**
  - on the slot-managed DEV path, inspect `aro-hcp-lease-acquire` and the runtime slot env export
  - on the legacy higher-environment path, the job likely did not receive the ci-operator lease it expected
- **`no assigned identity containers available for <specID>`**
  - the spec tried to consume more containers than it reserved, or skipped the normal reservation path
- **persistent FIC or role-assignment leakage in identity-container resource groups**
  - investigate the container resource group directly in Azure; repeated leftovers usually mean permission issues or unexpected extra resources

## MSI Mock Service Principal Pool

The MSI mock SP pool is DEV-only and solves a different problem from the managed identity container pool.

It also remains a separate Boskos lease by design. There is no current plan to fold this pool into the slot-manager model, because its purpose is to distribute ARM read traffic during provisioning rather than to drive customer-subscription sharding.

### Pooled MSI Mock SPs With Boskos

A pool of MSI mock service principals is distributed across concurrent DEV local E2E jobs via Boskos leasing. Each job gets one SP from that pool so ARM read traffic is spread across different actors.

Personal development environments continue using the existing single `miMockClientId` / `miMockPrincipalId` / `miMockCertName` configuration unchanged, so they share one ARM throttle budget.

### Infrastructure Setup

The pool currently uses a mixed-management setup. `MSI_MOCK_POOL_SIZE` in `dev-infrastructure/Makefile` still controls the local helper defaults, but customer-subscription RBAC is now reconciled from `config/config-dev-ci.yaml` through the standalone `Microsoft.Azure.ARO.HCP.DevCI.E2ESubscriptionRBAC` rollout.

Typical maintainer flow:

1. From `dev-infrastructure/`, run `make create-msi-mock-pool`.
2. If any pooled principal object IDs changed, update `config/config-dev-ci.yaml` under `ci.dev.devMockIdentities.msiMockPool.principals`.
3. From the repository root, run `make dev-ci-e2e-subscription-rbac-local-run`.
4. From `dev-infrastructure/`, run `make populate-msi-mock-pool`.
5. If the pool size or Boskos key set changed, update the release-side Boskos inventory and step-registry lease wiring as well.

In the current model:

- `make create-msi-mock-pool` is itself hybrid:
  - `dev-infrastructure/templates/mock-identity-pool.bicep` ensures the Key Vault certificate set.
  - `dev-infrastructure/scripts/create-sp-for-rbac.sh` and the surrounding `dev-infrastructure/Makefile` loop still create or update the `aro-dev-msi-mock-pool-<i>` Entra app and service principal objects and apply the home-subscription grants.
- `make dev-ci-e2e-subscription-rbac-local-run` reconciles pooled-principal access on the DEV E2E customer subscriptions from the principal IDs recorded in `config/config-dev-ci.yaml`.
- `dev-infrastructure/configurations/e2e-subscription-rbac-assignments.tmpl.bicepparam` still preserves legacy assignment IDs for the first DEV E2E subscription so the rollout can adopt existing grants without recreating them.
- `make populate-msi-mock-pool` performs live Entra lookups and rewrites `dev-infrastructure/openshift-ci/msi-mock-pool.yaml`, which remains the static catalog consumed by release-side jobs.

### Naming Bridge

The Azure objects and the Boskos leases intentionally use different names:

- Azure app and service principal display name: `aro-dev-msi-mock-pool-<i>`
- Boskos resource key and static catalog key: `aro-hcp-msi-mock-cs-sp-dev-<i>`

`dev-infrastructure/openshift-ci/populate-msi-mock-pool.sh` bridges those two namespaces by looking up the Azure object by display name and writing the resulting client ID and principal ID under the Boskos key in `msi-mock-pool.yaml`.

### Boskos Configuration

To change the naming or number of MSI mock SPs, update `openshift/release: core-services/prow/02_config/generate-boskos.py`.

This Boskos inventory is still a consumer artifact. It is not generated automatically from `config/config-dev-ci.yaml` or from the `dev-ci` rollout today.

### Lease Configuration

The lease itself is currently declared on `openshift/release: ci-operator/step-registry/aro-hcp/provision/environment/aro-hcp-provision-environment-ref.yaml`, not in the top-level local workflow.

That step requests a single lease from the pool and exposes it as `LEASED_MSI_MOCK_SP`. The leased SP is then consumed during environment provisioning in `openshift/release: ci-operator/step-registry/aro-hcp/provision/environment/aro-hcp-provision-environment-commands.sh`, overriding the default mock SP values:

```bash
MSI_MOCK_CLIENT_ID=$(yq ".miMockPool.\"${LEASED_MSI_MOCK_SP}\".clientId" dev-infrastructure/openshift-ci/msi-mock-pool.yaml)
MSI_MOCK_PRINCIPAL_ID=$(yq ".miMockPool.\"${LEASED_MSI_MOCK_SP}\".principalId" dev-infrastructure/openshift-ci/msi-mock-pool.yaml)
MSI_MOCK_CERT_NAME=$(yq ".miMockPool.\"${LEASED_MSI_MOCK_SP}\".certName" dev-infrastructure/openshift-ci/msi-mock-pool.yaml)
```

Jobs only consume the Boskos key and the static `msi-mock-pool.yaml` catalog at runtime. They do not query Entra or the `dev-ci` rollout directly during provisioning.

## Where To Look

When you need to change or debug identity leasing, start here:

- [CI Execution](execution.md)
- [E2E Subscription Onboarding](e2e-subscription-onboarding.md)
- [slot-manager design](../../test/cmd/aro-hcp-tests/slot-manager/DESIGN.md)
- ARO HCP test framework: `test/util/framework/identities_helper.go`
- slot-managed identity-pool code: `test/cmd/aro-hcp-tests/slot-manager/identity-pool/`
- release-side local workflow: `openshift/release: ci-operator/step-registry/aro-hcp/local-e2e/aro-hcp-local-e2e-workflow.yaml`
- release-side persistent workflow: `openshift/release: ci-operator/step-registry/aro-hcp/e2e/aro-hcp-e2e-workflow.yaml`
- release-side acquire step: `openshift/release: ci-operator/step-registry/aro-hcp/lease/acquire/`
- release-side provision step: `openshift/release: ci-operator/step-registry/aro-hcp/provision/environment/`
- slot catalog: `test/e2e-config/e2e-slots.yaml`
- Boskos inventory: `openshift/release: core-services/prow/02_config/generate-boskos.py`
- mock-SP pool setup and mixed management:
  - `config/config-dev-ci.yaml`
  - `dev-infrastructure/Makefile`
  - `dev-infrastructure/dev-ci/e2e-subscription-rbac/pipeline.yaml`
  - `dev-infrastructure/configurations/e2e-subscription-rbac-assignments.tmpl.bicepparam`
  - `dev-infrastructure/openshift-ci/populate-msi-mock-pool.sh`

## See Also

- [CI Overview](README.md)
- [CI Execution](execution.md)
- [E2E Subscription Onboarding](e2e-subscription-onboarding.md)
- [CI Quota Monitoring](quota-monitoring.md)
- [CI Operations](operations.md)
- [CI EV2 Integration](ev2-integration.md)
