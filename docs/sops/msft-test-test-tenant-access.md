# Access to MSFT Test Test Tenant

This document provides instructions for requesting and obtaining access to the **MSFT Test Test** tenant, which is used for ARO HCP E2E testing in Stage and Production environments.

## Overview

The **MSFT Test Test** tenant (Tenant ID: `93b21e64-4824-439a-b893-46c9b2a51082`) is a Microsoft-managed Azure Active Directory tenant used exclusively for E2E testing of the ARO HCP service. This tenant hosts the following subscriptions:

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

Access to the MSFT Test Test tenant is granted through **email invitation**.

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

**✅ Status**: Service principals and credentials for the MSFT Test Test tenant have **already been configured** for ARO HCP CI/CD.

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

**STAGE and PROD** use the **MSFT Test Test tenant** (Tenant ID: `93b21e64-4824-439a-b893-46c9b2a51082`).

### Current Setup

The ARO HCP project uses service principals to authenticate OpenShift CI (Prow) jobs with the MSFT Test Test tenant. The service principals were created using the setup script at [`dev-infrastructure/openshift-ci/create-openshift-release-bot-msft-test.sh`](../../dev-infrastructure/openshift-ci/create-openshift-release-bot-msft-test.sh), and the credentials were stored in OpenShift CI Vault.

**What was configured:**
- Service principal created in the MSFT Test Test tenant (Tenant ID: `93b21e64-4824-439a-b893-46c9b2a51082`)
- Credentials (Client ID, Client Secret, Tenant ID, Subscription ID/Name) stored in OpenShift CI Vault
- Cluster profiles configured to reference environment-specific secrets
- Secret Sync Controller configured to sync Vault secrets to Kubernetes

**Where to find the configuration:**

1. **Setup Documentation**: 
   - File: [`dev-infrastructure/openshift-ci/README.md`](../../dev-infrastructure/openshift-ci/README.md)
   - Complete documentation for the MSFT Test Test tenant setup process

2. **Setup Script**: 
   - File: [`dev-infrastructure/openshift-ci/create-openshift-release-bot-msft-test.sh`](../../dev-infrastructure/openshift-ci/create-openshift-release-bot-msft-test.sh)
   - Creates the Azure AD app registration with proper roles and permissions
   - Assigns `Contributor` and `Role Based Access Control Administrator` roles to both subscriptions
   - Grants required Graph API permissions
   - **Only needed for initial setup** - the app registration already exists

3. **Credential Recycling Script**: 
   - File: [`dev-infrastructure/openshift-ci/recycle-openshift-release-bot-creds.sh`](../../dev-infrastructure/openshift-ci/recycle-openshift-release-bot-creds.sh)
   - Used to rotate credentials when needed

4. **Cluster Profiles Configuration**:
   - Repository: [openshift/release](https://github.com/openshift/release)
   - File: [`ci-operator/step-registry/cluster-profiles/cluster-profiles-config.yaml`](https://github.com/openshift/release/blob/master/ci-operator/step-registry/cluster-profiles/cluster-profiles-config.yaml)
   - Defines `aro-hcp-int`, `aro-hcp-stg`, and `aro-hcp-prod` profiles with their corresponding Kubernetes secrets

5. **Prow Job Definitions**:
   - Repository: [openshift/release](https://github.com/openshift/release)
   - File: [`ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml`](https://github.com/openshift/release/blob/master/ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml)
   - Specifies `cluster_profile: aro-hcp-stg` or `cluster_profile: aro-hcp-prod` for each test job
   - **Note**: Tenant ID and subscription details are NO LONGER hardcoded in job definitions—they are injected via cluster profiles

6. **Credential Storage** (Vault secrets used by Prow jobs):
   - **STAGE**: `selfservice/hcm-aro/aro-hcp-stg` (credentials + stage subscription details)
   - **PROD**: `selfservice/hcm-aro/aro-hcp-prod` (credentials + prod subscription details)
   - Access: Requires OpenShift CI Vault permissions

### Verifying Cluster Profile Configuration

To verify that a cluster profile is correctly configured:

**1. Check Vault secret exists and has all required fields:**
```bash
vault kv get -format=json kv/selfservice/hcm-aro/aro-hcp-stg | jq '.data.data | keys'
# Should include: client-id, client-secret, tenant, subscription-id, 
# subscription-name, secretsync/target-name, secretsync/target-namespace
```

**2. Verify secretsync metadata is correct:**
```bash
vault kv get kv/selfservice/hcm-aro/aro-hcp-stg
# Check that:
# - secretsync/target-name matches the Kubernetes secret name
# - secretsync/target-namespace is "ci"
```

**3. Check cluster profile configuration**:
- File: [`openshift/release` repo → `ci-operator/step-registry/cluster-profiles/cluster-profiles-config.yaml`](https://github.com/openshift/release/blob/master/ci-operator/step-registry/cluster-profiles/cluster-profiles-config.yaml)
- **Default behavior**: If no `secret:` field is specified, the cluster profile uses `cluster-secrets-{profile-name}` (e.g., `cluster-secrets-aro-hcp-stg`)

**4. Test with a Prow job** (the definitive test):
- Create a test PR in the Azure/ARO-HCP repo
- Run `/test stage-e2e-parallel` or `/test prod-e2e-parallel`
- Monitor the job at https://prow.ci.openshift.org
- Check the `azure-login` step in the job logs—it should successfully authenticate with Azure
- If authentication fails, check the job logs for error messages like "invalid client secret" or "subscription not found"

### Credential Expiration and Rotation

> **⚠️ IMPORTANT: Service Principal Credential Expires on September 30, 2026**
> 
> The OpenShift CI service principal credential must be rotated before this date to prevent CI/CD pipeline failures.

**Credential Rotation Workflow:**

When credentials need to be rotated (before expiration), you must update the secrets used by Prow jobs:
- `selfservice/hcm-aro/aro-hcp-stg` (STAGE environment)
- `selfservice/hcm-aro/aro-hcp-prod` (PROD environment)

**Step 1: Reset Azure AD Credentials**

```bash
# Get the App Registration Client ID from Vault first
export VAULT_ADDR="https://vault.ci.openshift.org"
vault login --method=oidc
APP_CLIENT_ID=$(vault kv get -field=client-id kv/selfservice/hcm-aro/aro-hcp-stg)

# Reset credentials (requires Azure AD admin permissions)
az ad app credential reset \
  --id "$APP_CLIENT_ID" \
  --append \
  --display-name "OpenShift CI $(date +%Y-%m-%d)"
```

Save the output! You'll need:
- `appId`: The application client ID
- `password`: The new client secret
- `tenant`: 93b21e64-4824-439a-b893-46c9b2a51082

**Step 2: Update Vault Secrets**

```bash
export VAULT_ADDR="https://vault.ci.openshift.org"
vault login --method=oidc

# Get existing Client ID and Tenant ID from Vault
CLIENT_ID=$(vault kv get -field=client-id kv/selfservice/hcm-aro/aro-hcp-stg)
TENANT_ID=$(vault kv get -field=tenant kv/selfservice/hcm-aro/aro-hcp-stg)

echo "Enter the new client secret from Step 1:"
read -s NEW_CLIENT_SECRET

# Update STAGE
vault kv patch kv/selfservice/hcm-aro/aro-hcp-stg \
  client-id="$CLIENT_ID" \
  client-secret="$NEW_CLIENT_SECRET" \
  tenant="$TENANT_ID"

# Update PROD
vault kv patch kv/selfservice/hcm-aro/aro-hcp-prod \
  client-id="$CLIENT_ID" \
  client-secret="$NEW_CLIENT_SECRET" \
  tenant="$TENANT_ID"

# Clear the secret from memory
unset NEW_CLIENT_SECRET
```

#### Step 3: Wait and Test

- **Wait 5-10 minutes** for Secret Sync Controller to propagate changes to Kubernetes secrets
- **Test**: Create a PR in Azure/ARO-HCP and run `/test stage-e2e-parallel`
- **Verify**: Check that the test successfully authenticates with Azure

> **⚠️ Important**: 
> - You must have Azure AD admin permissions to reset app credentials
> - If you don't have these permissions, contact the Service Lifecycle team
> - The secrets `aro-hcp-stg` and `aro-hcp-prod` are the ones actually used by CI/CD
> - **Audit Trail**: Document all credential rotations in a team ticket (date, who, reason)

### Migrating to New Tenant / Updating Environment Secrets

The Prow jobs for STAGE and PROD use these Vault secrets:
- **STAGE**: `selfservice/hcm-aro/aro-hcp-stg`
- **PROD**: `selfservice/hcm-aro/aro-hcp-prod`

These secrets must contain credentials from the MSFT Test Test tenant plus environment-specific subscription details.

To update the secrets:

> **⚠️ Security Note**: Use `read -s` to input secrets securely without exposing them in shell history.

```bash
export VAULT_ADDR="https://vault.ci.openshift.org"
vault login --method=oidc

# Get existing credentials from Vault (or use known values)
# The Client ID and Tenant ID can be retrieved from existing secrets
CLIENT_ID=$(vault kv get -field=client-id kv/selfservice/hcm-aro/aro-hcp-stg 2>/dev/null || echo "")
TENANT_ID="93b21e64-4824-439a-b893-46c9b2a51082"

# If CLIENT_ID is empty, prompt for it
if [ -z "$CLIENT_ID" ]; then
    echo "Enter the Azure AD App Client ID:"
    read CLIENT_ID
fi

# Securely input the client secret (won't appear in shell history)
echo "Enter the client secret:"
read -s CLIENT_SECRET
echo ""

# Update STAGE
vault kv patch kv/selfservice/hcm-aro/aro-hcp-stg \
    client-id="$CLIENT_ID" \
    client-secret="$CLIENT_SECRET" \
    tenant="$TENANT_ID" \
    subscription-id="99399281-00a2-4b39-bb3d-b2645bbbdb93" \
    subscription-name="ARO HCP E2E - Staging" \
    secretsync/target-name="cluster-secrets-aro-hcp-stg" \
    secretsync/target-namespace="ci"

# Update PROD
vault kv patch kv/selfservice/hcm-aro/aro-hcp-prod \
    client-id="$CLIENT_ID" \
    client-secret="$CLIENT_SECRET" \
    tenant="$TENANT_ID" \
    subscription-id="403d9de9-132b-4974-94a5-5b78bdfa191e" \
    subscription-name="ARO HCP E2E" \
    secretsync/target-name="cluster-secrets-aro-hcp-prod" \
    secretsync/target-namespace="ci"

# Clear secrets from memory
unset CLIENT_SECRET
```

**Key fields required in environment Vault secrets:**

| Field | Description | Example |
|-------|-------------|---------|
| `client-id` | Azure AD App Registration Client ID | (retrieve from Vault) |
| `client-secret` | Azure AD App Registration Client Secret | (secret value - never log or commit) |
| `tenant` | Azure Tenant ID | `93b21e64-4824-439a-b893-46c9b2a51082` |
| `subscription-id` | Azure Subscription ID for the environment | `99399281-...` (STAGE) |
| `subscription-name` | Azure Subscription display name | `ARO HCP E2E - Staging` |
| `secretsync/target-name` | K8s secret name (default: `cluster-secrets-{profile-name}`) | `cluster-secrets-aro-hcp-stg` |
| `secretsync/target-namespace` | K8s namespace | `ci` |

> **Important Notes**:
> - Use `vault kv patch` to preserve other existing fields in the secrets
> - Wait 5-10 minutes after updating for Secret Sync Controller to propagate changes
> - The K8s secret name follows the default pattern: `cluster-secrets-{profile-name}` (e.g., `cluster-secrets-aro-hcp-stg`)
> - **Security**: Always use `read -s` for secret input to avoid shell history exposure
> - **Audit Trail**: Document all secret updates in a team ticket (date, who, reason, environments affected)


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

#### Boskos Quota Slices

> **TODO**: Configure Boskos quota slices to limit concurrent CI jobs in the MSFT Test Test tenant subscriptions.

#### How to Request Additional Quotas

If you need to request quota increases for new regions or resources:

1. Navigate to [Azure Portal](https://portal.azure.com)
2. Select **Subscriptions** → **ARO HCP E2E - Staging** (or **ARO HCP E2E** for prod)
3. In the left menu, select **Usage + quotas**
4. Find the quota you need to increase (e.g., "Total Regional vCPUs")
5. Click the pencil icon and request a new limit

### Entra ID Directory Object Limits

High-volume E2E testing can hit the Entra ID directory object limit (500,000) due to soft-deleted objects counting towards the quota. Microsoft has rejected requests to disable Entra ID Soft Deletion.

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

