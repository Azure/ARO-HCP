# OpenShift CI Credentials for ARO HCP E2E Tests

This directory contains scripts to manage Azure AD credentials for ARO HCP E2E tests in the Test Test Azure Red Hat OpenShift tenant.

## Base Image

The `Dockerfile` in this directory defines the base image used in our release configurations. This image includes:

- Red Hat UBI 9 base image
- Azure CLI with Bicep extension
- Go 1.25.7
- kubectl and kubelogin
- OpenShift CLI (oc)
- Promtool
- Required system tools (make, git, procps-ng)

### Version Management

Tool versions are defined in `versions.mk` and mirrored as `ARG` defaults in the `Dockerfile`. When updating a version:

1. Edit `versions.mk` with the new version
2. Update the matching `ARG` default in the `Dockerfile`
3. Run `make verify` to check both files are in sync and Go major.minor matches `go.work`
4. Run `make test` to build the image and smoke-test all tools

### CI Build Flow

We have created a Post Submit job in Release repo https://github.com/openshift/release/blob/master/ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__baseimage-generator.yaml , which would build this Docker image after any PR merges.

And in our Release Job(presubmit/periodic) we consume this prebuild images as build root https://github.com/openshift/release/blob/master/ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml#L7 .

## Scripts Overview

| Script | Purpose |
|--------|---------|
| `create-openshift-release-bot-msft-test.sh` | Create Azure AD app + roles + permissions (calls `recycle-openshift-release-bot-creds.sh`) |
| `recycle-openshift-release-bot-creds.sh` | Rotate credentials and update `*-test-tenant` Vault secrets |
| `switch-vault-tenant.sh` | Switch active secrets between Test Test tenant and legacy tenant |

## Vault Secret Structure

```
selfservice/hcm-aro/
├── aro-hcp-stg              # Active secret (used by Prow jobs, with secretsync)
├── aro-hcp-stg-test-tenant  # Test Test Azure Red Hat OpenShift tenant credentials (backup, no secretsync)
├── aro-hcp-stg-legacy       # Original legacy tenant credentials (backup, no secretsync)
├── aro-hcp-prod             # Active secret (used by Prow jobs, with secretsync)
├── aro-hcp-prod-test-tenant # Test Test Azure Red Hat OpenShift tenant credentials (backup, no secretsync)
└── aro-hcp-prod-legacy      # Original legacy tenant credentials (backup, no secretsync)
```

## Prerequisites

- Enable Global Administrator via PIM (for app registration API Resource permission admin consent)
- Enable User Access Administrator / Owner role via PIM (for role assignments)
- az CLI installed and logged in: `az login --tenant 93b21e64-4824-439a-b893-46c9b2a51082`
- HashiCorp Vault CLI installed
- jq installed

## Initial Setup (One-time)

```bash
# Create Azure AD app, assign roles, grant permissions, and store credentials
./create-openshift-release-bot-msft-test.sh

# Switch to Test Test tenant
./switch-vault-tenant.sh --to test-tenant
```

## Switching Tenants

```bash
# Check current tenant status
./switch-vault-tenant.sh --status

# Switch to Test Test Azure Red Hat OpenShift tenant
./switch-vault-tenant.sh --to test-tenant

# Rollback to legacy tenant
./switch-vault-tenant.sh --to legacy

# Switch only specific environment
./switch-vault-tenant.sh --to test-tenant --env stg
./switch-vault-tenant.sh --to test-tenant --env prod
```

## Credential Rotation

When credentials are expiring or need to be rotated:

```bash
# Rotate credentials (keeps old as backup)
./recycle-openshift-release-bot-creds.sh

# Rotate and delete old credentials
./recycle-openshift-release-bot-creds.sh --delete-old

# Rotate only specific environment
./recycle-openshift-release-bot-creds.sh --env stg

# Apply rotated credentials to active secrets
./switch-vault-tenant.sh --to test-tenant
```

## Verification

```bash
# Check current tenant status
./switch-vault-tenant.sh --status
```

## Troubleshooting

### Rollback to Legacy Tenant

If issues occur with Test Test tenant:

```bash
./switch-vault-tenant.sh --to legacy
```

### Check Prow Job Logs

Look for `Acquired 1 lease(s) for aro-hcp-test-tenant-quota-slice` in the build logs to confirm Test Test tenant is being used.

## Documentation

For detailed documentation, see:
- [Test Test Azure Red Hat OpenShift Tenant Access SOP](../../docs/sops/test-test-tenant-access.md)
