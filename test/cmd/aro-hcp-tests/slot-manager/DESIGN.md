# Slot Manager

Slot manager assigns an E2E job a customer-subscription slot and an Azure
runtime region. It uses Boskos for exclusive slot ownership and
`test/e2e-config/e2e-slots.yaml` as the canonical inventory.

Its responsibilities are:

- select an eligible customer-subscription pool,
- acquire one slot from that pool,
- select the runtime region according to the pool's region mode,
- resolve the cluster profile that owns the selected subscription,
- export the non-secret runtime contract for downstream CI steps, and
- release the exact Boskos resource acquired by the job.

Slot manager does not enforce regional concurrency, evaluate region health, or
change routing weights automatically.

## Catalog contract

The catalog groups pools into logical environments and maps deploy environment
names such as `ci01`, `int`, `stg`, and `prod` to one of them.

```yaml
version: 1
environments:
  dev:
    deploy_envs:
    - ci00
    - ci01
    pools:
    - subscription_name: "ARO HCP E2E Hosted Clusters (EA Subscription)"
      region_mode: weighted
      regions:
      - westus3
      - centralus
      - canadacentral
      identity_provisioning_region: westus3
      resource_type: aro-hcp-dev-shard0-slot
      slot_count: 5
      identity_container_prefix: aro-hcp-msi-container-dev-shard0
      identity_container_count: 60
```

Each pool defines:

| Field | Meaning |
| --- | --- |
| `subscription_name` | Name used to resolve the customer subscription from cluster profiles. |
| `region_mode` | `fixed`, `runtime-selected`, or `weighted`. Defaults to `fixed` when omitted. |
| `region` | Required for `fixed` and `runtime-selected`; the fixed or fallback runtime region. |
| `regions` | Ordered runtime-region allowlist for `weighted`. |
| `resource_type` | Boskos resource type containing the pool's slots. |
| `slot_count` | Number of independently leasable jobs in the pool. |
| `identity_container_prefix` | Prefix used to derive identity-container resource-group names. |
| `identity_container_count` | Number of identity containers assigned to each slot. |
| `identity_provisioning_region` | Location for identity-pool infrastructure, independent of the runtime region. It defaults to `region`; weighted pools must set it explicitly. |
| `identity_provisioning: unmanaged` | Marks identity infrastructure as externally managed. It does not remove the pool from runtime selection. |

All pools in one environment must use the same `region_mode`. Weighted pools
must also declare the same non-empty, duplicate-free, ordered `regions` list.
Resource types are unique across the complete catalog.

For slot index `N`, slot manager expands a pool into:

```text
resource name:             <resource_type>-<N, two digits>
identity container prefix: <identity_container_prefix>-<N, two digits>
identity containers:       <slot prefix>-<M, two digits>
```

## Acquire inputs

The CI acquire step provides these selectors and runtime inputs:

| Environment variable | Purpose |
| --- | --- |
| `ARO_HCP_DEPLOY_ENV` | Resolves the catalog environment. |
| `ALLOWED_SUBSCRIPTIONS` | Optional comma/newline-separated allowlist of catalog `subscription_name` values. |
| `ALLOWED_LOCATIONS` | Optional location allowlist used only by `fixed` mode. |
| `MULTISTAGE_PARAM_OVERRIDE_LOCATION` | Highest-precedence concrete runtime location. |
| `LOCATION_WEIGHTS` | Weighted-mode entries in `location=weight` form, required only when no explicit location override is set. |
| `BUILD_ID` | Stable per-Prow-run key for deterministic weighted selection. |
| `CLUSTER_PROFILE_DIRS` | Optional comma/newline-separated cluster profile directories. |
| `CLUSTER_PROFILE_DIR` | Backward-compatible single profile directory used when `CLUSTER_PROFILE_DIRS` is unset. |
| `LEASE_PROXY_SERVER_URL` | Ci-operator Boskos proxy endpoint. |
| `SHARED_DIR` | Directory for the state and exported environment files. |

Equivalent command-line flags exist for the acquire options. Explicit location
override takes precedence over other location selectors.

## Region modes

### `fixed`

The pool represents a subscription+region coordinate:

- `region` is the runtime region.
- `ALLOWED_SUBSCRIPTIONS` filters by subscription.
- `MULTISTAGE_PARAM_OVERRIDE_LOCATION`, when set, filters pools to that region.
- Otherwise `ALLOWED_LOCATIONS` can restrict eligible regions.

The leased pool determines `SELECTED_LOCATION`.

### `runtime-selected`

The pool represents subscription capacity; the caller chooses the runtime
region:

- `ALLOWED_SUBSCRIPTIONS` filters candidate pools.
- `ALLOWED_LOCATIONS` does not affect pool selection.
- `MULTISTAGE_PARAM_OVERRIDE_LOCATION` becomes `SELECTED_LOCATION`.
- If no override is supplied, the pool's `region` is the fallback.

This mode is intended for jobs, such as regional gating, whose external caller
already knows the target region.

### `weighted`

The pool represents subscription capacity and the job supplies the desired
regional distribution:

- `ALLOWED_SUBSCRIPTIONS` filters candidate pools.
- The catalog `regions` list is the authoritative location allowlist.
- Without an explicit override, `LOCATION_WEIGHTS` controls the approximate
  distribution for new runs.
- `ALLOWED_LOCATIONS` is ignored.
- `MULTISTAGE_PARAM_OVERRIDE_LOCATION` bypasses weighted selection but must
  name a catalog-allowed region.

Example equal distribution:

```text
LOCATION_WEIGHTS=westus3=1,centralus=1,canadacentral=1
```

Example drain:

```text
LOCATION_WEIGHTS=westus3=0,centralus=1,canadacentral=1
```

When an explicit override is set, `LOCATION_WEIGHTS` and `BUILD_ID` are not
required and any supplied weights are ignored. This allows callers already
pinned to a valid catalog region to continue working when an environment moves
from `runtime-selected` to `weighted`.

Without an explicit override, weights are required and must be non-negative
integers. Every catalog region must appear exactly once, unknown regions are
rejected, and at least one weight must be non-zero. Duplicate, missing,
negative, non-integer, and overflowing values are errors. `BUILD_ID` must also
be non-empty. Slot manager uses 64-bit FNV-1a over its UTF-8 bytes:

```text
bucket = fnv1a64(BUILD_ID) % sum(weights)
```

The selected region is the first region in catalog order whose cumulative
positive weight contains `bucket`. Map iteration order must not affect the
result.

This produces a reproducible choice within one Prow run and approximate
weighted distribution over time. It does not provide exact distribution or a
hard per-region concurrency limit. A retry is a new Prow run and may select a
different subscription or region. Setting a weight to zero affects new runs
only.

## Pool selection and lease acquisition

Acquire follows this sequence:

1. Load and validate the catalog.
2. Resolve `ARO_HCP_DEPLOY_ENV` to one slot environment.
3. Validate mode-specific selectors before acquiring a lease.
4. Build the candidate pool list:
   - all modes apply `ALLOWED_SUBSCRIPTIONS`;
   - only `fixed` applies location filtering to pool identity.
5. Rotate the initial candidate once per acquire invocation, preserving catalog
   order for the remaining candidates.
6. Probe each candidate's Boskos resource type until one returns a lease.
7. Resolve the leased resource name back to an expanded catalog slot.
8. Resolve the runtime region according to the environment's region mode.
9. Find exactly one cluster profile whose
   `customer-*-subscription-name` matches the pool's `subscription_name`.
10. Persist the acquired state, then write the downstream environment file.

Candidate rotation spreads subscription demand without changing the stable
fallback order. Region selection is independent from subscription selection in
`runtime-selected` and `weighted` modes.

### Waiting and errors

One candidate probe is bounded by `lease-proxy-timeout`, which defaults to
`30s`. Proxy timeouts and retryable proxy/server responses mean that the pool
did not yield an immediate lease; slot manager continues with the next
candidate.

After every candidate is temporarily unavailable, slot manager waits
`lease_wait_interval` (default `1m`) and retries the full candidate list.
`max_wait_for_lease` defaults to `30m`; zero means wait indefinitely.

Non-retryable proxy errors, invalid configuration, unknown leased resources,
and ambiguous or missing cluster-profile matches fail immediately. If
finalization fails before durable state is written, slot manager attempts to
return the lease.

## Runtime output contract

Acquire writes `${SHARED_DIR}/aro-hcp-slot.env` with:

| Variable | Meaning |
| --- | --- |
| `CUSTOMER_SUBSCRIPTION` | Selected catalog subscription name, verified against the cluster profile. |
| `SELECTED_LOCATION` | Authoritative runtime region. |
| `SELECTED_CLUSTER_PROFILE_DIR` | Profile containing the selected subscription's tenant and service-principal credentials. |
| `LEASED_MSI_CONTAINERS` | Space-separated identity-container resource groups assigned to the slot. |
| `ARO_HCP_E2E_SLOT_NAME` | Leased Boskos resource name. |
| `ARO_HCP_E2E_SLOT_RESOURCE_TYPE` | Boskos resource type used for acquisition. |

Downstream steps may map `SELECTED_LOCATION` to `LOCATION`, but must not repeat
region selection or substitute a job default.

The selection log must include the candidate pool, catalog region order,
normalized weights when applicable, whether an override was used, the
selection-key source, and the selected runtime region. These values are
non-secret.

## State and release

Acquire writes `${SHARED_DIR}/aro-hcp-slot-state.yaml` before writing the env
file. The state records:

- deploy environment,
- runtime region,
- expanded slot,
- exact Boskos resource name.

Writing state first ensures the post-step can return the lease if the process
stops before the env file is complete.

`slot-manager release` loads the state file, returns the exact leased resource
through the Boskos proxy, and removes both state files. A missing state file is
treated as nothing to release. A failed lease return is an error; failure to
remove local files after a successful return is logged.

## Inventory maintenance

The ARO-HCP catalog is the source of truth for slot shape. Slot-manager commands
derive related infrastructure from it:

- `sync-boskos-config` and `validate-boskos-config` reconcile the managed block
  in `openshift/release`.
- `apply-identity-pool` and `validate-identity-pool` reconcile or inspect the
  slot-expanded managed identity containers.

Operational onboarding, capacity calculations, and recovery procedures live in
[`docs/ci/identity-leasing.md`](../../../../docs/ci/identity-leasing.md) and
[`docs/ci/e2e-subscription-onboarding.md`](../../../../docs/ci/e2e-subscription-onboarding.md).
