# OpenShift CI Credentials for ARO HCP E2E Tests

This directory contains scripts to manage Azure AD credentials for ARO HCP E2E tests in the Test Test Azure Red Hat OpenShift tenant.

## Scripts Overview

| Script | Purpose |
|--------|---------|
| `create-openshift-release-bot-msft-test.sh` | Create Azure AD app + roles + permissions (calls `recycle-openshift-release-bot-creds.sh`) |
| `recycle-openshift-release-bot-creds.sh` | Rotate credentials and update `*-msft` Vault secrets |
| `switch-vault-tenant.sh` | Switch active secrets between MSFT and RH tenant |

## Vault Secret Structure

```
selfservice/hcm-aro/
├── aro-hcp-stg           # Active secret (used by Prow jobs, with secretsync)
├── aro-hcp-stg-msft      # Test Test Azure Red Hat OpenShift tenant credentials (backup, no secretsync)
├── aro-hcp-stg-rh-tenant # Original Red Hat tenant credentials (backup, no secretsync)
├── aro-hcp-prod          # Active secret (used by Prow jobs, with secretsync)
├── aro-hcp-prod-msft     # Test Test Azure Red Hat OpenShift tenant credentials (backup, no secretsync)
└── aro-hcp-prod-rh-tenant # Original Red Hat tenant credentials (backup, no secretsync)
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

# Switch to MSFT tenant
./switch-vault-tenant.sh --to msft
```

## Switching Tenants

```bash
# Check current tenant status
./switch-vault-tenant.sh --status

# Switch to Test Test Azure Red Hat OpenShift tenant
./switch-vault-tenant.sh --to msft

# Rollback to Red Hat tenant
./switch-vault-tenant.sh --to rh-tenant

# Switch only specific environment
./switch-vault-tenant.sh --to msft --env stg
./switch-vault-tenant.sh --to msft --env prod
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
./switch-vault-tenant.sh --to msft
```

## Verification

```bash
# Check current tenant status
./switch-vault-tenant.sh --status
```

## Troubleshooting

### Rollback to Red Hat Tenant

If issues occur with MSFT tenant:

```bash
./switch-vault-tenant.sh --to rh-tenant
```

### Check Prow Job Logs

Look for `Acquired 1 lease(s) for aro-hcp-test-tenant-quota-slice` in the build logs to confirm MSFT tenant is being used.

## Documentation

For detailed documentation, see:
- [Test Test Azure Red Hat OpenShift Tenant Access SOP](../../docs/sops/msft-test-test-tenant-access.md)
