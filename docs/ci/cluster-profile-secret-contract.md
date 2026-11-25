# Cluster Profile Secret Contract

CI steps mount their Azure credentials from the `hcm-aro` collection in the CI
secret store. A step that needs credentials declares the groups it mounts, one
directory per group, and reads whichever mounted directory `VAULT_SECRET_PROFILE`
selects. The mount is `/var/run/<group>` and the group name already carries the
`aro-hcp-` prefix, so `aro-hcp-prod` is mounted at `/var/run/aro-hcp-prod` while
the selector value is `prod`. The current step scripts derive the directory as
`/var/run/aro-hcp-${VAULT_SECRET_PROFILE}`, which only works because every group
is named after its selector with that one prefix; the mount itself is declared
per step in `openshift/release`, so a step has to mount the group whose directory
it reads.

This page defines the supported shape of those profiles: which fields exist,
which field a consumer is allowed to read, and how the fields relate to the
runtime contract a job gets when it leases a slot.

The mechanism that produces the runtime contract, including its validation and
ownership rules, is specified in the
[slot-manager design](../../test/cmd/aro-hcp-tests/slot-manager/DESIGN.md#runtime-contract).
This page is the consumer-facing side of that contract: which profile fields
back it, and which field reads are no longer allowed.

## Groups And Profiles

The collection holds one group per CI identity. The environment profiles
(`aro-hcp-dev`, `aro-hcp-int-rh`, `aro-hcp-stg-rh`, `aro-hcp-prod-rh`) carry the
Red Hat tenant bots that run the E2E and cleanup jobs for their environment, and
[E2E Subscription Onboarding](e2e-subscription-onboarding.md) covers the
subscription fields added to each one. The two groups below are the exception
worth naming, because they hold the same application in the Microsoft Test Test
tenant:

| Group | Fields | Used by |
| :---- | :----- | :------ |
| `aro-hcp-prod` | `client-id`, `client-secret`, `tenant`, `customer-shard0-subscription-id`, `customer-shard0-subscription-name`, plus the transitional `subscription-id` and `subscription-name` | Jobs with `VAULT_SECRET_PROFILE=prod`: the PROD E2E and CAPZ jobs and the PROD cleanup periodics |
| `aro-hcp-msft-test-tenant` | `client-id`, `client-secret`, `tenant` | The expired app-registration cleanup in the Test Test tenant. It holds the same application as `aro-hcp-prod`; once that cleanup reads `aro-hcp-prod` the group is deleted |

The secret store cannot be read back: `sm describe` shows a secret's metadata
only, never its value. See
[adding a secret to CI GSM](https://docs.ci.openshift.org/how-tos/adding-a-new-secret-to-ci-gsm/).

To confirm which profile a running job used, look for the acquisition line in
its build log, for example `Acquired 1 lease(s) for aro-hcp-test-tenant-quota-slice`
for a Test Test tenant job.

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
| `customer-<shard>-subscription-id`, `customer-<shard>-subscription-name` | One customer shard, where E2E tests create their resource groups and clusters | Slot acquisition reads the `-name` half to choose the profile; the cleanup sweepers and expired-resource-group periodics read the `-id` half |

`<deploy-env>` and `<shard>` are stable inventory identifiers. Never renumber a
shard or repoint an environment to a different subscription while jobs depend on
the mapping; capacity changes add entries instead. The identification of which
deploy environment binds which infrastructure subscription belongs to the slot
catalog, not to this page.

Within a shard, the `-id` and `-name` entries must identify the same Azure
subscription, because different consumers read different halves of the pair.
Slot acquisition matches the catalog's subscription name against the
`customer-<shard>-subscription-name` files to choose the profile directory, then
resolves the ID through ARM by display name; it does not read the sibling
`-id` file, so a mismatch is not caught at acquisition. The `-id` entry is what
the cleanup sweepers and the expired-resource-group periodics target. A
mismatched pair therefore passes acquisition and then cleans a different
subscription than the job tested against, which is why the pair is added,
changed and audited together.

Slot-manager does read one binding from the profile: when a pool declares an
infrastructure subscription, the resolved ID must match
`infra-<deploy-env>-subscription-id` for the environment the slot runs in, and
acquisition fails otherwise.

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

Rotation replaces the **value** of `client-secret` and nothing else. It must not
write `client-id` or `tenant`: those identify the application and its directory,
they do not change when the secret does, and rewriting them repoints CI at a
different identity. For the same reason every subscription field, shard pair and
environment binding in the profile stays as it is.

The procedure depends on the tenant that owns the identity, and the two are not
interchangeable:

| Profiles | Tenant | Procedure |
| :------- | :----- | :-------- |
| `dev`, `int-rh`, `stg-rh`, `prod-rh` | Red Hat tenant | [Rotate CI bot credentials](../sops/rotate-ci-bot-credentials.md). The applications and principals are managed by the DEV CI privileged entrypoint and their secrets by `tooling/entra-app-credentials`. |
| `red-hat-tenant` | Red Hat tenant | The same procedure, but this profile holds a different application: it is not one of the four bots, so confirm its application and where its value is stored first, as that page describes. |
| `prod`, `msft-test-tenant` | Microsoft Test Test tenant | [Rotating the CI Client Secret](../sops/test-test-tenant-access.md#rotating-the-ci-client-secret). The application is created and rotated by hand with `az`, and belongs to a tenant this repository has no pipeline for. |

In both cases the new value reaches CI through the secret store, not through the
step definitions: a rotated secret is published into the group's `client-secret`
entry, and jobs pick it up on their next start. A job that is already running
keeps using the secret it loaded, which is why the old credential is deleted only
after a run started post-publication has passed and the older runs have drained.

## Adding A Subscription

Follow [E2E Subscription Onboarding](e2e-subscription-onboarding.md) for an
ARO-HCP-managed subscription, and
[External Subscription Management](external-subscription-management.md) for a
subscription owned by another team. Both procedures add the shard pair to the
profile; neither involves adding a transitional field.
