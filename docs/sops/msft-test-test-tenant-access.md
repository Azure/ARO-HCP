# Access to Test Test Azure Red Hat OpenShift Tenant

This document provides instructions for requesting and obtaining access to the **Test Test Azure Red Hat OpenShift** tenant, which is used for ARO HCP E2E testing in Stage and Production environments.

## Overview

The **Test Test Azure Red Hat OpenShift** tenant (Tenant ID: `93b21e64-4824-439a-b893-46c9b2a51082`) is a Microsoft-managed Azure Active Directory tenant used exclusively for E2E testing of the ARO HCP service. This tenant hosts the following subscriptions:

- **ARO HCP E2E - Staging** (Subscription ID: `99399281-00a2-4b39-bb3d-b2645bbbdb93`)
- **ARO HCP E2E** (Subscription ID: `403d9de9-132b-4974-94a5-5b78bdfa191e`)

## When Access is Needed

You need access to this tenant if you are:
- Running E2E tests against the Stage or Production environments
- Debugging test failures in the CI/CD pipeline
- Requesting Azure quota increases for E2E testing
- Managing Azure resources created by automated tests
- Creating support tickets for E2E testing infrastructure

## Prerequisites

### Required Accounts
- **Microsoft b-Account**

### Team Membership
- You must be a member of the ARO HCP engineering team

## Access Request Process

### 1. Request Access via Email Invitation

Access to the Test Test Azure Red Hat OpenShift tenant is granted through **email invitation**.

**To request access:**

1. **Contact the ARO HCP Service Lifecycle Team Lead:**
   - **Slack**: @aro-hcp-service-lifecycle-ic
   - **Channel**: `#hcm-aro-team-service-lifecycle`

2. **Wait for email invitation:**
   - You will receive an email invitation from Microsoft
   - The email will be titled **"Microsoft Invitations"**
   - Organization: **Test Test Azure Red Hat OpenShift**
   - **Click "Accept invitation"** in the email

### 2. Access Level

Once invited, you will typically receive **Contributor** access to:
- **ARO HCP E2E - Staging** (Subscription ID: `99399281-00a2-4b39-bb3d-b2645bbbdb93`)
- **ARO HCP E2E** (Subscription ID: `403d9de9-132b-4974-94a5-5b78bdfa191e`)

### 3. Accepting the Invitation

**Important**: The initial invitation acceptance may require specific network access:

- **Option 1: SAW (Secure Admin Workstation)** - Recommended for initial invitation acceptance
  - Open the invitation link in your SAW device by logging in with your b-account
  - Set up multi-factor authentication (MFA) for this tenant in the Microsoft Authenticator app on your phone
  - This ensures proper authentication through Microsoft's secure channels

- **Option 2: VPN Connection**
  - You may also be able to accept this invitation by connecting to the MSFT VPN, but if this fails please use your SAW instead

**Note**: After the initial invitation is accepted (typically via SAW), subsequent access from your local development machine may work without VPN. However, this varies by user configuration and network setup. If you encounter authentication issues, try:
1. Connecting to VPN
2. Using SAW for the operation
3. Contacting the Service Lifecycle team for assistance

### 4. Verify Access

Once approved, verify your access:

1. **Login to Azure Portal**
   ```bash
   az login --tenant 93b21e64-4824-439a-b893-46c9b2a51082
   ```

2. **List accessible subscriptions**
   ```bash
   az account list --output table
   ```
   
   You should see:
   ```
   Name                       CloudName    SubscriptionId                        State    IsDefault
   -------------------------  -----------  ------------------------------------  -------  -----------
   ARO HCP E2E - Staging      AzureCloud   99399281-00a2-4b39-bb3d-b2645bbbdb93  Enabled  False
   ARO HCP E2E                AzureCloud   403d9de9-132b-4974-94a5-5b78bdfa191e  Enabled  False
   ```

3. **Set the subscription**
   ```bash
   az account set --subscription "ARO HCP E2E - Staging"
   ```

4. **Verify you can list resources**
   ```bash
   az group list --output table
   ```

5. **Verify tenant ID**
   ```bash
   az account show --query tenantId -o tsv
   ```
   
   Expected output: `93b21e64-4824-439a-b893-46c9b2a51082`

## CI/CD Service Principal Configuration

**✅ Status**: Service principals and credentials for the Test Test Azure Red Hat OpenShift tenant have **already been configured** for ARO HCP CI/CD.

### Key Mechanism: Cluster Profiles

ARO HCP uses **OpenShift CI Cluster Profiles** to manage multi-tenant authentication. Each environment (INT, STAGE, PROD) uses a different cluster profile, which automatically injects the correct Azure credentials into test jobs.

**How it works:**
1. **Cluster Profiles** define environment-specific configurations
2. Each profile references a **Kubernetes secret** containing Azure credentials
3. **Secret Sync Controller** syncs credentials from Vault to Kubernetes secrets
4. **ci-operator** mounts these secrets into test pods automatically

### Environment to Credential Mapping

| Environment | Cluster Profile | Azure Subscription | Vault Secret Path | K8s Secret Name |
|-------------|----------------|-------------------|-------------------|-----------------|
| **INT** | `aro-hcp-int` | MSIT INT | `selfservice/hcm-aro/aro-hcp-int` | `cluster-secrets-aro-hcp-int` |
| **STAGE** | `aro-hcp-stg` | ARO HCP E2E - Staging<br>`99399281-00a2-4b39-bb3d-b2645bbbdb93` | `selfservice/hcm-aro/aro-hcp-stg` | `cluster-secrets-aro-hcp-stg` |
| **PROD** | `aro-hcp-prod` | ARO HCP E2E<br>`403d9de9-132b-4974-94a5-5b78bdfa191e` | `selfservice/hcm-aro/aro-hcp-prod` | `cluster-secrets-aro-hcp-prod` |

**STAGE and PROD** use the **Test Test Azure Red Hat OpenShift tenant** (Tenant ID: `93b21e64-4824-439a-b893-46c9b2a51082`).

### Scripts Overview

Scripts are located in [`dev-infrastructure/openshift-ci/`](../../dev-infrastructure/openshift-ci/):

| Script | Purpose |
|--------|---------|
| `create-openshift-release-bot-msft-test.sh` | Create Azure AD app + roles + permissions (calls `recycle-openshift-release-bot-creds.sh`) |
| `recycle-openshift-release-bot-creds.sh` | Rotate credentials and update `*-msft` Vault secrets |
| `switch-vault-tenant.sh` | Switch active secrets between MSFT and RH tenant |

For detailed usage, see [`dev-infrastructure/openshift-ci/README.md`](../../dev-infrastructure/openshift-ci/README.md).

### Vault Secret Structure

```
selfservice/hcm-aro/
├── aro-hcp-stg           # Active secret (used by Prow jobs, with secretsync)
├── aro-hcp-stg-msft      # Test Test Azure Red Hat OpenShift tenant credentials (backup, no secretsync)
├── aro-hcp-stg-rh-tenant # Original Red Hat tenant credentials (backup, no secretsync)
├── aro-hcp-prod          # Active secret (used by Prow jobs, with secretsync)
├── aro-hcp-prod-msft     # Test Test Azure Red Hat OpenShift tenant credentials (backup, no secretsync)
└── aro-hcp-prod-rh-tenant # Original Red Hat tenant credentials (backup, no secretsync)
```

### Rollback to Red Hat Tenant


```bash
cd dev-infrastructure/openshift-ci/

# Check current tenant status
./switch-vault-tenant.sh --status

# Rollback BOTH environments to Red Hat tenant
./switch-vault-tenant.sh --to rh-tenant

# Or rollback only specific environment
./switch-vault-tenant.sh --to rh-tenant --env stg
./switch-vault-tenant.sh --to rh-tenant --env prod
```

**What this does:**
- Copies credentials from `aro-hcp-{env}-rh-tenant` → `aro-hcp-{env}`
- Preserves the `secretsync` fields in the active secret
- Changes propagate to Prow jobs in **5-10 minutes**

**When to rollback:**
- E2E tests consistently fail with authentication errors
- Azure subscription quota issues in MSFT tenant
- MSFT tenant access is revoked or expired

**After rollback:**
- Verify with `./switch-vault-tenant.sh --status`
- Test by running `/test stage-e2e-parallel` in Azure/ARO-HCP repo

### Switching to MSFT Tenant

```bash
cd dev-infrastructure/openshift-ci/

# Switch BOTH environments to MSFT tenant
./switch-vault-tenant.sh --to msft

# Or switch only specific environment
./switch-vault-tenant.sh --to msft --env stg
./switch-vault-tenant.sh --to msft --env prod
```

### Current Configuration

**Credential Storage** (Vault secrets used by Prow jobs):
- **STAGE**: `selfservice/hcm-aro/aro-hcp-stg`
- **PROD**: `selfservice/hcm-aro/aro-hcp-prod`
- Access: Requires OpenShift CI Vault permissions

**Cluster Profiles Configuration**:
- Repository: [openshift/release](https://github.com/openshift/release)
- File: [`ci-operator/step-registry/cluster-profiles/cluster-profiles-config.yaml`](https://github.com/openshift/release/blob/master/ci-operator/step-registry/cluster-profiles/cluster-profiles-config.yaml)
- Defines `aro-hcp-int`, `aro-hcp-stg`, and `aro-hcp-prod` profiles

**Prow Job Definitions**:
- Repository: [openshift/release](https://github.com/openshift/release)
- File: [`ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml`](https://github.com/openshift/release/blob/master/ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml)
- Specifies `cluster_profile: aro-hcp-stg` or `cluster_profile: aro-hcp-prod` for each test job

### Credential Expiration and Rotation

> **⚠️ IMPORTANT: Service Principal Credential Expires on September 30, 2026**
> 
> The OpenShift CI service principal credential must be rotated before this date to prevent CI/CD pipeline failures.

**Credential Rotation:**

```bash
cd dev-infrastructure/openshift-ci/

# Rotate credentials (keeps old credentials as backup)
./recycle-openshift-release-bot-creds.sh

# Rotate and delete old credentials
./recycle-openshift-release-bot-creds.sh --delete-old

# Apply rotated credentials to active secrets (if MSFT tenant is active)
./switch-vault-tenant.sh --to msft
```

This updates the `*-msft` secrets, then `switch-vault-tenant.sh` copies them to the active secrets.

> **⚠️ Important**: 
> - You must have Azure AD admin permissions to reset app credentials
> - If you don't have these permissions, contact the Service Lifecycle team
> - **Audit Trail**: Document all credential rotations in a team ticket (date, who, reason)


## Common Tasks

### Requesting Quota Increases

#### Current Quota Allocations

The following quotas have been requested and approved for ARO HCP E2E testing:

**STAGE Environment (uksouth):**
| Quota Type | Current Allocation | Justification |
|------------|-------------------|---------------|
| Public IP Addresses | 300 | Support 15 parallel tests × 2 IPs per test |
| Total Regional vCPUs | 300 | Support 12 parallel nodepools × 16 vCPUs per nodepool |
| Standard DSv3 Family vCPUs | 300 | Node pool VMs use Standard_D8s_v3 (8 cores each) |

**PRODUCTION Environment (8 Regions):**

*Public IP Addresses (All 8 regions):*
| Region | Public IP Limit |
|--------|----------------|
| uksouth | 300 |
| brazilsouth | 300 |
| centralindia | 300 |
| switzerlandnorth | 300 |
| canadacentral | 300 |
| australiaeast | 300 |
| westeurope | 300 |
| eastus2 | 300 |

*vCPU Quotas (Active E2E Test Regions):*
| Region | DSv3 vCPUs |
|--------|------------|
| uksouth | 300 |
| brazilsouth | 300(pending) |
| centralindia | 300 |

**Note**: Other 5 production regions (switzerlandnorth, canadacentral, australiaeast, westeurope, eastus2) do not have E2E tests configured yet, so vCPU quotas were not requested.

#### Boskos Quota Slices (Concurrency Limits)

Boskos leases control how many E2E tests can run concurrently. Each test must acquire leases before running.

**Configuration files:**
- Lease definitions: [`openshift/release` → `core-services/prow/02_config/_boskos.yaml`](https://github.com/openshift/release/blob/master/core-services/prow/02_config/_boskos.yaml)
- Generator script: [`openshift/release` → `core-services/prow/02_config/generate-boskos.py`](https://github.com/openshift/release/blob/master/core-services/prow/02_config/generate-boskos.py)

| Lease Type | Max Count | Purpose |
|------------|-----------|---------|
| `aro-hcp-stg-quota-slice` | 1 | Only 1 STAGE test at a time |
| `aro-hcp-prod-quota-slice` | 1 | Only 1 PROD test at a time |
| `aro-hcp-int-quota-slice` | 1 | Only 1 INT test at a time |
| `aro-hcp-test-tenant-quota-slice` | 10 | Up to 10 tests using MSFT Test tenant |

**How it works:**
- Each E2E test needs **two leases**: environment-specific (e.g., `aro-hcp-stg-quota-slice`) + tenant-wide (`aro-hcp-test-tenant-quota-slice`)
- STAGE and PROD can run simultaneously (different environment leases)
- Two STAGE tests cannot run simultaneously (only 1 `aro-hcp-stg-quota-slice` available)

#### How to Request Additional Quotas

If you need to request quota increases for new regions or resources:

1. Navigate to [Azure Portal](https://portal.azure.com)
2. Select **Subscriptions** → **ARO HCP E2E - Staging** (or **ARO HCP E2E** for prod)
3. In the left menu, select **Usage + quotas**
4. Find the quota you need to increase (e.g., "Total Regional vCPUs")
5. Click the pencil icon and request a new limit

### Entra ID Directory Object Limits

High-volume E2E testing can hit the Entra ID directory object limit (500,000) due to soft-deleted objects counting towards the quota. 

> **TODO**: Consider reusing Managed Service Identities (MSI) where possible to reduce the number of directory objects created.

## Automated Cleanup

Both STAGE and PROD subscriptions have automated cleanup enabled to prevent resource accumulation and quota limits.

### Automation Accounts

| Environment | Resource Group | Automation Account Name |
|-------------|----------------|-------------------------|
| STAGE | `hcp-stage-automation-account` | `hcp-stage-automation` |
| PROD | `hcp-prod-automation-account` | `hcp-prod-automation` |

### Cleanup Schedules

**Role Assignment Cleanup** (PowerShell):
- **Schedule**: Nightly at midnight UTC
- **Purpose**: Removes orphaned role assignments (where the principal/identity has been deleted)
- **Runbook**: `hcp-{env}-automation_roleAssignmentsCleanup`

### Manual Testing

To manually trigger cleanup (e.g., after large test runs):

1. Go to Azure Portal → Automation Accounts → `hcp-{env}-automation` → Runbooks
2. Select the runbook to test
3. Click "Start"
4. Monitor the job output

## Troubleshooting

### Issue: "Subscription not found"

**Cause**: You don't have access yet, or access hasn't propagated.

**Solution**:
1. Make sure you have accepted the invition to this tenant 
2. Wait 10-15 minutes for access to propagate
3. Clear Azure CLI cache: `az account clear && az login`

### Issue: "Cannot create resources - quota exceeded"

**Cause**: Subscription quotas are insufficient for your testing needs.

**Solution**:
1. Check current quotas: `az vm list-usage --location uksouth -o table`
2. Request quota increase following the steps above

## Related Documentation

- [Environments Overview](../environments.md) - Understand the different ARO HCP environments
- [MSIT INT Credential Setup](./msit-int-credential-setup.md) - Similar process for INT environment
- [OpenShift CI Configuration](https://github.com/openshift/release/tree/master/ci-operator/config/Azure/ARO-HCP) - Prow job definitions for ARO HCP
- [Cluster Profiles Documentation](https://docs.ci.openshift.org/docs/architecture/step-registry/#cluster-profiles) - Official OpenShift CI cluster profiles guide
- [OpenShift CI Secret Management](https://docs.ci.openshift.org/docs/how-tos/adding-a-new-secret-to-ci/) - How to add/update secrets in OpenShift CI

## Support Contacts

- **Team Slack**: `#hcm-aro-team-service-lifecycle` (for general questions)

