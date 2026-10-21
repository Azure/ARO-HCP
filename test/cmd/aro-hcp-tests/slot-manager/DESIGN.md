# Slot-manager

Slot-manager is a CI-only allocator of a complete E2E environment, not a raw
Boskos resource. A **slot** is one allocatable E2E environment instance. A
**pool** groups slots with compatible deployment, subscription, region, and asset
requirements. An **asset** is a resource set the environment needs in addition to
the primary slot. **Admission** verifies and, where needed, cleans an allocated
asset before reuse. A **runtime contract** is the non-secret set of resolved
values downstream job steps consume.

The catalog describes intent; acquired state records the exact allocation; the
runtime contract publishes that resolved allocation. A **dedicated** asset is
assigned deterministically to one primary slot and needs no separate lease. A
**leased** asset is independently acquired from an asset pool. Every catalog and
state document requires `version: 2`. The acquire and release steps use the same
runner image; completed job artifacts are read-only, and cross-version recovery
is unsupported.

## A job's path

```text
catalog → select environment and pool → lease primary slot
  → resolve subscriptions and runtime region
  → acquire or resolve declared assets
  → admit assets (fail closed unless explicitly opted out)
  → publish runtime contract
  → run job against the lease
  → release primary and independent asset leases
```

Presence of an asset declaration means the asset is required. Slot-manager
acquires or resolves, admits, and publishes every declared asset before the job
runs. The runtime contract is withheld until all non-disabled assets pass
admission. Release returns recorded leases; it does not generally destroy
persistent backing resources.

## Catalog

The catalog groups named pools into logical environments. Each environment has
one deployment environment; each pool declares its E2E subscription, region
behavior, slot count, and required assets. Infrastructure subscription binding
is required only when a pool in that environment declares an infrastructure
asset.

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
      regions: [westus3, centralus, canadacentral]
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

A pool's `name` is stable within its environment; subscription and region are
properties, not identifiers. Default Boskos and asset names derive from
environment, pool, and slot index. Explicit naming overrides may preserve
existing inventory. `deployment_environment.name` must match
`[A-Za-z0-9][A-Za-z0-9_-]*`. Workflow configuration must not override the
acquired deployment environment; a different deployment environment is a
separate logical environment with its own footprint and asset demand.

Every pool declares `subscriptions.e2e`. The optional
`deployment_environment.infrastructure_subscription` locates infrastructure
assets. Slot-manager resolves required subscription names to exactly one
cluster profile and Azure subscription ID, verifies infrastructure bindings
against the profile when demanded, and persists resolved names and IDs. Missing
or mismatched required bindings fail acquisition. Without infrastructure
demand, no infrastructure profile file is read and no infrastructure
subscription is needed in state or exports.

Region selection is pool behavior:

- `fixed`: one authoritative region;
- `runtime-selected`: caller supplies a permitted region, with the pool region
  as fallback;
- `weighted`: an ordered allowlist and caller-supplied weights select a region
  deterministically from `BUILD_ID` and the weights.

An explicit permitted location override takes precedence. Downstream steps use
the acquired region; they must not select it again.

Every pool requires `slot_assets.e2e_identities`; missing or null is invalid.
`slot_assets` entries for other kinds are opt-in, and their presence makes them
required. Each asset uses `allocation: dedicated` or `allocation: leased`.
Provisioning is `managed` (default) or `unmanaged`; this changes who provisions
backing resources, not the admission requirement. Asset-intrinsic details such
as the standard identity set belong to the asset, not the catalog.

Top-level `asset_pools` define independently leased inventory: a globally unique
name, kind, provisioning ownership, Boskos resource type, stable naming, and
kind-specific configuration. Capacity is derived from all consumers, not
configured independently:

```text
required capacity = sum(consumer pool slot_count * units_per_slot)
```

`units_per_slot` defaults to one. Consumers of an asset pool must resolve to
compatible subscriptions and asset configuration. Unknown, unused,
under-capacity, multiply defined, or incompatible pools are invalid. Derived
capacity drives Boskos inventory and backing-resource management.

## Asset lifecycle

Every asset kind follows the same behavior: resolve a dedicated asset or acquire
its independent lease; persist lease ownership immediately; admit the asset;
publish only its owned runtime values; and release its recorded lease. Handlers
own the asset-specific readiness checks and cleanup. Release returns every
recorded independent lease, without deleting persistent backing resources
unless that asset explicitly defines otherwise.

Pool apply reconciles managed backing resources; pool validation checks
backing inventory without mutation. Unmanaged pools are externally provisioned
and validated when selected. Generic commands validate catalog demand and
inventory before acting, including when an asset-kind filter is used.

Admission is handler-owned and fail-closed. The emergency option
`slot-manager acquire --disable-asset-admission=<kind>` skips admission only for
the named kind; acquisition, resolution, structural validation, publication,
and release remain required. The repeatable, comma-separated option accepts
`ARO_HCP_DISABLE_ASSET_ADMISSION` as its default; explicit CLI values replace
that default, and `--disable-asset-admission=` clears it. Unknown or
unimplemented kinds fail before leasing. Each skipped demanded asset produces a
warning. Use only as a temporary mitigation: skipped assets are not guaranteed
ready for reuse.

## Acquire, persist, fail, release

Acquisition validates the catalog, selects an environment and candidate pool,
leases the primary slot, and immediately journals it. It then resolves the
profile and subscriptions, acquires or resolves declared assets in deterministic
registry order, persists each independent lease and resolved asset before
continuing, admits assets, builds exports, and atomically publishes the runtime
contract. Each independent Boskos acquisition has its own
`--lease-proxy-timeout`; the primary slot's optional
`--max-wait-for-lease` is not reused for secondary acquisitions. All asset
admission handlers share a configurable budget, default 10 minutes.

If acquisition fails after partial allocation, slot-manager attempts to release
everything already journaled. If cleanup or the process fails, Test Platform
job-lifecycle reconciliation provides eventual lease cleanup. Admission fails
closed on missing resources, incomplete inventory, Azure list/delete errors,
unexpected resource shape, inability to establish readiness, or export-owner
collision. Admission assumes exclusive allocation ownership; it cannot protect
against concurrent external writers. Admission is idempotent so interrupted
attempts can be retried safely.

State at `${SHARED_DIR}/aro-hcp-slot-state.yaml` is a write-ahead release
journal. It records the exact primary and independent lease names as well as the
resolved allocation. Successful returns are persisted individually and skipped
on retry. Before each return, a `returning` marker is persisted; success changes
it to `returned`. An interrupted request or failed post-return write leaves an
uncertain outcome. Do not blindly return that name again: it may belong to
another job. Slot-manager reports uncertainty and leaves ownership
reconciliation to Test Platform. If return intent cannot be persisted, cleanup
fails closed. Other recorded leases are still attempted with independent,
uncancelled bounded contexts.

Release is best-effort across the complete journal: one failed return does not
prevent attempts for the others, and errors are reported after all attempts.
Partial journal state can be released without the catalog, resolved
subscriptions, or installed asset handlers. An existing state file must be
released before another acquisition in the same shared directory. After
successful release, local state and runtime-contract files are removed.

## Runtime contract

After successful admission, slot-manager atomically writes
`${SHARED_DIR}/aro-hcp-slot.env`. Core exports include:

- `ARO_HCP_DEPLOY_ENV`
- `CUSTOMER_SUBSCRIPTION`
- `SELECTED_CLUSTER_PROFILE_DIR`
- `SELECTED_LOCATION`
- `ARO_HCP_E2E_VM_FAMILY_POLICY`
- `ARO_HCP_E2E_SLOT_NAME`
- `ARO_HCP_E2E_SLOT_RESOURCE_TYPE`

Asset handlers own their exports: E2E identities own `LEASED_MSI_CONTAINERS`;
infrastructure identities own `LEASED_INFRASTRUCTURE_IDENTITY_BUNDLE`. The
latter is JSON containing service and management resource groups and identity
names. `INFRA_SUBSCRIPTION_ID` is exported only when infrastructure demand
requires and resolves it. Each export has exactly one owner; collisions fail.
Values are shell-escaped. The contract contains no credentials. Downstream steps
source it and must not re-resolve acquired behavior.

## E2E identity asset

E2E identity assets are `dedicated`: each slot receives a deterministic set of
persistent managed-identity resource groups. The groups contain the standard
per-HCP identity set defined by
[`managed-identities.bicep`](../../../e2e-setup/bicep/modules/managed-identities.bicep).
For slot index `N`, names derive from:

```text
<resource_group_prefix>-<N, two digits>-<M, two digits>
```

where `M` ranges from zero to `resource_group_count - 1`. An optional
`identity_container_provisioning_count` may reserve larger physical inventory;
it defaults to `resource_group_count` and cannot be smaller. Physical count
controls pool provisioning and validation; `resource_group_count` controls the
lease and `LEASED_MSI_CONTAINERS` export.

Admission enumerates each group's identities, requires the standard identities
and valid principal IDs, then discovers and deletes their role assignments in
the E2E subscription, including child scopes. Unexpected identities are
reported but not cleaned. Each synchronous deletion must succeed or return
`404`; incomplete enumeration or deletion failure blocks publication. FIC
cleanup is not part of E2E identity admission. Release does not delete identity
resource groups or identities.

`apply-pool-assets` creates or updates managed identity groups;
`validate-pool-assets` performs read-only shape validation. E2E identity pool
management skips unmanaged pools by default; explicitly selecting a pool or
subscription includes them and may modify unmanaged backing resources.

## Infrastructure identity asset

Infrastructure identities are a leased bundle, one bundle per slot, from a
top-level asset pool in the consumer environment's infrastructure subscription.
Capacity is derived from all consumer slots. A bundle contains the UAMIs needed
by one ephemeral service cluster and two management stamps. Its resource groups
derive from the Boskos bundle name as `<bundle>-svc`, `<bundle>-mgmt-1`, and
`<bundle>-mgmt-2`; identity topology is intrinsic to the asset.

Admission requires the complete identity set and valid principal IDs, then
removes federated credentials and role assignments for those principals in the
infrastructure subscription. Missing identities, incomplete enumeration, or
delete errors fail closed. Release returns the Boskos lease but preserves the
persistent identities and resource groups. Publication writes
`LEASED_INFRASTRUCTURE_IDENTITY_BUNDLE` with `serviceResourceGroup`,
`serviceAksIdentityName`, `managementResourceGroups`, and
`managementAksIdentityNames`. Deployment resolves client and principal IDs from
those names. AKS-created kubelet and Key Vault provider identities are outside
this asset.

## VM / worker SKU policy

An optional pool-level `vm_family_policy` constrains SKU selection for that
pool's customer subscription. `worker_families` and `helper_families` are
ordered Azure Resource SKU family identifiers. Top-level lists are defaults;
regional lists replace only the specified role, and omitted roles inherit the
pool default. Region keys are lowercase Azure region names.

Slot-manager resolves policy against the acquired **runtime region**, including
weighted and runtime-selected pools, and exports the resolved JSON object as
`ARO_HCP_E2E_VM_FAMILY_POLICY`. Consumers must not re-resolve it. A pool without
policy exports `{}` to clear inherited policy. Unset/null whole policy means no
policy; an empty object `{}` means historical selector behavior. Invalid
configuration, including explicit empty/null role lists or unknown fields,
fails rather than silently falling back. The region must be chosen at acquire;
consumers reject a subsequent location that differs from `SELECTED_LOCATION`.

See [E2E VM size selection](../../../e2e/README.md#selecting-vm-sizes-skus) for
how tests select a SKU under this policy.

## Suite parallelism from leased identities

`LEASED_MSI_CONTAINERS` is the runtime identity ceiling. Opted-in suites may
derive their default worker count from its length. `ARO_HCP_SUITE_PARALLELISM`
is a separate user or CI override; the lease does not set it. Suite parallelism
precedence is user override, opted-in acquired count, then suite fallback. An
explicitly empty override opts out of the acquired default. Explicit
`--max-concurrency` limits execution workers independently; the runner uses the
minimum of that value and suite parallelism. This worker limit does not change
the leased identity budget. See
[`docs/ci/identity-leasing.md`](../../../../docs/ci/identity-leasing.md) for
capacity and quota formulas.

## Inventory commands

- `sync-boskos-config` and `validate-boskos-config` reconcile or validate
  derived Boskos inventory for primary slots and independent asset pools.
- `apply-pool-assets` reconciles managed backing resources.
- `validate-pool-assets` validates declared backing resources, including
  unmanaged resources when explicitly selected.

All derive capacity from complete catalog demand. See
[`docs/ci/identity-leasing.md`](../../../../docs/ci/identity-leasing.md) and
[`docs/ci/e2e-subscription-onboarding.md`](../../../../docs/ci/e2e-subscription-onboarding.md)
for operator procedures.

## Future assets

The asset registry is an extension point. Mock identities (MSI mock service
principal, backend ARM helper, and clusters-service ARM helper) remain intent
only; their persistent credentials and baseline RBAC need an asset-specific
preparation policy.

## Feature progress

- E2E identity assets are live.
- The infrastructure identity asset exists; catalog demand and pool rollout
  are separate from this design.
- Mock identity assets have not started.
