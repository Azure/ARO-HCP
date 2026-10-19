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

Catalogs and acquired state require an explicit `version: 2`; other versions,
including missing versions, are rejected. Slot-manager is CI-only: each job's
runner image contains the matching code and catalog, and acquire and release
use that same image. Completed job artifacts are read-only; cross-version
migration and recovery are not supported.

## Package boundaries

- The root `slotmanager` package owns CLI flags, compatibility command aliases,
  and acquisition/release orchestration. `assets_cmd.go` constructs the registry
  explicitly with `e2eidentities.NewHandler()` and
  `infrastructureidentities.NewHandler()`. Both constructors accept optional
  functional options for tests; production uses the no-argument call.
- `assets` defines the handler contract and deterministic registry lifecycle.
  It depends on `slots`, never on concrete handler packages.
- `assets/azureutil` holds shared Azure identity helpers used by both identity
  handlers: cluster-profile credentials, identity-name inventory checks,
  subscription role-assignment collection and delete operations, federated
  credential list/delete operations, worker-bounded admission deletes, and the
  `--cleanup-workers` flag type. It does not own a lifecycle. E2E admission
  uses the shared RBAC helpers only; infrastructure admission also uses the
  federated-credential helpers.
- `assets/e2eidentities` implements the E2E identity lifecycle: Azure provisioning
  (`apply.go`), pool resolution, inventory validation, admission, and runtime
  publication, with their tests alongside the implementation.
- `assets/infrastructureidentities` implements the infrastructure identity
  lifecycle for independently leased bundles: Azure provisioning, inventory
  validation, admission (including federated-credential cleanup), and runtime
  publication of `LEASED_INFRASTRUCTURE_IDENTITY_BUNDLE`.
- `slots` owns catalog and acquired-state types, wire keys, structural validation,
  inventory derivation, and lease journaling. `slots/e2e_identities.go` groups the
  E2E identity model types, normalization, deterministic naming, and structural
  checks; it does not provision or inspect Azure resources. Generic catalog and
  asset orchestration remain in `catalog.go` and `asset_pools.go`.
- Runtime publication combines core exports with handler `PublishLease`
  contributions through the shared runtime contract builder. The E2E identity
  handler exclusively owns `LEASED_MSI_CONTAINERS`. The infrastructure identity
  handler exclusively owns `LEASED_INFRASTRUCTURE_IDENTITY_BUNDLE`.

## Implementation status

Dedicated E2E identity assets are implemented. The v2 catalog, whole-catalog
independent inventory calculation, Boskos generation, multi-lease journal, and
registry lifecycle are implemented and exercised with fake independent asset
handlers. The checked-in live catalog uses v2 with only dedicated E2E identity
assets. It preserves the existing Boskos resources, identity-container names,
capacity, subscription ownership, and region policies. Acquisition now runs
credential-backed identity admission and uses the v2 release journal.

The live catalog still declares no infrastructure subscription, infrastructure
asset demand, or independent asset pool, so current jobs do not resolve or
access an infrastructure subscription. The infrastructure identity handler is
registered: catalogs that declare that demand acquire, admit, provision, and
publish a bundle. Live pool creation, catalog activation, and rollout remain
separate work. Generating Boskos inventory does not prove backing Azure
resources exist. Existing `--deploy-env` callers remain supported; switching to
`--environment` is not a rollout prerequisite.

Mock identity assets are future work.

## Design goals

The model must:

- make pool identity independent of subscription and region;
- keep deployment and subscription bindings in one authoritative place;
- derive asset-pool capacity from complete catalog demand;
- let new asset types join the lifecycle without adding asset-specific command
  orchestration;
- persist enough state to release every lease after interruption;
- admit mutable assets for reuse before provisioning starts;
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
  infrastructure_identities:
    provisioning_region: westus3

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
          resource_group_count: 58

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
inventory and role assignments, and delete leased principals' role
assignments. When a pool demands infrastructure identities, those credentials
must also enumerate the leased bundle identities and federated credentials in
the infrastructure subscription and delete those federated credentials and the
principals' role assignments there. Missing credential files or insufficient
permissions fail admission. No credentials are written to shared runtime state.

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
asset and slot-manager must acquire or resolve, admit, and publish
it.

Every pool must declare `slot_assets.e2e_identities`; a missing or null
declaration fails catalog validation before any lease is acquired. Additional
asset kinds are opt-in: absence means slot-manager has no contract for that
asset. There is no `mode: none` or `mode: on-demand`.

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

Provisioning ownership does not alter admission. Every declared asset must pass
admission before publication unless explicitly disabled with the emergency
opt-out below.

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
- `Declared`: reports whether the handler applies to a pool. E2E identities
  are required for every pool, so their handler always returns true; additional
  asset kinds are opt-in;
- `AcquireLease`: resolves a dedicated asset or acquires the required
  independent Boskos resources and records them in state;
- `ReleaseLease`: attempts to return every independent Boskos resource recorded
  for the asset;
- `ApplyPools`: creates or updates managed backing resources;
- `ValidatePools`: validates declared backing resources;
- `AdmitLease`: inspects and, where necessary, cleans or prepares the allocated
  asset for reuse, returning success only when its readiness requirements are
  met; and
- `PublishLease`: contributes owned values to the runtime contract.

The registry separates handler selection from lifecycle execution.
`ListHandlers` and `GetHandler` expose registered handlers; `FilterHandlers`
selects explicit CLI asset kinds in registration order, with an empty filter
meaning all handlers. `HandlersForRequirements` validates asset requirements
and selects their handlers in registration order, with empty demand meaning
no handlers. Unknown kinds, duplicate requirements, and invalid allocations
are rejected before lease handlers run.

Lifecycle methods use explicit loops, add asset context to failures, and stop
lease admission on the first failure. Pool commands validate all selected
pools' demand and inventories before invoking any handler, even with an asset
filter. Each handler receives only the primary pools that declare its kind
and the referenced asset inventories. Inventory capacities remain derived
from the whole catalog, never from that scoped request.

`PoolRequest.AssetInventories` supplies inventory for pool management.
Lease acquisition receives inventory explicitly through `Registry.AcquireLeases`;
the shared `LeaseRequest` carries `LeaseJournal` and `AcquiredSlotState`, not
inventory needed only during acquisition.
Each `AssetInventory.AssetPool` is a pool definition;
each leased `ExpandedSlot.AssetRequirements` entry refers to an independent pool by
`AssetRequirement.AssetPoolName`. `AssetInventoryForRequirement` matches both
the pool name and asset kind. These Go names do not change catalog or acquired
state YAML/JSON wire names.

Admission is one handler-owned phase, not mandatory preparation and validation
passes. Each handler decides which observations it can reuse and whether an
operation requires confirmation, such as waiting for asynchronous completion.
`ValidatePools` remains a separate read-only operator command.

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
  -> admit declared assets except explicitly disabled kinds
  -> build core and asset runtime exports
  -> atomically publish runtime contract
```

Independent asset leases are acquired in deterministic registry order, with a
separate `--lease-proxy-timeout` budget for every required Boskos acquisition.
The primary slot's optional infinite `--max-wait-for-lease` is never reused for
secondary acquisitions. This prevents indefinite hold-and-wait when one asset
pool is exhausted.

Before acquiring or resolving assets, slot-manager validates every leased
requirement's inventory reference, journal availability, and unit count.
Each acquired independent lease is still persisted immediately, and resolved
state is persisted after each handler before moving to the next handler.

State is updated with every resolved lease before admission begins. The
release step can therefore return the primary slot and every independently
leased asset if admission fails or the process is interrupted.
Malformed or already-journaled names are rejected before changing the journal,
so a duplicate acquisition response cannot prevent cleanup of existing leases.

If acquisition fails after obtaining only part of the required lease set,
slot-manager immediately attempts to release everything recorded so far. If
that cleanup or the process itself fails, Test Platform's job-lifecycle
reconciliation provides the same eventual lease cleanup guarantee as static
ci-operator Boskos leases.

The runtime contract is withheld until every non-disabled asset passes admission.
All assets must still be fully acquired and resolved before publication.

Admission assumes exclusive ownership of the allocated assets: previous
consumers must no longer modify them, and handlers must not invalidate another
asset's readiness. It does not protect against concurrent external writers.

### Admission timeout

All asset admission handlers share a configurable time budget, defaulting to
10 minutes, separate from the lease acquisition and release timeouts.

### Asset admission options

Each asset handler declares its own admission options through
`Handler.AttachOptions(*pflag.FlagSet)`, binding each to its own state and owning
the name, default, validation and help. The registry never interprets a key, so
assets that need no options (for example a mock identities asset) declare none.
`slot-manager acquire --asset-admission-option <kind>.<key>=<value>` sets them.
The flag is repeatable and comma-separable, and `ARO_HCP_ASSET_ADMISSION_OPTIONS`
supplies the default list for CI invocations; explicit flag values replace it.
Later entries override earlier ones. `--help` lists every declared option with its
default. An unknown kind, an unknown key, an asset without options, or a value the
handler rejects fails before leasing.

Options configure only the addressed kind. Shared code, such as role assignment
cleanup, takes plain parameters from the handler that uses it.

### Emergency admission opt-out

`slot-manager acquire --disable-asset-admission=e2e_identities` skips only that
asset kind's admission phase. The flag accepts comma-separated values and can be
repeated. Admission remains enabled for every kind not explicitly listed,
including newly added kinds. Unknown or unimplemented kinds fail before leasing.

`ARO_HCP_DISABLE_ASSET_ADMISSION` supplies the default list for CI invocations;
explicit flag values replace it. `--disable-asset-admission=` clears an inherited
list and restores admission for all assets. The acquire step in `openshift/release`
exposes the environment parameter with an empty default.

Each skipped, demanded asset produces a warning with its kind and slot name.
Acquisition, subscription resolution, structural state validation, runtime exports,
and release remain unchanged. Disabling admission does not make an asset optional
and does not bypass catalog validation or provisioning checks.

Use this only as a temporary mitigation, for example during ARM throttling.
For E2E identities it skips identity/principal verification and cleanup of stale
role assignments, so the identities are not guaranteed clean for reuse.
Remove the opt-out after mitigation; it applies only to that acquisition and is
not a persistent catalog or release setting.

### Failure behavior

For asset kinds without an explicit opt-out, admission is fail-closed:

- a missing required resource fails;
- incomplete inventory fails;
- an Azure list or delete error fails;
- unexpected resource shape fails;
- failure to establish the asset's readiness fails; and
- a runtime-export ownership conflict fails.

Admission is idempotent so a later acquisition can safely retry after an
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
      - service-control-plane
      - management-control-plane
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
before admission, and runtime publication validates again. These are local
resolved-state checks, not additional Azure inventory scans.

Resolved infrastructure assets must use leased allocation and contain exactly
the resource-group names recorded for that asset in the journal, without
duplicates. Ordering may differ. These checks do not restrict partial-journal
persistence or release.

## Runtime contract

After successful admission, slot-manager writes
`${SHARED_DIR}/aro-hcp-slot.env`.

Core exports include:

- `ARO_HCP_DEPLOY_ENV`;
- `CUSTOMER_SUBSCRIPTION`;
- `SELECTED_CLUSTER_PROFILE_DIR`;
- `SELECTED_LOCATION`;
- `ARO_HCP_E2E_VM_FAMILY_POLICY`;
- `ARO_HCP_E2E_SLOT_NAME`; and
- `ARO_HCP_E2E_SLOT_RESOURCE_TYPE`.

Asset handlers contribute asset-specific exports. The E2E identity handler owns
`LEASED_MSI_CONTAINERS`; it does not assign user worker overrides.
The infrastructure identity handler owns `LEASED_INFRASTRUCTURE_IDENTITY_BUNDLE`,
a JSON object with service and management resource groups and AKS identity
names. `INFRA_SUBSCRIPTION_ID` is exported only when the selected pool demands
an infrastructure asset and that subscription has been resolved.

Every export has one owner. Duplicate names fail contract construction. Export
names must be valid shell identifiers, and values are shell-escaped before the
file is atomically published.

The contract contains no credentials. Downstream steps source it and use the
acquired values without re-resolving pool behavior.

### Acquired identity count and suite defaults

In every deployment environment, the E2E identity handler publishes the resolved
acquired container names in `LEASED_MSI_CONTAINERS`. Physical inventory and reserve
containers do not contribute to this list.

Suite registration explicitly opts the integration and Stage fast, slow, and
combined parallel suites, PROD fast and slow parallel suites, RP API compatibility
fast and slow parallel suites, and Hypershift presubmit parallel suite into an
acquired worker default. The count is derived from the exported list, independently
of pooled identity mode. Resource-pool membership does not select this policy:
`dev-cd-check/parallel` consumes MI resources but retains its worker fallback.
`upgrade/in-place` retains its participant-based default so the complete cohort
can reach its barrier.
Explicit worker overrides for `upgrade/in-place` must allow the complete cohort
to start; fewer workers leave participants queued behind the barrier.

`ARO_HCP_SUITE_PARALLELISM` is an exported user or CI override; the lease contract
does not assign it. The CLI parses and validates it once during construction and
applies it when registering suites. A nonempty value must be a positive integer
and applies globally. An explicitly empty value opts out of the acquired default.
An absent acquired list retains suite fallbacks.

Suite parallelism is resolved from the user override, then the opt-in acquired
count, then the suite fallback. Invalid environment configuration fails CLI
construction. Metadata reports registered values without a command-time hook.
An explicit `--max-concurrency` limits workers without changing suite metadata.
When omitted, the flag defaults to the selected suite's registered parallelism.
The runner uses `min(max-concurrency, Suite.Parallelism)`; for example, a suite
value of 75 with `--max-concurrency=100` runs at most 75 workers, while a flag
value of 25 limits it to 25. Worker limits do not change the independent leased
MSI resource budget.

The CLI entrypoint is in `main.go`, orchestration in `cli.go`, suite definitions
in `suites.go`, and concurrency resolution in `concurrency.go`. MI scheduling and
EV2 result reporting are grouped in `mi_scheduler.go` and `ev2_reporting.go`.

### Pool VM family policy

An optional `vm_family_policy` on a catalog pool keeps quota-related selection
constraints beside the customer subscription binding. There is no separate
policy file, schema version, or embedded environment resolver. For example:

```yaml
vm_family_policy:
  worker_families: [standardDSv5Family]
  helper_families: [standardDDSv5Family]
  regions:
    centralus:
      worker_families: [standardDASv5Family]
```

The top-level role lists are pool defaults. Each regional list replaces only
that role; an omitted role inherits the pool default. Region keys are lowercase
Azure region names. Resolution matches `AcquiredSlotState.RuntimeRegion`
case-insensitively, never the identity asset's provisioning region. This also
applies to weighted and runtime-selected pools.

The unresolved pool policy is carried in the expanded slot and acquired state.
`AddCoreRuntimeExports` validates it and publishes only the resolved
`worker_families`/`helper_families` JSON object. An unconfigured pool exports `{}`
to overwrite any inherited policy. Omitted roles preserve historical selector
behavior; supplied empty/null role lists, duplicate or unknown fields, and
invalid region mappings are rejected. An omitted or null whole catalog policy
means no policy.
The shared resolved policy type and strict JSON parser live in
`test/pkg/vmfamily`; the pool-specific YAML and regional resolution code stays
with slot-manager. `test/e2e-config` contains only configuration data.

Consumers already source the contract. The suite parses
`ARO_HCP_E2E_VM_FAMILY_POLICY` without environment or region lookup and rejects a
downstream `LOCATION` differing from `SELECTED_LOCATION`. Region overrides must
therefore be supplied at acquisition, before policy resolution. Local runs can
provide the JSON variable directly without a slot contract.

Only the `int` pool `westus3-shard0` currently declares policy. The UK South pool
is unchanged. See [VM selection](../../../e2e/README.md#selecting-vm-sizes-skus)
for ordered family search, exact role-size bounds, and specialized-selector
exceptions.

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

The optional pool-level `identity_container_provisioning_count` reserves a larger
physical inventory without increasing the runtime lease budget. It defaults to
`resource_group_count` and cannot be smaller. Pool apply and inventory validation
use the physical count; resolution, admission, and `LEASED_MSI_CONTAINERS` use
`resource_group_count`. For INT West US 3, these counts are 100 physical groups
and 75 leased groups per slot.

V2 catalog validation checks the generated names against Azure resource-group
naming rules, including the 90-character limit with both numeric suffixes.
Suffix widths grow beyond two digits when an index exceeds 99.

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

### Admission

For every resolved resource group, the handler:

1. enumerates all user-assigned managed identities;
2. selects the 13 standard identities and requires valid principal IDs,
   reporting unexpected names without inspecting or cleaning those identities;
3. enumerates role assignments for their principal IDs across the E2E
   subscription, including child scopes;
4. deletes all discovered role assignments for those principals.

Federated identity credential (FIC) cleanup is not part of admission: it is
expensive in time and ARM calls and does not affect test correctness.

Discovery lists role assignments once for the E2E subscription and matches them
against the expected identities' principal IDs. Each synchronous deletion must
complete successfully or return `404` (already absent). No confirmation reads,
deletion polling, or second inventory scan are performed. Azure authorization
propagation can still lag behind successful deletion; checking resource absence
would not guarantee authorization-cache convergence.

Inventory reads run serially, finishing each container's identity inventory
before moving to the next container. Role assignment deletes run with at most
`e2e_identities.cleanup-workers` concurrent requests (see Asset admission
options; default 1, so serial). All deletes share the admission deadline. Raise
it only for low-concurrency environments: ARM throttles deletes per subscription
and service principal (token bucket of 200, refilled at 10 per second), and every
slot admitted concurrently in the same subscription adds its workers to that
budget. Azure SDK throttling retries remain enabled. A clean inventory returns
without deletion calls.

A missing required identity, invalid principal metadata, incomplete enumeration,
or deletion error (including exhausted SDK retries) fails acquisition before
runtime publication. Expected identity and enumerated role-assignment principal
IDs must be hyphenated UUIDs without surrounding whitespace; matching is
case-insensitive. Invalid role principal IDs fail rather than being silently
classified as foreign.

Extra identities are not used by the tests and are neither
admitted nor cleaned by this asset. They produce one informational log entry
per affected resource group, listing the group and unexpected identity names.
This reporting adds no ARM requests and does not block admission, including for
unmanaged pools.

The E2E framework cleans FICs and role assignments after each test. Admission
repairs RBAC residue from interrupted jobs or failed teardown.

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

## Infrastructure identity asset

Infrastructure identities use `allocation: leased` and come from a dedicated
top-level asset pool. A lease resolves one complete identity bundle containing
the explicit UAMIs required by one ephemeral service cluster and two management
stamps. The consumer contract is exactly one bundle per slot.

There is no value in pinning an empty identity bundle to one primary slot.
Before use, the identities have no per-run RBAC or federated credentials, so any
compatible primary slot may consume any free bundle from the infrastructure
identity pool.

The asset pool lives in the consumer environment's infrastructure subscription.
Its required bundle count is derived from the total slot count of every
consumer. Slot-manager generates the corresponding Boskos resources and, when
managed provisioning is enabled, creates or reconciles every identity bundle.

The Boskos resource name is the journal identity of the bundle. Azure resource
groups are derived from that name (`<bundle>-svc`, `<bundle>-mgmt-1`,
`<bundle>-mgmt-2`). Workload and AKS control-plane identity names are an
intrinsic handler topology matching the product service and management
templates; they are not cluster-name derived and are not catalog fields.
Managed pools declare `infrastructure_identities.provisioning_region` for
`apply-pool-assets`.

### Pool management

`ApplyPools` creates or updates the three Azure resource groups and the
complete identity set for every derived bundle with direct ARM
`CreateOrUpdate` calls. Infrastructure pools are a small, persistent topology
owned in Go (one service group plus management stamps with different identity
lists). They do not use Bicep deployment stacks: E2E stacks exist for a
homogeneous container loop and `ActionOnUnmanage: delete` pool reductions,
which would be unsafe for identities that admission and release must keep.
Apply is idempotent and does not prune extra groups or identities.
`ValidatePools` is read-only and reports missing groups, missing identities,
and unexpected identities. Live pool creation and catalog activation remain
separate rollout work.

### Admission

Admission inspects those Azure groups, requires the complete identity set with
valid principal IDs, and deletes every federated credential on those identities
plus every role assignment granted to their principals in the infrastructure
subscription. Extra identities are logged and excluded from cleanup.

Discovery lists identities and federated credentials once per expected
identity, then lists role assignments once for the infrastructure
subscription. Each synchronous deletion must complete successfully or return
`404` (already absent). No confirmation reads, deletion polling, or second
inventory scan are performed. Azure authorization propagation can still lag
behind successful deletion; checking resource absence would not guarantee
authorization-cache convergence.

Inventory and federated-credential reads run serially. Deletes run with at most
`infrastructure_identities.cleanup-workers` concurrent requests (see Asset
admission options; default 1, so serial). All deletes share the admission
deadline. Raise it only for low-concurrency environments: ARM throttles deletes
per subscription and service principal, and every slot admitted concurrently
in the same subscription adds its workers to that budget.

Missing required identities, incomplete enumeration, or a delete error fails
closed before publication. Release returns the Boskos lease without deleting
the persistent identities or their resource groups.

`PublishLease` writes `LEASED_INFRASTRUCTURE_IDENTITY_BUNDLE` after admission.
The JSON object contains `serviceResourceGroup`, `serviceAksIdentityName`,
`managementResourceGroups`, and `managementAksIdentityNames`. Client and
principal identifiers are resolved by deployment from those names.
AKS-created kubelet and Key Vault provider identities remain outside this asset.

## Future assets

The registry allows future assets to be added independently.

### Mock identities

A slot may own an MSI mock service principal, backend ARM helper, and
clusters-service ARM helper. Names derive from environment, pool, and slot.
Their persistent credentials and baseline RBAC require an asset-specific
preparation policy.
