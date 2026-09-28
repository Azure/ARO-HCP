# Test Tenant OpenShift Release Bot

This document covers the `OpenShift Release Bot MSFT Test` identity in the
Test Test Azure Red Hat OpenShift tenant. It does not cover the release bot
identities in the Red Hat tenant.

Production E2E currently uses three customer subscriptions:

- one Test Tenant subscription, whose identity is stored in `aro-hcp-prod`
- two Red Hat tenant subscriptions, whose identity is stored in
  `aro-hcp-prod-rh`

The scripts described here manage only the Test Tenant identity. They must not
be used to rotate or repair `aro-hcp-prod-rh`.

## Current Model

- Azure application: `OpenShift Release Bot MSFT Test`
- tenant: Test Test Azure Red Hat OpenShift
- GSM collection: `hcm-aro`
- GSM groups containing this identity:
  - `aro-hcp-prod`, used by E2E jobs selecting the Test Tenant customer shard
  - `aro-hcp-msft-test-tenant`, used by Test Tenant app-registration cleanup
- credential-management scripts:
  - `dev-infrastructure/openshift-ci/create-test-tenant-openshift-release-bot.sh`
  - `dev-infrastructure/openshift-ci/rotate-test-tenant-openshift-release-bot-credentials.sh`

The `aro-hcp-prod` profile also contains purpose-qualified customer
subscription fields. The scripts update only `client-id`, `client-secret`, and
`tenant`; individual GSM field updates preserve the subscription inventory.
See [Cluster Profile Secret Contract](cluster-profile-secret-contract.md) for
the authoritative shape.

Tenant switching and legacy backup profiles are no longer supported. Do not
recreate deleted `*-legacy` or `*-test-tenant` groups, and do not add
unqualified `subscription-id`, `subscription-name`, or
`infra-subscription-id` fields.

## Prerequisites

- Azure CLI authenticated to the Test Tenant:

  ```bash
  az login --tenant 93b21e64-4824-439a-b893-46c9b2a51082
  ```

- permissions to manage the application credential
- membership in the `aro-hcp-prow-ci` Rover group
- the OpenShift CI
  [Secret Manager CLI](https://docs.ci.openshift.org/architecture/cli-secret-manager/)
  authenticated with `login`
- `jq`

Use privileged Azure roles only for the duration required by the operation and
follow the team's access and audit procedures.

Set the script to use the Secret Manager wrapper from an
`openshift/release` checkout:

```bash
export SECRET_MANAGER_CLI="${HOME}/projects/release/hack/secret-manager.sh"
"${SECRET_MANAGER_CLI}" login
```

## Initial Application Setup

From `dev-infrastructure/openshift-ci/`:

```bash
./create-test-tenant-openshift-release-bot.sh
```

The script creates or updates the Test Tenant application, grants its
configured permissions and subscription roles, and invokes the rotation script
to update both Test Tenant GSM groups.

## Credential Rotation

Entra client secrets expire. The scripts previously omitted Azure CLI's
`--years` option, whose default is one year; no ARO-HCP policy requiring annual
rotation was found. The Test Tenant credential created on September 14, 2026
has a two-year lifetime, so the rotation script now sets two years explicitly.
Override `CREDENTIAL_LIFETIME_YEARS` only when a different lifetime is
required.

Rotate the Test Tenant credential:

```bash
./rotate-test-tenant-openshift-release-bot-credentials.sh
```

The script:

- appends a new credential rather than invalidating the active one
- validates that the new credential can authenticate
- updates the three identity fields in both GSM groups
- retains previous application credentials for rollback during propagation

Google Secret Manager values cannot be read back. Previous secret versions are
retained by GSM, but the Secret Manager CLI cannot roll back to them.

## Verification

The Secret Manager CLI can verify metadata, not values:

```bash
"${SECRET_MANAGER_CLI}" describe -c hcm-aro aro-hcp-prod/client-secret
"${SECRET_MANAGER_CLI}" describe -c hcm-aro aro-hcp-msft-test-tenant/client-secret
```

After propagation:

1. rehearse a PROD job restricted to the Test Tenant subscription
2. verify authentication uses the `aro-hcp-prod` profile
3. verify the
   `delete-expired-msft-test-tenant-app-registrations` periodic succeeds with
   `aro-hcp-msft-test-tenant`
4. only then delete superseded application credentials by key ID

## Recovery

The rotation script never deletes old application credentials. If GSM was
updated incorrectly:

1. create another credential for the same Test Tenant application
2. rerun the rotation workflow to update both GSM groups
3. wait for secret propagation
4. rerun both validation jobs

Do not copy an entire profile from another tenant. That can replace the
customer shard inventory with subscription data from a different tenant or
purpose.

Document rotations and recovery actions in the tracking ticket, including the
date, operator, reason, and validation result.

## History

- [AROSLSRE-344](https://redhat.atlassian.net/browse/AROSLSRE-344)
  recorded the original one-year credential expiry problem.
- [AROSLSRE-1719](https://redhat.atlassian.net/browse/AROSLSRE-1719)
  tracks the `hcm-aro` migration from Vault to GSM.
- [AROSLSRE-2043](https://redhat.atlassian.net/browse/AROSLSRE-2043)
  records the September 2026 rotation, confirms that these are client secrets
  rather than certificates, and documents the current two-year lifetime.
