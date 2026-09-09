# CI Identity Leasing

ARO HCP E2E uses three related Boskos-backed leasing mechanisms:

- a **managed identity container pool** used by the test framework when creating HCP-related managed identities
- a **DEV-only MSI mock service-principal pool** used during local E2E provisioning to spread ARM read traffic across multiple actors
- a **DEV-only ARM helper service-principal pool** used to give each E2E backend
  an independent CheckAccess request budget

The important operational distinction today is that the managed identity container pool is acquired in two different ways:

- DEV, INT, and STG `e2e-parallel` jobs use `slot-manager` through the `aro-hcp-local-e2e` workflow
- PROD is being migrated onto the same slot-manager model; until its `openshift/release` job wiring lands it still uses the older ci-operator `leases:` path directly

The high-level execution flow is summarized in [CI Execution](execution.md). This document preserves the deeper mechanics that matter when you need to reason about parallelism, pool sizing, workflow wiring, or lease-related failures.

## Why Identity Leasing Exists

Identity leasing exists to solve two different scaling problems:

- **Managed identity reuse:** heavy identity creation and deletion churn consumes Azure directory quota. When the limit is reached, no more identities can be created until the monthly Azure process runs and soft-deleted objects are permanently purged, releasing quota.
- **Mock service-principal reuse:** if every DEV CI run used the same MSI mock SP, ARM reads would all share the same throttle budget.

The result is a split model:

- the test framework reuses pre-created **identity-container resource groups**
- DEV provisioning reuses a pool of **mock service principals**

Both pools are backed by Boskos resource types, but they are consumed by different parts of the workflow. Both the directory quota and the role-assignment quota are actively monitored — see [DEV CI Monitoring and Alert Response](dev-ci-monitoring.md).

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

#### DEV, INT, and STG `e2e-parallel`: slot-managed acquisition

The live slot-manager consumers today are the DEV, INT, and STG `e2e-parallel` jobs in `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml`. PROD is being onboarded onto the same path.

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

#### Remaining legacy ci-operator leases

Any E2E job not yet migrated to slot-manager uses the legacy acquire model. Today that is PROD (during its onboarding) plus the non-`e2e-parallel` job variants such as the `__e2e` and `__periodic` jobs.

Those jobs run the persistent workflow in `openshift/release: ci-operator/step-registry/aro-hcp/e2e/aro-hcp-e2e-workflow.yaml`, which does not call slot-manager acquire or release. Instead, the job definitions in:

- `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml`
- `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__e2e.yaml`
- `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__periodic.yaml`

still request environment-specific identity-container resource types through ci-operator `leases:`. Those leases populate `LEASED_MSI_CONTAINERS` directly, and the test framework consumes them exactly as it did before the slot-manager rollout.

### Subscription Sharding And Region Selection

The slot-manager path is what lets CI shard `e2e-parallel` across multiple customer subscriptions without forking the workflow or the test binary.

The current model is:

- the canonical slot inventory lives in `test/e2e-config/e2e-slots.yaml`
- each slot pool has a Boskos `resource_type`, a customer `subscription_name`, slot count, and identity-container settings
- `slot-manager acquire` maps `ARO_HCP_DEPLOY_ENV` to the catalog environment and builds an ordered candidate pool list
- `ALLOWED_SUBSCRIPTIONS` narrows the candidate pool set when a job needs to pin or restrict shard selection
- when `region_mode: runtime-selected` is used, the concrete runtime region is driven by the job's runtime override and exported as `SELECTED_LOCATION`

The current slot-manager rollout intentionally keeps the implementation details in [slot-manager design](../../test/cmd/aro-hcp-tests/slot-manager/DESIGN.md). For day-to-day CI understanding, the important points are:

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

The key limiting factor for identity pool sizing is **Azure role assignments per subscription**. To check current quota usage before resizing pools, see [DEV CI Monitoring and Alert Response](dev-ci-monitoring.md).

Each HCP cluster created during E2E consumes role assignments in its identity container. The cost depends on the RBAC scope mode:

- **`resourceGroupScope`**: 26 role assignments per HCP (13 from E2E test bicep + 13 from the RP-managed resource group)
- **`resourceScope`**: 41 role assignments per HCP (28 from E2E test bicep + 13 from the RP-managed resource group)

Most specs run in `resourceGroupScope` mode; the exceptions are the specs that pass `framework.RBACScopeResource`. Derive that count from the test source rather than trusting a number written here — this sentence has been stale before:

```bash
git grep -o "framework.RBACScopeResource," -- test/e2e/ | wc -l   # 3 at the time of writing
```

A test path that deploys a pinned back-level copy of the setup bicep is also resource-scoped regardless of what it passes, because those copies have no `rbacScope` parameter and only ever grant resource-scoped RBAC. No such path exists today, but one has existed before and may again.

The test-side role-assignment count comes from the managed-identity deployment bicep at `test/e2e-setup/bicep/modules/managed-identities.bicep`, which delegates to:

- `non-msi-scoped-assignments.bicep`: role assignments on customer resources (subnet, vnet, nsg, key vault)
- `msi-scoped-assignments.bicep`: role assignments on the MSI identities themselves (reader, federated credentials)

Each file contains conditional resources gated on the `rbacScope` parameter. To verify the current test-side count for each mode, count the `Microsoft.Authorization/roleAssignments` resources that fire for each `rbacScope` value (including unconditional ones). The RP-managed resource group contributes additional role assignments that are not visible in the test bicep but still consume subscription quota. Since these are created by the RP rather than the test suite, the easiest way to verify the current count is to inspect the role assignments in managed resource groups created in test subscriptions.

Individual test specs may also create additional role assignments beyond this baseline. At the time of writing this is not the common case, but if it grows, the headroom in the formula below may need to be adjusted.

Given the concurrent HCP demand of a run and a subscription's role-assignment quota, the cost of a run and the maximum number of concurrent runs are:

```text
# Every value below is derived from a checked-in source. The numbers are current
# at the time of writing; re-derive rather than trusting them.

RG_SCOPE_COST   = 26   # unconditional + rbacScope=='resourceGroup' assignments
                       #   in test/e2e-setup/bicep/modules/{non-msi,msi}-scoped-assignments.bicep,
                       #   plus RP-managed assignments (see the per-HCP costs above)
RES_SCOPE_COST  = 41   # same, for rbacScope=='resource'
RES_SCOPED_HCPS = 3    # git grep -o "framework.RBACScopeResource," -- test/e2e/ | wc -l

identity_container_count = 60   # the leasing pool's per-slot container count, from
                                #   test/e2e-config/e2e-slots.yaml. Differs per pool
                                #   (20, 25 and 60 today), so run-cost is per pool too.

# Concurrent HCPs is NOT the Ginkgo worker count. Some specs lease more than one
# identity container each, so P workers can hold more than P clusters.
#
# Let demand(spec) be the value of that spec's labels.MIContainers decorator,
# taken over the specs the run actually selects, and let P = suite-parallelism.
hcp-concurrency = min(
    sum of demand(spec) over the selected specs,   # total declared demand
    identity_container_count,                      # leased pool ceiling
    sum of the P largest demand(spec) values       # worker bound
)

# A run cannot hold more resource-scoped clusters than clusters. Assuming every
# resource-scoped spec is among those running concurrently is the worst case.
res-scoped-concurrent = min(RES_SCOPED_HCPS, hcp-concurrency)

run-cost        = ((hcp-concurrency - res-scoped-concurrent) * RG_SCOPE_COST) + (res-scoped-concurrent * RES_SCOPE_COST)
max-concurrency = floor((role-assignment-quota - 100) / run-cost)
```

The 100 subtracted from quota is headroom reserved for other activity in the subscription and for any additional role assignments created by individual specs.

`suite-parallelism` is declared per suite in `test/cmd/aro-hcp-tests/main.go` and can be overridden at runtime by `ARO_HCP_SUITE_PARALLELISM`, which the CI job configuration in `openshift/release` sets. That override is not visible from this repository, so check the job config rather than assuming the source literal applies.

`run-cost` is the cost of a single run. To answer whether a change fits within a
subscription's quota — the question that arises whenever role assignments are
added to the E2E bicep — scale it across the slots that subscription configures:

```text
# Slots and their container counts come from test/e2e-config/e2e-slots.yaml.
# Group pools by subscription_name first: several pools can share one
# subscription and therefore one quota, so their costs add up. Today the INT
# environment is the case that matters.
#
# run-cost is per pool, not per subscription: it depends on
# identity_container_count, which differs between pools (20, 25 and 60 today).
# Compute it separately for each pool rather than reusing one value.
subscription-cost = sum over that subscription's pools of (run-cost(pool) * slot_count(pool))

assert subscription-cost + persistent-baseline <= role-assignment-quota
```

`persistent-baseline` is the role assignments a subscription holds independently of any running suite — subscription-scoped grants for humans and automation, plus long-lived infrastructure. Measure it on an idle subscription rather than assuming; assignments belonging to running clusters cannot be told apart by scope:

```bash
az role assignment list --subscription <name-or-id> --all -o json | jq length
```

This is a *configured worst case*: it assumes every slot runs a suite at peak concurrency simultaneously. Real usage is normally well below it, so a breach means the catalog permits one, not that one has occurred.

Every input above has a stated derivation, so the whole calculation can be re-run from this repository without trusting a figure written here. Do that rather than reusing quoted numbers, which require manual maintenance and have been stale in the past.

For the current live capacity model:

- DEV `e2e-parallel` capacity is determined by the sum of available slots across the eligible shard pools in `test/e2e-config/e2e-slots.yaml`
- higher-environment capacity is still determined by the legacy Boskos pool sizes in `openshift/release: core-services/prow/02_config/generate-boskos.py`
- the active job wiring and runtime-region overrides are defined in the live `openshift/release` ci-operator config

### Scaling Constraints

Three bottlenecks matter:

**Bottleneck 1: maximum concurrent E2E runs.**

- In the legacy flat-pool model, each E2E job leases a fixed number of identity containers, so:

```text
# pool-size here is the total size of the legacy flat identity pool, not a
# quantity from the role-assignment model above.
max-concurrent-runs = floor(pool-size / per-job-lease-count)
```

- In the slot-managed DEV model, concurrency is instead bounded by the number of available slots across the shard pools that the job is allowed to consume.

**Bottleneck 2: parallelism within a single run.** How many HCP clusters a single suite execution holds at once is bounded by both the leased identity-container set and the effective suite parallelism, whichever is smaller — see `hcp-concurrency` above. When the suite has more specs requiring HCPs than can run concurrently, specs run in waves — the first wave runs, and the remaining specs block inside `AssignIdentityContainers()` until containers are released. This means adding more test specs increases total suite runtime even if the specs themselves are fast.

**Bottleneck 3: deny assignments per subscription (AME only — STG and PROD).** The Azure Authorization RP allows at most **2000 deny assignments per subscription**. The RP currently creates *sharded* (per-cluster) deny assignments — roughly 21 per HCP — which caps a single subscription at about **92 concurrent HCP clusters**, regardless of role-assignment quota or identity-pool size:

```text
DENY_ASSIGNMENTS_PER_SUB   = 2000  (Authorization RP hard limit)
DENY_ASSIGNMENTS_PER_HCP   = 21    (current sharded, per-cluster count)

max-hcps-per-sub = floor(DENY_ASSIGNMENTS_PER_SUB / DENY_ASSIGNMENTS_PER_HCP) ≈ 92
```

This applies to **AME environments only (STG and PROD)**: the RP only creates deny assignments in AME. In practice it is the binding constraint for PROD slot sizing — STG runs a single slot per subscription, well under the ceiling. It translates into slot count as:

```text
max-slots-per-sub = floor(max-hcps-per-sub / identity-container-count-per-slot)
```

where `identity-container-count-per-slot` is the pool's `identity_container_count` — an upper bound on the HCPs a single suite run provisions concurrently, not the actual figure; see `hcp-concurrency` above. Using the ceiling here is deliberate and safe, since it overstates rather than understates deny-assignment consumption. The PROD `slot_count` in `test/e2e-config/e2e-slots.yaml` is sized to stay within this cap; that catalog is the source of truth for the current per-subscription values.

The RP is expected to consolidate the per-cluster deny assignments into a single deny assignment with all managed identities excluded once Azure raises the excluded-principals limit from 10 to 25. When that lands, this per-subscription HCP ceiling is lifted and the PROD `slot_count` can be raised accordingly.

The path to higher throughput is still adding subscription capacity, because each additional customer subscription brings its own role-assignment budget and its own managed identity container fleet. In DEV, slot-manager is what lets CI consume that extra capacity through one job family rather than through separate workflows.

### Managing Identity-Container Capacity

For the live DEV slot-managed path:

- update `test/e2e-config/e2e-slots.yaml`
- sync or validate the release-side Boskos inventory with `./test/aro-hcp-tests slot-manager sync-boskos-config` and `./test/aro-hcp-tests slot-manager validate-boskos-config`
- apply the identity pool with `make -C test apply-identity-pool ENVIRONMENT=dev`

  Always apply through this Make target rather than `go run` or a hand-built binary. The target rebuilds `aro-hcp-tests` and, as part of that, regenerates the Bicep-derived ARM artifacts (e.g. `msi-pools.json`) from the source-of-truth Bicep in `test/e2e-setup/bicep/`. The generated artifacts under `test/e2e/test-artifacts/generated-test-artifacts/` are git-ignored build outputs, so bypassing the Make build can embed and apply a stale template — which manifests as resource groups being deleted and recreated instead of updated in place.
- follow [DEV E2E Subscription Onboarding](dev-e2e-subscription-onboarding.md) for the full operator runbook when adding another customer subscription

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

The pooled `aro-dev-msi-mock-pool-<i>` identities are fully declarative on the
Azure side. Their certificates, Entra apps/service principals, pinning, and
subscription RBAC are reconciled by the standalone, **Owner-only**
`Microsoft.Azure.ARO.HCP.DevCI.Privileged` entrypoint. The pool size has one
source of truth: `.ci.dev.mockIdentities.pool.size` in
`config/config-dev-ci.yaml`.

Typical maintainer flow:

1. Change `.ci.dev.mockIdentities.pool.size` in `config/config-dev-ci.yaml`.
2. Ask an OWNERS-group member to run `make dev-ci-privileged-local-run`
   (requires subscription Owner, Key Vault certificate create/read, and
   owner/Application.ReadWrite on the apps). It creates every missing indexed
   certificate and app/SP, pins the current certificate, and applies RBAC.
3. Run `make -C dev-infrastructure populate-msi-mock-pool` to regenerate the
   static Boskos catalog. The target reads the desired size directly from
   `config/config-dev-ci.yaml`.
4. Update the release-side Boskos inventory and step-registry lease wiring.

In the current model:

- `make dev-ci-privileged-local-run` creates the pooled Entra objects
  (`mock-identity-apps.bicep`), creates missing Key Vault certificates and pins
  them via the `pin-mock-certs` Shell step, and reconciles access on the DEV home
  and E2E customer subscriptions (`mock-identity-rbac.bicep`). Decreasing the
  configured size does not delete higher-index resources; they are simply no
  longer reconciled.
- `make populate-msi-mock-pool` performs live Entra lookups and rewrites `dev-infrastructure/openshift-ci/msi-mock-pool.yaml`, which remains the static catalog consumed by release-side jobs.

### Naming Bridge

The Azure objects and the Boskos leases intentionally use different names:

- Azure app and service principal display name: `aro-dev-msi-mock-pool-<i>`
- Boskos resource key and static catalog key: `aro-hcp-msi-mock-cs-sp-dev-<i>`

`dev-infrastructure/openshift-ci/populate-mock-identity-pool.sh` bridges those
two namespaces by looking up each Azure object by display name and writing the
resulting client ID and principal ID under the Boskos key in the pool's static
catalog.

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

## ARM Helper Service Principal Pool

The DEV ARM helper pool prevents concurrent E2E backends from sharing the
third-party-application CheckAccess limit. Each member is an
`aro-dev-arm-helper-pool-<i>` application/service principal with its own pinned
`armHelperPoolCert-<i>` certificate and the same subscription-level Contributor
and Role Based Access Control Administrator grants as `aro-dev-arm-helper2` on
the DEV home and E2E customer subscriptions. The home-subscription grants allow
a pool member to be tested in a personal development environment.

The Azure-side pool size has one source of truth:
`.ci.dev.mockIdentities.armHelperPool.size`. Increasing it causes the privileged
pipeline to create the missing certificate, application/service principal,
pinned credential, and E2E-subscription RBAC for each new index. Decreasing it
does not delete higher-index resources.

Maintainer flow:

1. Change `.ci.dev.mockIdentities.armHelperPool.size`.
2. Run `make dev-ci-privileged-local-run`. The DEV `pin-mock-certs` step
   reconciles the shared identities, MSI mock pool, and ARM helper pool after
   their combined app deployment.
3. Verify token acquisition for every new application. The full entrypoint also
   reconciles the pool's home- and E2E-subscription grants in
   `mock-identity-rbac`.
4. Run `make -C dev-infrastructure populate-arm-helper-pool`. The target reads
   the desired size directly from `config/config-dev-ci.yaml`.
5. Add or update the `aro-hcp-arm-helper-sp-dev` Boskos inventory in
   `openshift/release`;
   after that inventory has rolled out, request two leases as
   `LEASED_ARM_HELPER_SP`.

The runtime catalog is
`dev-infrastructure/openshift-ci/arm-helper-pool.yaml`. An unknown or incomplete
lease entry fails provisioning. The first whitespace-separated lease configures
Backend through `armHelperClientId` and `armHelperCertName`; the second configures
Clusters Service through `clustersServiceArmHelperClientId` and
`clustersServiceArmHelperCertName`. A single lease remains supported during the
transition to the shared `hack/ci` provisioning scripts and configures both
Backend and Clusters Service with that identity. A missing lease preserves all
configured defaults. Neither lease overrides `armHelperFPAPrincipalId`, which is
the shared mock first-party principal rather than an authenticating ARM helper.

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
  - `dev-infrastructure/dev-ci/e2e-subscription-rbac-grants/pipeline.yaml`
  - `dev-infrastructure/configurations/mock-identity-apps.tmpl.bicepparam`
  - `dev-infrastructure/configurations/mock-identity-rbac.tmpl.bicepparam`
  - `dev-infrastructure/openshift-ci/populate-mock-identity-pool.sh`

## See Also

- [CI Overview](README.md)
- [CI Execution](execution.md)
- [E2E Subscription Onboarding](e2e-subscription-onboarding.md)
- [DEV CI Monitoring and Alert Response](dev-ci-monitoring.md)
- [CI Operations](operations.md)
- [CI EV2 Integration](ev2-integration.md)
