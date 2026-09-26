# Test Test Tenant Access

This SOP covers the Azure identity used by the active `aro-hcp-prod` OpenShift
CI cluster profile in the Test Test Azure Red Hat OpenShift tenant.

## Current Model

- Azure application: `OpenShift Release Bot MSFT Test`
- Tenant: Test Test Azure Red Hat OpenShift
- Active Vault profile: `kv/selfservice/hcm-aro/aro-hcp-prod`
- Credential-management scripts:
  - `dev-infrastructure/openshift-ci/create-openshift-release-bot-msft-test.sh`
  - `dev-infrastructure/openshift-ci/recycle-openshift-release-bot-creds.sh`

The profile contains identity fields and purpose-qualified subscription fields.
See [Cluster Profile Secret Contract](../ci/cluster-profile-secret-contract.md)
for the authoritative shape.

Tenant switching and legacy backup profiles are no longer supported. Do not
recreate the deleted `*-legacy` or `*-test-tenant` groups, and do not add
unqualified `subscription-id`, `subscription-name`, or
`infra-subscription-id` fields.

## Prerequisites

- Azure CLI authenticated to the Test Test tenant
- permissions to manage the application credential
- HashiCorp Vault CLI access to `kv/selfservice/hcm-aro/aro-hcp-prod`
- `jq`

Use privileged Azure roles only for the duration required by the operation and
follow the team's access and audit procedures.

## Initial Application Setup

From `dev-infrastructure/openshift-ci/`:

```bash
./create-openshift-release-bot-msft-test.sh
```

The script creates or updates the Azure application, grants its configured
permissions and subscription roles, and invokes the rotation script to patch
the active PROD profile.

## Credential Rotation

Rotate credentials while retaining existing credentials during the rollout:

```bash
./recycle-openshift-release-bot-creds.sh
```

Delete all existing application credentials while rotating only when the old
credentials must be revoked immediately:

```bash
./recycle-openshift-release-bot-creds.sh --delete-old
```

The rotation script patches only:

- `client-id`
- `client-secret`
- `tenant`

It preserves all `customer-shardN-subscription-*`,
`infra-*-subscription-*`, and secret-sync fields.

## Verification

Verify the non-secret identity fields without printing the client secret:

```bash
vault kv get -field=tenant kv/selfservice/hcm-aro/aro-hcp-prod
vault kv get -field=client-id kv/selfservice/hcm-aro/aro-hcp-prod
```

After secret sync has propagated, rehearse an affected PROD job and verify that
authentication succeeds with the expected cluster profile. For slot-managed
jobs, confirm `${SHARED_DIR}/aro-hcp-slot.env` identifies the expected
`SELECTED_CLUSTER_PROFILE_DIR` and customer subscription.

## Recovery

There is no tenant-switch rollback script. If a new credential is invalid:

1. create another credential for the intended Azure application
2. patch the active profile's three identity fields
3. wait for secret sync
4. rerun the affected rehearsal

Do not recover by copying an entire profile from another secret. That can
replace the customer shard inventory with subscription data from a different
tenant or purpose.

Document rotations and recovery actions in the tracking ticket, including the
date, operator, reason, and validation result.
