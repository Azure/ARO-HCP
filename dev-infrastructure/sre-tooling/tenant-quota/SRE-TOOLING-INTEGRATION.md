# SRE Tooling Integration Notes

## Managed Identity Configuration

✅ **COMPLETED**: The `custom-metrics-collector` Managed Identity has been added to the sre-tooling cluster's workload identities.

### Changes Made to `sre-tooling-cluster.bicep`

Added the following to the `workloadIdentities` map in `dev-infrastructure/templates/sre-tooling-cluster.bicep`:

```bicep
custom_metrics_collector_wi: {
  uamiName: 'custom-metrics-collector'
  namespace: 'tenant-quota'
  serviceAccountName: 'custom-metrics-collector'
}
```

### Output Template Update

✅ **COMPLETED**: Added to `output-sre-tooling-cluster.bicep`:

```bicep
resource customMetricsCollectorUAMI 'Microsoft.ManagedIdentity/userAssignedIdentities@2024-11-30' existing = {
  name: 'custom-metrics-collector'
}

output customMetricsCollectorUAMIClientId string = customMetricsCollectorUAMI.properties.clientId
```

## Namespace Deployment

The `tenant-quota` namespace is **automatically created** during deployment via the `namespaceFiles` configuration in `pipeline.yaml`. No manual namespace creation is required.

## Environment Restrictions

This service should only be deployed to:
- `pers` (personal dev environments)
- `dev` (shared dev environment)

It should **NOT** be deployed to production environments (`int`, `stg`, `prod`).

**Important**: This service is **NOT** included in automatic bulk deployments (`make mgmt.deployall` or `make deployall`). It must be manually deployed using:

```bash
# Deploy to pers dev
export DEPLOY_ENV=pers
make pipeline/dev-infrastructure.sre-tooling.tenant-quota.deploy_pipeline

# Or use the Makefile deploy target from the service directory
cd dev-infrastructure/sre-tooling/tenant-quota
export DEPLOY_ENV=pers
make deploy
```

**Note**: This service is restricted to `pers` and `dev` environments through manual deployment only. It is NOT included in automatic bulk deployments (`make mgmt.deployall` or `make deployall`), preventing accidental deployment to production.

## Service Principal Secret

The service principal secret `custom-metrics-collector-redhat0-client-secret` must be stored in the sre-tooling Key Vault (not the service Key Vault).

The service principal client ID is configured via `customMetricsCollector.servicePrincipalClientId` in the config.

## Secret Management Across Environments

**Important**: The service principal secret is stored in different Key Vaults depending on the environment:

- **Original location (persistent)**: `aro-hcp-dev-svc-kv` (service Key Vault)
  - This is the source of truth for the secret
  - Secret name: `custom-metrics-collector-redhat0-client-secret`
  - Service Principal Client ID: `1ef710d1-afd7-4bf3-8095-e8126650607f`

- **Pers dev environment**: `ah-pers-tool-usw3trwi-1` (sre-tooling Key Vault)
  - Secret was copied from the service Key Vault for pers dev deployment
  - **Note**: When pers dev environment is destroyed, the secret will be lost from this Key Vault
  - **Action required**: Before deploying to pers dev again, retrieve the secret from `aro-hcp-dev-svc-kv` and add it to the pers dev sre-tooling Key Vault

- **Dev environment**: Will use the dev sre-tooling Key Vault (name will be determined at deployment time)
  - **Action required**: Before deploying to dev, retrieve the secret from `aro-hcp-dev-svc-kv` and add it to the dev sre-tooling Key Vault

### Secret Retrieval and Deployment

To retrieve the secret from the original location and deploy to a new environment:

```bash
# 1. Retrieve secret from original location
SECRET_VALUE=$(az keyvault secret show \
  --vault-name aro-hcp-dev-svc-kv \
  --name custom-metrics-collector-redhat0-client-secret \
  --query "value" -o tsv)

# 2. Find the target sre-tooling Key Vault name
# (Query the deployed infrastructure or check the pipeline outputs)

# 3. Add secret to target sre-tooling Key Vault
./dev-infrastructure/scripts/kv-add-secret.sh \
  <sre-tooling-keyvault-name> \
  <sre-tooling-resource-group> \
  custom-metrics-collector-redhat0-client-secret \
  "$SECRET_VALUE"
```

## Image Build and Deployment

**Important**: The deployment requires a container image to be available in the target ACR. The pipeline uses an `ImageMirror` step to mirror the image from the source registry to the target ACR.

### Image Configuration

- **Default registry**: `arohcpsvcdev.azurecr.io`
- **Default repository**: `custom-metrics-collector`
- **Digest**: If empty in config, uses commit SHA of the repository

### Building and Pushing a Local Image

If you need to build and push a new image from local code changes, use the `make deploy` target from the `dev-infrastructure/sre-tooling/tenant-quota/` directory:

```bash
cd dev-infrastructure/sre-tooling/tenant-quota
export DEPLOY_ENV=pers
make deploy
```

This will:
1. Build the Docker image using `Dockerfile`
2. Push the image to the target ACR (`arohcpsvcdev` for pers dev)
3. Generate an override config file with the new image digest
4. Deploy using the override config

**Note**: The `make deploy` target automatically handles image building, pushing, and config override generation. For local development/testing, you can use `Dockerfile.local` to build a development image.

### Using Existing Images

If an image already exists in the registry (e.g., from CI/CD or previous deployments), you can deploy directly using the pipeline without building:

```bash
export DEPLOY_ENV=pers
make pipeline/dev-infrastructure.sre-tooling.tenant-quota.deploy_pipeline
```

The pipeline will use the image digest specified in `config.yaml` (or commit SHA if digest is empty).

