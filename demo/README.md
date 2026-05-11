# Create an HCP

## Prepare

* Have access to an Azure subscription and resource group for cluster resources
* Authenticate with Azure: `az login`
* Set your subscription: `az account set --subscription <SUBSCRIPTION_ID>`

## Create a Cluster via ARM

The `deploy-hcp-bicep.sh` script provisions the required customer infrastructure (VNETs, subnets, NSGs, KeyVault), creates an HCP cluster, and creates a node pool using Bicep templates against ARM:

```bash
./deploy-hcp-bicep.sh
```

Configuration is controlled via environment variables in `env_vars`. Review and adjust as needed before running.

## Useful Commands

Set these variables before running the commands below:

```bash
source env_vars
```

### Show a cluster

```bash
az resource show --ids "${CLUSTER_RESOURCE_ID}" --api-version 2025-12-23-preview
```

### List clusters in a resource group

```bash
az resource list --resource-group "${CUSTOMER_RG_NAME}" --resource-type "Microsoft.RedHatOpenShift/hcpOpenShiftClusters" --api-version 2025-12-23-preview
```

### Request admin credentials

```bash
az resource invoke-action --ids "${CLUSTER_RESOURCE_ID}" --action requestAdminCredential --api-version 2025-12-23-preview
```

### Show a node pool

```bash
az resource show --ids "${NODE_POOL_RESOURCE_ID}" --api-version 2025-12-23-preview
```

### Show external auth

```bash
az resource show --ids "${CLUSTER_RESOURCE_ID}/externalAuths/<EXTERNAL_AUTH_NAME>" --api-version 2025-12-23-preview
```

## E2E Testing

For end-to-end testing scenarios, refer to the `test/` directory at the root of this repository.
