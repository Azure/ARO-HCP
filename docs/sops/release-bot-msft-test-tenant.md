# Release Bot MSFT Test Tenant

This SOP covers creating and rotating credentials for the "OpenShift Release Bot MSFT Test" Entra application in the Microsoft E2E tenant.

## Background

The OpenShift Release Bot MSFT Test is a service principal in the `Test Test Azure Red Hat OpenShift` tenant (`93b21e64-4824-439a-b893-46c9b2a51082`). It runs E2E tests against Microsoft-hosted subscriptions. Its credentials are stored in HashiCorp Vault (`vault.ci.openshift.org`) and consumed by OpenShift CI Prow jobs.

Two scripts manage it:

- [`create-openshift-release-bot-msft-test.sh`](../../dev-infrastructure/openshift-ci/create-openshift-release-bot-msft-test.sh) -- one-time setup (creates SP, assigns roles, grants Graph permissions, generates initial credentials)
- [`recycle-openshift-release-bot-creds.sh`](../../dev-infrastructure/openshift-ci/recycle-openshift-release-bot-creds.sh) -- rotates the client secret and updates Vault

These cannot be migrated to Bicep because they operate cross-tenant and store credentials in external HashiCorp Vault.

## Prerequisites

- `az` CLI logged into the test tenant: `az login --tenant 93b21e64-4824-439a-b893-46c9b2a51082`
- `vault` CLI installed; will prompt for OIDC login to `https://vault.ci.openshift.org`
- `jq` installed
- Permission to manage the application's credentials in the test tenant

## Procedure: Initial Setup

Only needed once per tenant or after a full teardown.

```bash
cd dev-infrastructure/openshift-ci
./create-openshift-release-bot-msft-test.sh
```

This creates the SP (if missing), assigns Contributor and RBAC Administrator on both E2E subscriptions, grants Graph permissions, triggers admin consent, and calls the recycle script to generate credentials.

## Procedure: Credential Rotation

### 1. Rotate credentials

```bash
cd dev-infrastructure/openshift-ci
./recycle-openshift-release-bot-creds.sh
```

By default this rotates both STG and PROD credentials. To rotate only one:

```bash
./recycle-openshift-release-bot-creds.sh --env stg
```

To also delete old credentials (instead of appending):

```bash
./recycle-openshift-release-bot-creds.sh --delete-old
```

### 2. Activate the new credentials

After rotation, switch the active tenant configuration:

```bash
./switch-vault-tenant.sh --to test-tenant
```

### 3. Verify

Confirm the new credentials work by checking a recent Prow job that uses the test tenant. Vault secrets are at:

- `kv/selfservice/hcm-aro/aro-hcp-stg-test-tenant`
- `kv/selfservice/hcm-aro/aro-hcp-prod-test-tenant`

## Key Locations

| What | Where |
|------|-------|
| Setup script | [`dev-infrastructure/openshift-ci/create-openshift-release-bot-msft-test.sh`](../../dev-infrastructure/openshift-ci/create-openshift-release-bot-msft-test.sh) |
| Rotation script | [`dev-infrastructure/openshift-ci/recycle-openshift-release-bot-creds.sh`](../../dev-infrastructure/openshift-ci/recycle-openshift-release-bot-creds.sh) |
| Tenant switch | [`dev-infrastructure/openshift-ci/switch-vault-tenant.sh`](../../dev-infrastructure/openshift-ci/switch-vault-tenant.sh) |
| Vault secrets | `kv/selfservice/hcm-aro/aro-hcp-{stg,prod}-test-tenant` on `vault.ci.openshift.org` |
| Test tenant ID | `93b21e64-4824-439a-b893-46c9b2a51082` |
| Tenant access SOP | [`docs/sops/test-test-tenant-access.md`](test-test-tenant-access.md) |
