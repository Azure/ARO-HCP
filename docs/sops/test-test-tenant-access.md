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

The "OpenShift Release Bot MSFT Test" app in the Test Test Azure Red Hat OpenShift tenant is the CI identity for the
jobs that use the `prod` profile (subscription "ARO HCP E2E", `403d9de9-132b-4974-94a5-5b78bdfa191e`) and for cleaning
up expired app registrations in this tenant.

### How CI Gets the Credentials

1. Credentials are stored in **Google Secret Manager (GSM)**, collection `hcm-aro`, one group per CI identity
2. CI steps in [openshift/release](https://github.com/openshift/release/tree/main/ci-operator/step-registry/aro-hcp)
   mount the groups they need as files under `/var/run/<group>`
3. The `VAULT_SECRET_PROFILE` variable selects which mounted group a step uses
   (`/var/run/aro-hcp-${VAULT_SECRET_PROFILE}`)

### GSM Groups for the Test Test Tenant

| GSM group | `VAULT_SECRET_PROFILE` | Used by |
|-----------|------------------------|---------|
| `aro-hcp-prod` | `prod` | `prod-e2e-parallel`, `production` (CAPZ), `delete-expired-prod-resource-groups` (hourly) |
| `aro-hcp-msft-test-tenant` | `msft-test-tenant` | `delete-expired-msft-test-tenant-app-registrations` (hourly) |

Other profiles (`dev`, `int-rh`, `stg-rh`, `prod-rh`) use their own groups and are not covered by this SOP.
GSM values cannot be read back; `sm describe` shows a secret's metadata only.

### Rotating the CI Client Secret

The app lives in this Microsoft tenant, but CI reads its secret from GSM, which needs Red Hat SSO. Run each step on a
machine with the access it needs: **Azure** = `az` logged in to this tenant
(`az login --tenant 93b21e64-4824-439a-b893-46c9b2a51082`), **GSM** = the
[Secret Manager CLI](https://docs.ci.openshift.org/architecture/cli-secret-manager/) after `sm login`, with access to the
`hcm-aro` collection. Rotate before the current secret expires; check its expiry with step 2.

1. **GSM**: confirm you can update both groups. Stop here if a line is missing, before any secret is created:

   ```bash
   sm list -c hcm-aro | grep -E '^(aro-hcp-prod|aro-hcp-msft-test-tenant)/client-secret'
   ```

2. **Azure**: note the current secrets (their key IDs are deleted in step 6):

   ```bash
   APP_ID=$(az ad app list --filter "displayName eq 'OpenShift Release Bot MSFT Test'" --query '[].appId' -o tsv)
   echo "${APP_ID}"   # must be exactly one app ID: stop if it is empty or lists more than one
   az ad app credential list --id "${APP_ID}" --query '[].{keyId:keyId,name:displayName,end:endDateTime}' -o table
   ```

3. **Azure**: create a new secret (existing ones keep working). It is shown once, with a short hash for step 4:

   ```bash
   S=$(az ad app credential reset --id "${APP_ID}" --append --display-name "OpenShift CI $(date -u +%F)" --query password -o tsv)
   echo "${S}"; printf '%s' "${S}" | sha256sum | cut -c1-8; unset S
   ```

   Enter the secret yourself where you run step 4. Never paste it into chat, tickets, email or AI tools, and do not
   run this step in a terminal that is logged or recorded.

4. **GSM**: enter the secret at the hidden prompt and check that the hash matches step 3 before publishing
   (in steps 3 and 4, use `sha256sum` or `shasum -a 256`, whichever your system has). A file is used so the
   secret stays out of shell history:

   ```bash
   read -rs S; printf '%s' "${S}" | shasum -a 256 | cut -c1-8
   # only if the hash matches step 3:
   F=$(mktemp "${HOME}/.release-bot-secret.XXXXXX") && printf '%s' "${S}" > "${F}" && unset S \
     && sm update -c hcm-aro aro-hcp-prod/client-secret --from-file="${F}" \
     && sm update -c hcm-aro aro-hcp-msft-test-tenant/client-secret --from-file="${F}" \
     && echo "PUBLISHED to both groups"
   rm -f "${F}"
   ```

   Continue only if `PUBLISHED to both groups` is printed. Otherwise fix the error and run step 4 again with the same
   secret; do not go on to step 6 while a group may still hold the old secret.

5. **Verify**: new CI jobs use the new secret immediately. Before step 6, wait for a run that started after step 4
   to pass for each group: `delete-expired-msft-test-tenant-app-registrations` (hourly, `aro-hcp-msft-test-tenant`)
   and one job with the `prod` profile, for example `delete-expired-prod-resource-groups` (hourly, `aro-hcp-prod`).

6. **Azure**: wait until every CI run that started before step 4 has finished (for example 24 hours), because a
   running job keeps requesting tokens with the secret it loaded at start. Then delete the previous secrets noted in
   step 2:

   ```bash
   az ad app credential delete --id "${APP_ID}" --key-id <old-key-id>
   ```

If jobs fail after step 4, fix forward: GSM keeps older versions, but the CLI cannot restore them and the old value
cannot be read back. Repeat steps 3 and 4 with a new secret, then delete the failed one.

> **⚠️ Important**: 
> - You must have Azure AD admin permissions (or be an owner of the app) to reset app credentials
> - **Audit Trail**: Document all credential rotations in a team ticket (date, who, reason)

### Recreating the App

Only needed if the app was deleted. You need Global Administrator (for admin consent) and Owner or User Access
Administrator on the "ARO HCP E2E" subscription (for role assignments), both via PIM.

```bash
APP_ID=$(az ad app create --display-name "OpenShift Release Bot MSFT Test" --query appId -o tsv)
az ad sp create --id "${APP_ID}"
SCOPE="/subscriptions/403d9de9-132b-4974-94a5-5b78bdfa191e"

az role assignment create --assignee "${APP_ID}" --role "Contributor" --scope "${SCOPE}"

# Can assign any role except Owner, User Access Administrator and Role Based Access Control Administrator
CONDITION="((!(ActionMatches{'Microsoft.Authorization/roleAssignments/write'})) OR (@Request[Microsoft.Authorization/roleAssignments:RoleDefinitionId] ForAnyOfAllValues:GuidNotEquals {8e3af657-a8ff-443c-a75c-2fe8c4bcb635, 18d7d88d-d35e-4fb5-a5c3-7773c20a72d9, f58310d9-a9f6-439a-9e8d-f62e7b41a168})) AND ((!(ActionMatches{'Microsoft.Authorization/roleAssignments/delete'})) OR (@Resource[Microsoft.Authorization/roleAssignments:RoleDefinitionId] ForAnyOfAllValues:GuidNotEquals {8e3af657-a8ff-443c-a75c-2fe8c4bcb635, 18d7d88d-d35e-4fb5-a5c3-7773c20a72d9, f58310d9-a9f6-439a-9e8d-f62e7b41a168}))"
az role assignment create --assignee "${APP_ID}" --role "Role Based Access Control Administrator" --scope "${SCOPE}" \
  --condition "${CONDITION}" --condition-version "2.0"

# Microsoft Graph: User.Read (delegated) and Application.ReadWrite.OwnedBy (application)
az ad app permission add --id "${APP_ID}" --api 00000003-0000-0000-c000-000000000000 \
  --api-permissions e1fe6dd8-ba31-4d61-89e7-88639da4683d=Scope 18a4783c-866b-4cc7-a460-3d5e5662c884=Role
az ad app permission admin-consent --id "${APP_ID}"
```

Then follow [Rotating the CI Client Secret](#rotating-the-ci-client-secret). The app ID is new, so in step 4 also
update the `client-id` and `tenant` fields of both groups:
`sm update -c hcm-aro <group>/client-id --from-literal=<app-id>` and
`sm update -c hcm-aro <group>/tenant --from-literal=93b21e64-4824-439a-b893-46c9b2a51082`.


## Common Tasks

### Requesting Quota Increases

#### Current Quota Allocations

The following quotas have been requested and approved for ARO HCP E2E testing:

**STAGE Environment (uksouth):**
| Quota Type | Current Allocation |
|------------|-------------------|
| Public IPv4 Addresses - Standard | 300 |
| Standard DSv3 Family vCPUs | 300 |
| Standard Dplsv6 Family vCPUs | 100 (pending) |

**ARM64 VM Quota (Dplsv6 Family):**

The ARM64 node pool test (`arm64_nodepool.go`) uses `Standard_D2pls_v6` VMs. This requires the **Standard Dplsv6 Family vCPUs** quota.

| Region | Current Quota | Requested |
|--------|---------------|-----------|
| uksouth | 10 | 100 |
| brazilsouth | 10 | 100 |
| centralindia | 10 | 100 |

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
| Region | DSv3 vCPUs | Dplsv6 vCPUs (ARM64) |
|--------|------------|---------------------|
| uksouth | 300 | 100 (pending) |
| brazilsouth | 300 | 100 (pending) |
| centralindia | 300 | 100 (pending) |

**Note**: Other 5 production regions (switzerlandnorth, canadacentral, australiaeast, westeurope, eastus2) do not have E2E tests configured yet, so vCPU quotas were not requested.

#### Boskos Quota Slices (Concurrency Limits)

Boskos leases control how many E2E tests can run concurrently. Each test must acquire leases before running.

**Configuration files:**
- Lease definitions: [`openshift/release` → `core-services/prow/02_config/_boskos.yaml`](https://github.com/openshift/release/blob/master/core-services/prow/02_config/_boskos.yaml)
- Generator script: [`openshift/release` → `core-services/prow/02_config/generate-boskos.py`](https://github.com/openshift/release/blob/master/core-services/prow/02_config/generate-boskos.py)

| Lease Type | Max Count | Purpose |
|------------|-----------|---------|
| `aro-hcp-stg-quota-slice` | 3 | Up to 3 STAGE tests simultaneously |
| `aro-hcp-prod-quota-slice` | 3 | Up to 3 PROD tests simultaneously |
| `aro-hcp-int-quota-slice` | 1 | Only 1 INT test at a time |
| `aro-hcp-test-tenant-quota-slice` | 10 | Up to 10 tests using MSFT Test tenant |

**How it works:**
- Each E2E test needs **two leases**: environment-specific (e.g., `aro-hcp-stg-quota-slice`) + tenant-wide (`aro-hcp-test-tenant-quota-slice`)
- STAGE and PROD can run simultaneously (different environment leases)
- Up to 3 STAGE tests can run simultaneously (3 `aro-hcp-stg-quota-slice` available)
- Up to 3 PROD tests can run simultaneously (3 `aro-hcp-prod-quota-slice` available)

#### How to Request Additional Quotas

If you need to request quota increases for new regions or resources:

1. Navigate to [Azure Portal](https://portal.azure.com)
2. Select **Subscriptions** → **ARO HCP E2E - Staging** (or **ARO HCP E2E** for prod)
3. In the left menu, select **Usage + quotas**
4. Find the quota you need to increase (e.g., "Total Regional vCPUs")
5. Click the pencil icon and request a new limit

## Troubleshooting

### Issue: "Subscription not found"

**Cause**: You don't have access yet, or access hasn't propagated.

**Solution**:
1. Make sure you have accepted the invitation to this tenant 
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

