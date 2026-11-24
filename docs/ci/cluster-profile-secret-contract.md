# Cluster Profile Secret Contract

CI steps mount their Azure credentials from the `hcm-aro` collection in the CI
secret store. Each mounted group becomes a cluster profile directory,
`/var/run/aro-hcp-<group>`, and a step selects one with `VAULT_SECRET_PROFILE`.
This page defines the supported shape of those profiles: which fields exist,
which field a consumer is allowed to read, and how the fields relate to the
runtime contract a job gets when it leases a slot.

The mechanism that produces the runtime contract, including its validation and
ownership rules, is specified in the
[slot-manager design](../../test/cmd/aro-hcp-tests/slot-manager/DESIGN.md#runtime-contract).
This page is the consumer-facing side of that contract: which profile fields
back it, and which field reads are no longer allowed.

## Identity Fields

Every active profile describes exactly one Azure identity:

| Field | Purpose |
| :---- | :------ |
| `client-id` | Service-principal client ID |
| `client-secret` | Service-principal credential |
| `tenant` | Azure tenant ID |

A job must read the identity from the same profile directory that owns the
subscription it operates on. Two profiles can hold credentials for different
tenants, and mixing an identity with another profile's subscription is the
failure mode this rule exists to prevent. Slot-manager expresses that pairing as
`SELECTED_CLUSTER_PROFILE_DIR`.

## Subscription Fields

Subscription fields are scoped by purpose. The scope is part of the field name,
and a consumer reads the scope it needs rather than an unqualified key.

| Field pattern | Scope | Used by |
| :------------ | :---- | :------ |
| `infra-global-subscription-id`, `infra-global-subscription-name` | Shared global infrastructure (registry, Kusto, Key Vault, DNS) | Global pipeline, observability and snapshot steps that act outside a leased customer subscription |
| `infra-<deploy-env>-subscription-id`, `infra-<deploy-env>-subscription-name` | One deployment environment's infrastructure footprint | Steps that operate on an environment's own infrastructure scope; slot acquisition also checks the environment binding against this field |
| `customer-<shard>-subscription-id`, `customer-<shard>-subscription-name` | One customer shard, where E2E tests create their resource groups and clusters | Slot acquisition, the cleanup sweepers and the expired-resource-group periodics |

`<deploy-env>` and `<shard>` are stable inventory identifiers. Never renumber a
shard or repoint an environment to a different subscription while jobs depend on
the mapping; capacity changes add entries instead. The identification of which
deploy environment binds which infrastructure subscription belongs to the slot
catalog, not to this page.

Within a shard, the `-id` and `-name` entries must identify the same Azure
subscription. Slot acquisition matches the catalog's subscription name against
the shard names and then takes the ID from the matching shard, so a mismatched
pair either fails acquisition or, worse, cleans the wrong subscription.

## Consumer Rules

- A job that operates on a leased customer subscription reads
  `CUSTOMER_SUBSCRIPTION` and `CUSTOMER_SUBSCRIPTION_ID` from the slot
  environment file and must not re-resolve either from the profile.
- A job that operates outside a lease reads the purpose-qualified
  `infra-*` or `customer-*` field it needs, and fails when that field is
  missing rather than falling back to a differently scoped one.
- A job never runs `az account list` or `az account show` to turn a name into an
  ID. The ID is published by the producer that already resolved it.
- Wildcard discovery, where a step needs every subscription in a profile, uses
  the `infra-*-subscription-id` and `customer-*-subscription-id` patterns. CI
  cleanup does this deliberately; other steps should name their scope.

The following fields are transitional and are not part of the supported
contract. Do not add consumers for them, and do not recreate them:

- `subscription-id`
- `subscription-name`
- `infra-subscription-id`
- `kusto-subscription-name`

Their remaining readers are the `openshift/release` steps that have not been
migrated yet; the migration is tracked under ARO-27235. Once those steps read
purpose-qualified fields, the transitional fields are deleted from the profiles.

## Rotation

Credential rotation patches only `client-id`, `client-secret` and `tenant`. It
must preserve every subscription field, the shard pairs and the environment
bindings in the same profile, because jobs select them by name and a rotation
that rewrites them silently repoints CI. The rotation procedure itself lives in
the [Test Tenant access SOP](../sops/test-test-tenant-access.md).

## Adding A Subscription

Follow [E2E Subscription Onboarding](e2e-subscription-onboarding.md) for an
ARO-HCP-managed subscription, and
[External Subscription Management](external-subscription-management.md) for a
subscription owned by another team. Both procedures add the shard pair to the
profile; neither involves adding a transitional field.
