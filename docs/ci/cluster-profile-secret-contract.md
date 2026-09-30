# Cluster Profile Secret Contract

OpenShift CI mounts ARO HCP Azure credentials from the `hcm-aro` secret
collection as cluster profiles. This document defines the supported field
shape for those profiles and the runtime contract produced when a job leases a
customer subscription.

## Profile Fields

Every active environment profile contains one Azure identity:

| Field | Purpose |
|-------|---------|
| `client-id` | Service-principal client ID |
| `client-secret` | Service-principal credential |
| `tenant` | Azure tenant ID |

Subscription fields are scoped by purpose. Consumers must not use an
unqualified subscription field.

### Infrastructure Subscriptions

Infrastructure fields describe subscriptions that host the ARO HCP service
footprint:

| Field pattern | Purpose |
|---------------|---------|
| `infra-global-subscription-id` | Subscription ID for shared global infrastructure |
| `infra-global-subscription-name` | Subscription name for shared global infrastructure |
| `infra-<deploy-env>-subscription-id` | Subscription ID for an environment-specific infrastructure scope |
| `infra-<deploy-env>-subscription-name` | Subscription name for an environment-specific infrastructure scope |

`<deploy-env>` is the deployment environment represented by that profile, for
example `ci00` or `ci01`.

### Customer Subscriptions

Customer fields describe subscriptions in which E2E tests create customer
resource groups and clusters:

| Field pattern | Purpose |
|---------------|---------|
| `customer-shardN-subscription-id` | Azure subscription ID for customer shard `N` |
| `customer-shardN-subscription-name` | Azure subscription name for customer shard `N` |

Each shard must contain both fields, and the ID and name must identify the same
Azure subscription. Shard indices are stable inventory identifiers; do not
renumber existing shards when adding or removing capacity.

## Consumer Rules

- Global infrastructure jobs use `infra-global-subscription-*`.
- Environment-specific infrastructure jobs use the matching
  `infra-<deploy-env>-subscription-*` pair.
- E2E and cleanup jobs that operate on customer resources use the shard
  selected by slot-manager.
- Jobs must authenticate with the `client-id`, `client-secret`, and `tenant`
  from the same cluster profile directory that owns the selected subscription.
- A consumer must not fall back from a purpose-qualified field to a
  differently scoped field.

The following transitional fields are not part of the supported contract:

- `subscription-id`
- `subscription-name`
- `infra-subscription-id`

Do not add new consumers or recreate these fields.

## Slot-Manager Runtime Contract

The slot catalog records customer subscription names because those names are
human-readable and stable in Boskos resource definitions. During acquisition,
slot-manager:

1. selects a slot pool from `test/e2e-config/e2e-slots.yaml`
2. matches the pool's subscription name against exactly one
   `customer-*-subscription-name` file across the mounted profiles
3. reads the sibling `customer-*-subscription-id` file
4. records the profile directory that owns the matched pair
5. writes `${SHARED_DIR}/aro-hcp-slot.env`

Customer-subscription consumers source that file and use:

| Variable | Meaning |
|----------|---------|
| `CUSTOMER_SUBSCRIPTION` | Selected customer subscription name |
| `CUSTOMER_SUBSCRIPTION_ID` | Selected customer subscription ID |
| `SELECTED_CLUSTER_PROFILE_DIR` | Profile containing the matching credentials and shard pair |
| `SELECTED_LOCATION` | Runtime Azure location selected for the slot |
| `LEASED_MSI_CONTAINERS` | Identity-container resource groups assigned to the slot |
| `ARO_HCP_E2E_SLOT_NAME` | Leased Boskos resource name |
| `ARO_HCP_E2E_SLOT_RESOURCE_TYPE` | Leased Boskos resource type |

This resolution is intentionally strict. Acquisition fails if the name is
missing, matches more than one field, lacks its sibling ID, or contains an
empty ID. Downstream jobs therefore do not need Azure CLI lookups or legacy
subscription-field fallbacks.

## Updating The Inventory

When adding a customer shard, follow
[E2E Subscription Onboarding](e2e-subscription-onboarding.md). Update the ID
and name together, validate the slot catalog against the release-side Boskos
inventory, and rehearse a job that can select the new shard.

Credential rotation must patch only `client-id`, `client-secret`, and `tenant`.
It must preserve infrastructure fields, customer shard pairs, and secret-sync
metadata.
