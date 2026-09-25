# Slot Manager Asset Model

Slot-manager leases a complete E2E environment allocation rather than an
isolated Boskos resource. The acquired slot is the source of truth for:

- the deployment environment;
- the E2E subscription and any required infrastructure subscription;
- the runtime region;
- the primary and asset Boskos resources;
- every asset assigned to the job; and
- the runtime contract consumed by downstream CI steps.

The catalog describes pool intent. Acquired state records the exact resolved
allocation. Asset handlers own provisioning, admission, and publication for
their asset type.

## Implementation status

Dedicated E2E identity assets are implemented. The v2 catalog, whole-catalog
independent inventory calculation, Boskos generation, multi-lease journal, and
registry lifecycle are implemented and exercised with fake independent asset
handlers. The checked-in live catalog uses v2 with only dedicated E2E identity
assets. It preserves the existing Boskos resources, identity-container names,
capacity, subscription ownership, and region policies. Acquisition now runs
credential-backed identity admission and uses the v2 release journal.

The live catalog declares no infrastructure subscription, infrastructure asset
demand, or independent asset pool. Slot-manager therefore does not resolve or
access an infrastructure subscription. Existing `--deploy-env` callers remain
supported; switching to `--environment` is not a rollout prerequisite.

Infrastructure identities below describe the forward product contract, not a
working Azure provisioner. Their typed demand and inventory can be validated
and rendered to Boskos, but acquisition and pool management reject that demand
before any network operation because no production handler is registered.
An infrastructure identity handler, its product Bicep consumers, rollout, and
mock identity assets are future work. Generating Boskos inventory does not
prove backing Azure resources exist or admit a lease.

## Design goals

The model must:

- make pool identity independent of subscription and region;
- keep deployment and subscription bindings in one authoritative place;
- derive asset-pool capacity from complete catalog demand;
- let new asset types join the lifecycle without adding asset-specific command
  orchestration;
- persist enough state to release every lease after interruption;
- prepare and validate mutable assets before provisioning starts;
- fail closed when an asset cannot be completely inspected or cleaned;
- publish one collision-checked, non-secret runtime contract; and
- preserve stable Azure and Boskos resource names where inventory already
  exists.

## Catalog

The catalog groups named pools into logical environments:

```yaml
version: 2

asset_pools:
- name: dev-infrastructure-identities
  kind: infrastructure_identities
  provisioning: managed
  boskos_resource_type: aro-hcp-dev-infrastructure-identities
  resource_name_prefix: aro-hcp-dev-infrastructure-identities

environments:
  dev:
    deployment_environment:
      name: ci01
      infrastructure_subscription: "ARO HCP E2E Infrastructure (EA Subscription)"

    pools:
    - name: shard0

      region_mode: weighted
      regions:
      - westus3
      - centralus
      - canadacentral

      slot_count: 5

      subscriptions:
        e2e: "ARO HCP E2E Hosted Clusters (EA Subscription)"

      slot_assets:
        e2e_identities:
          allocation: dedicated
          provisioning_region: westus3
          resource_group_prefix: aro-hcp-msi-container-dev-shard0
          resource_group_count: 60

        infrastructure_identities:
          allocation: leased
          asset_pool: dev-infrastructure-identities
```

### Pool identity

`name` is the stable identity of a pool within an environment. Subscription and
region are pool properties, not pool identifiers.

New Boskos and asset names derive from:

```text
<environment> + <pool name> + <slot index>
```

For example:

```text
resource type: aro-hcp-dev-shard0-slot
slot name:     aro-hcp-dev-shard0-slot-00
```

Explicit naming overrides are allowed when required to preserve existing
inventory. There is no hidden global ordinal that maps slots to assets.

### Deployment environment

Every logical environment declares exactly one deployment environment.
Its infrastructure subscription is required only when a pool in that environment
declares an infrastructure asset. Acquiring any pool in that environment determines
the deployment environment used by all downstream steps:

```text
select logical environment
  -> acquire pool slot
  -> use environment deployment_environment
```

`deployment_environment.name` must match `[A-Za-z0-9][A-Za-z0-9_-]*`.
It is used as part of a cluster-profile filename and cannot contain path
separators or traversal components.

Workflow configuration must not override the acquired deployment environment.
An alternative deployment environment is a separate logical environment with
its own infrastructure footprint and asset demand, not a fallback within the
same environment.

### Subscriptions

Every pool declares `subscriptions.e2e`, where tests create HCP resources and
where E2E identity assets live. The environment's
optional `deployment_environment.infrastructure_subscription` locates declared
infrastructure assets. A persistent environment can omit it entirely: the test
identity does not need access to the service's infrastructure tenant or subscription.

For example, an E2E-only pool in a persistent environment needs only this
deployment binding, alongside its customer subscription and E2E asset declaration:

```yaml
deployment_environment:
  name: int
```

Infrastructure demand makes the environment binding mandatory. In an environment
with mixed pool requirements, an E2E-only selection still does not resolve or
access the infrastructure subscription, even if another pool requires it.

Cluster profiles distribute credentials but do not define slot behavior. After
selecting a pool, slot-manager:

1. resolves the pool's E2E subscription to exactly one cluster profile;
2. resolves the E2E subscription name to its Azure subscription ID;
3. only when the selected pool demands an infrastructure asset, resolves the
   environment infrastructure subscription and verifies it against the profile's
   `infra-<deployment-environment>-subscription-id` binding; and
4. persists the required resolved subscription names and IDs.

Missing or mismatched required bindings fail acquisition. Without infrastructure
demand, no infrastructure profile file is read and no infrastructure subscription
is required in acquired state or runtime exports.

### Credentials

Acquisition and identity admission read `tenant`, `client-id`, and `client-secret`
from the selected mounted cluster profile and construct an explicit Azure SDK
`ClientSecretCredential`. They do not require an earlier `az login`, Azure CLI
credentials, or exported `AZURE_*` variables.

The acquire step must mount the same applicable profile credentials used by the
test step. The selected identity needs permission to enumerate the E2E identity
inventory and role assignments, and delete leased principals' FICs and role
assignments. Missing credential files or insufficient permissions fail admission.
No credentials are written to shared runtime state.

### Region selection

Each pool has one region mode:

- `fixed`: the pool declares one authoritative runtime region;
- `runtime-selected`: the caller supplies a permitted runtime region, with the
  pool region as fallback; or
- `weighted`: the pool declares an ordered region allowlist and the job
  supplies weights.

Weighted selection is deterministic for one CI run:

```text
bucket = fnv1a64(BUILD_ID) % sum(weights)
```

The first region whose cumulative positive weight contains the bucket wins.
An explicit permitted location override takes precedence. Downstream steps use
the selected region from the runtime contract and must not repeat selection.

### Asset declarations

The presence of an entry under `slot_assets` means that each slot requires that
asset and slot-manager must acquire or resolve, prepare, validate, and publish
it.

Absence means slot-manager has no contract for that asset. There is no
`mode: none` or `mode: on-demand`.

Every declared asset uses one allocation strategy:

- `dedicated`: the asset is deterministically derived from the primary slot and
  does not require another Boskos lease; or
- `leased`: slot-manager acquires an additional resource from a referenced
  top-level `asset_pool`.

`allocation` describes assignment topology, not whether an asset is required.
Asset presence already means required.

`provisioning` controls pool-management behavior:

- omitted or `managed`: slot-manager may create or update backing resources;
- `unmanaged`: another owner provisions the backing resources.

Provisioning ownership does not alter admission. Every declared leased asset is
prepared and validated before publication.

Implementation details that are intrinsic to an asset, such as the standard
E2E identity set, belong in the handler and its provisioning code rather than
in the catalog.

### Independent asset pools

Top-level `asset_pools` define independently leased inventory. An asset-pool
definition owns:

- one globally unique name;
- one asset kind;
- managed or unmanaged provisioning;
- one Boskos resource type;
- stable resource naming; and
- asset-specific provisioning configuration.

It does not declare an independently chosen capacity or repeat a concrete
subscription. Consumers contribute demand, and the asset kind determines
whether its resources use the consumer environment's E2E or infrastructure
subscription.

For each asset pool, slot-manager computes:

```text
required capacity =
  sum(consumer pool slot_count * units_per_slot)
```

`units_per_slot` defaults to one. This capacity covers the full catalog demand
without hidden oversubscription.

All consumers of one asset pool must resolve to a compatible subscription and
asset configuration. For infrastructure identities, every consumer must
resolve to the same environment infrastructure subscription. Incompatible
references are invalid.

Slot-manager uses the derived capacity to generate and validate Boskos
resources. For managed pools it also creates or reconciles the backing Azure
resources. For unmanaged pools it validates that the external inventory
contains enough matching resources. Unknown, unused, under-capacity, or
multiply defined asset pools are invalid.

## Asset registry

Slot-manager constructs one deterministic registry of asset handlers. Commands
and acquisition orchestration operate only on the registry; they do not switch
on concrete asset types.

Each handler implements:

- `Kind`: returns the stable asset type identifier;
- `Declared`: reports whether a pool declares the asset;
- `AcquireLease`: resolves a dedicated asset or acquires the required
  independent Boskos resources and records them in state;
- `ReleaseLease`: attempts to return every independent Boskos resource recorded
  for the asset;
- `ApplyPools`: creates or updates managed backing resources;
- `ValidatePools`: validates declared backing resources;
- `PrepareLease`: removes mutable state from the previous consumer;
- `ValidateLease`: proves the asset is ready for reuse; and
- `PublishLease`: contributes owned values to the runtime contract.

Registry dispatch preserves registration order, filters by requested asset
kind, adds asset context to failures, and stops lease admission on the first
failure.

Adding an asset requires:

1. typed catalog and resolved-state fields;
2. one handler implementing the lifecycle; and
3. one registry registration.

The generic commands and acquisition path then understand the asset
automatically.

## Acquisition and admission

Acquisition follows this sequence:

```text
load and validate catalog
  -> select logical environment and candidate pools
  -> acquire primary Boskos slot
  -> immediately persist the primary lease
  -> resolve cluster profile and subscriptions
  -> acquire or resolve all declared assets in registry order
     (persist each independent lease immediately, before resolving its assets)
  -> prepare all declared assets
  -> validate all declared assets
  -> build core and asset runtime exports
  -> atomically publish runtime contract
```

Independent asset leases are acquired in deterministic registry order, with a
separate `--lease-proxy-timeout` budget for every required Boskos acquisition.
The primary slot's optional infinite `--max-wait-for-lease` is never reused for
secondary acquisitions. This prevents indefinite hold-and-wait when one asset
pool is exhausted.

State is updated with every resolved lease before preparation begins. The
release step can therefore return the primary slot and every independently
leased asset if admission fails or the process is interrupted.
Malformed or already-journaled names are rejected before changing the journal,
so a duplicate acquisition response cannot prevent cleanup of existing leases.

If acquisition fails after obtaining only part of the required lease set,
slot-manager immediately attempts to release everything recorded so far. If
that cleanup or the process itself fails, Test Platform's job-lifecycle
reconciliation provides the same eventual lease cleanup guarantee as static
ci-operator Boskos leases.

The runtime contract is withheld until every declared asset passes admission.
Downstream provisioning cannot start with a partially prepared slot.

### Failure behavior

Admission is fail-closed:

- an unknown or missing resource fails;
- incomplete inventory fails;
- an Azure list or delete error fails;
- unexpected resource shape fails;
- residue found by validation fails; and
- a runtime-export ownership conflict fails.

Preparation is idempotent so a later acquisition can safely retry after an
interruption. Repeated failures identify the environment, pool, slot, and asset
in logs so operators can remove the Boskos resource from circulation while
repairing it.

Release is best-effort across the complete recorded lease set: one failed
return does not prevent attempts for the remaining asset leases or the primary
slot. Slot-manager reports the joined errors after every return has been
attempted.

## Acquired state

Resolved state records the exact allocation:

```yaml
version: 2
deploy_environment: ci01
runtime_region: westus3

leases:
  primary:
    resource_type: aro-hcp-dev-shard0-slot
    resource_name: aro-hcp-dev-shard0-slot-00
  assets:
    infrastructure_identities:
    - resource_type: aro-hcp-dev-infrastructure-identities
      resource_name: aro-hcp-dev-infrastructure-identities-03

slot:
  environment: dev
  pool_name: shard0
  deploy_environment: ci01
  resource_type: aro-hcp-dev-shard0-slot
  resource_name: aro-hcp-dev-shard0-slot-00
  slot_index: 0

  subscriptions:
    e2e:
      name: "ARO HCP E2E Hosted Clusters (EA Subscription)"
      id: ...
    infrastructure:
      name: "ARO HCP E2E Infrastructure (EA Subscription)"
      id: ...

  assets:
    e2e_identities:
      allocation: dedicated
      provisioning_region: westus3
      resource_groups:
      - aro-hcp-msi-container-dev-shard0-00-00
      - aro-hcp-msi-container-dev-shard0-00-01
    infrastructure_identities:
      allocation: leased
      resource_groups:
      - aro-hcp-dev-infrastructure-identities-03
      identities:
      - service-cluster-...
      - management-cluster-...
```

The state file is `${SHARED_DIR}/aro-hcp-slot-state.yaml`.

`slot-manager release` reads the exact primary and asset lease names, attempts
to return all of them through the Boskos proxy, and removes the local state and
runtime-contract files after successful release. A missing state file means
there is nothing to release. Any failed Boskos return is reported after all
recorded returns have been attempted.

The v2 state is also a write-ahead release journal. Successful returns are
persisted individually and skipped on retry; failures leave only the remaining
leases to return. A `returning` marker is persisted before each request, then
changed to `returned` on success. An interrupted request or a failed
post-return state write leaves an uncertain outcome: do not blindly return that
name again, since it may already belong to another job. The command reports the
uncertainty and leaves ownership reconciliation to Test Platform. If even the
return intent cannot be persisted, cleanup fails closed rather than creating
an unsafe retry. Other recorded leases are still attempted with independent,
uncancelled bounded contexts.

Partial v2 state does not need resolved subscriptions, a catalog, or installed
asset handlers to release its recorded names. Resolved-state checks instead
gate admission and runtime publication. An existing state file must be released
before starting another acquisition in the same shared directory.

`ValidateForRelease` checks journal integrity without requiring resolution.
State persistence and loading use this check, so loading a journal does not
imply runtime readiness. `Validate` checks the complete resolved allocation,
including agreement between the primary lease, slot, and deployment bindings,
and rejects leases already being returned. Acquisition runs full validation
before preparation, and runtime publication validates again.

## Runtime contract

After successful admission, slot-manager writes
`${SHARED_DIR}/aro-hcp-slot.env`.

Core exports include:

- `ARO_HCP_DEPLOY_ENV`;
- `CUSTOMER_SUBSCRIPTION`;
- `SELECTED_CLUSTER_PROFILE_DIR`;
- `SELECTED_LOCATION`;
- `ARO_HCP_E2E_SLOT_NAME`; and
- `ARO_HCP_E2E_SLOT_RESOURCE_TYPE`.

Asset handlers contribute asset-specific exports. The E2E identity handler
owns `LEASED_MSI_CONTAINERS`.
`INFRA_SUBSCRIPTION_ID` is exported only when the selected pool demands an
infrastructure asset and that subscription has been resolved.

Every export has one owner. Duplicate names fail contract construction. Export
names must be valid shell identifiers, and values are shell-escaped before the
file is atomically published.

The contract contains no credentials. Downstream steps source it and use the
acquired values without re-resolving pool behavior.

## E2E identity asset

The current asset assigns each slot a fixed set of persistent managed-identity
resource groups. Each resource group contains the standard per-HCP identity set
created by `test/e2e-setup/bicep/modules/managed-identities.bicep`.

E2E identities use `allocation: dedicated`. Their resource groups are
deterministically attached to the primary slot because each set must be cleaned
and validated before that same slot can be admitted.

### Resolution

For slot index `N`, the handler expands the configured prefix and count into:

```text
<resource_group_prefix>-<N, two digits>-<M, two digits>
```

where `M` ranges from zero to `resource_group_count - 1`.

The resolved names are persisted in state and later published as
`LEASED_MSI_CONTAINERS`. Runtime validation requires dedicated allocation and
the complete deterministic list above, not merely a nonempty set, before
admission or publication.

### Pool management

`ApplyPools` creates or updates managed identity resource groups declared by
the selected pools. `ValidatePools` performs read-only validation of their
expected shape.

Generic commands expose these operations:

```text
slot-manager apply-pool-assets
slot-manager validate-pool-assets
```

Both commands can filter by environment, pool, subscription, or asset type.
The canonical E2E identity selector is `--asset e2e_identities`.
For E2E identities, both commands skip unmanaged pools by default. An explicit
pool or subscription selection includes them, preserving the operator override
used by the identity-pool commands. Explicitly applying such a selection can
therefore modify unmanaged backing resources.

### Preparation

For every resolved resource group, the handler:

1. enumerates all user-assigned managed identities;
2. requires the exact standard identity set and valid principal IDs;
3. enumerates every federated identity credential on those identities;
4. enumerates role assignments for their principal IDs across the E2E
   subscription, including child scopes;
5. deletes all discovered federated identity credentials and role assignments;
   and
6. waits for the deleted resources to disappear using targeted reads.

Initial discovery lists role assignments once per subscription and indexes them
by principal ID. Convergence checks only the FICs and role assignments deleted
in that preparation, removing confirmed deletions from subsequent checks.
It does not repeat subscription-wide enumeration or re-read unchanged identities.
Identity and credential inventory, as well as deletion, use bounded
concurrency. The complete preparation has an overall deadline.
Convergence reads use exponential backoff starting at five seconds, doubling
to a one-minute maximum between attempts. The convergence wait has a two-minute
deadline, and cancellation interrupts both reads and backoff waits.

The reusable baseline is:

- every expected managed identity exists;
- no unexpected managed identity exists;
- no federated identity credentials exist; and
- no role assignments exist for the leased principals.

### Validation

Validation independently re-reads Azure and proves the baseline. It does not
mutate resources or translate an unknown state into success.
This fresh inventory includes a subscription-wide role-assignment scan, so
resources added after initial discovery cannot be hidden by cached results.
Successful admission therefore performs two full inventory passes, regardless
of the number of deletion-convergence checks.

An extra or missing identity, incomplete enumeration, remaining credential, or
remaining role assignment fails acquisition before runtime publication.

The normal E2E framework cleanup remains the fast path after each test. Asset
admission is authoritative because it also handles interrupted jobs and
best-effort cleanup failures.

## Inventory maintenance

The catalog is the source of truth for pool and slot shape. Generic
slot-manager commands derive related infrastructure from it:

- `sync-boskos-config` and `validate-boskos-config` reconcile the managed
  Boskos inventory for primary slots and independently leased asset pools;
- `apply-pool-assets` reconciles managed backing resources; and
- `validate-pool-assets` validates all declared assets, including unmanaged
  assets when explicitly selected.

Inventory generation first expands all primary slots, then computes aggregate
demand for every top-level asset pool. The same derived counts drive Boskos
configuration, managed resource provisioning, and unmanaged inventory
validation so those surfaces cannot drift independently.

Operational onboarding, capacity calculations, and recovery procedures live in
[`docs/ci/identity-leasing.md`](../../../../docs/ci/identity-leasing.md) and
[`docs/ci/e2e-subscription-onboarding.md`](../../../../docs/ci/e2e-subscription-onboarding.md).

## Future assets

The registry allows future assets to be added independently.

### Mock identities

A slot may own an MSI mock service principal, backend ARM helper, and
clusters-service ARM helper. Names derive from environment, pool, and slot.
Their persistent credentials and baseline RBAC require an asset-specific
preparation policy.

### Infrastructure identities

Infrastructure identities use `allocation: leased` and come from a dedicated
top-level asset pool. A lease resolves one complete identity bundle containing
the explicit UAMIs required by one ephemeral service and management
environment.

There is no value in pinning an empty identity bundle to one primary slot.
Before use, the identities have no per-run RBAC or federated credentials, so any
compatible primary slot may consume any free bundle from the infrastructure
identity pool.

The asset pool lives in the consumer environment's infrastructure subscription.
Its required bundle count is derived from the total slot count of every
consumer. Slot-manager generates the corresponding Boskos resources and, when
managed provisioning is enabled, creates or reconciles every identity bundle.

The handler validates the persistent identity set and removes per-run
federated credentials and role assignments during admission. AKS-created
kubelet and Key Vault provider identities remain outside this asset.
