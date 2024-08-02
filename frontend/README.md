# ARO-HCP-FRONTEND

## Build frontend binary for local testing
```
make frontend
```

## Run the frontend binary locally (requires a local running CS to fully function)
```
make run
```

## Build the frontend container
```bash
# Note: for testing changes, please use your own registry
# versus pushing images to the DEV ACR
export ARO_HCP_BASE_IMAGE="quay.io/QUAY_USERNAME"
make image

# Push the image to a container registry
make push

# all in one option
export ARO_HCP_BASE_IMAGE="quay.io/QUAY_USERNAME"
make build-push
```

## Run the frontend container

**Locally**:
```bash
docker run -p 8443:8443 aro-hcp-frontend
```

**In Cluster:**
```bash
### Using Kustomize -- Prefered as this is how DEV is updated
# Requires kustomize CLI to be installed
AKSCONFIG=svc-cluster make kustomize-deploy
AKSCONFIG=svc-cluster make kustomize-undeploy

### Non-Kustomize -- eventually will be removed?
AKSCONFIG=svc-cluster make deploy
AKSCONFIG=svc-cluster make undeploy

# If using a private cluster
AKSCONFIG=svc-cluster make deploy-private
AKSCONFIG=svc-cluster make undeploy-private
```

> To create a cluster, follow the instructions in [development-setup.md](../dev-infrastructure/docs/development-setup.md)

## Available endpoints

> Note: If you need a test cluster.json file or node_pool.json for some of the below API calls, you can generate one using [utils/create.go](./utils/create.go)
> `go run utils/create.go -type cluster`
> or
> `go run utils/create.go -type node_pool`
> Any Create/Get/Delete cluster calls below will expect a running CS in order to function for now



Update a subscription state (Must be **Registered** for other calls to function)
```bash
curl -X PUT "localhost:8443/subscriptions/00000000-0000-0000-0000-000000000000?api-version=2.0" --json '{"state":"Registered", "properties": { "tenantId": "00000000-0000-0000-0000-000000000000"}}'
```

List the Operations for the Provider
```bash
curl -X GET "localhost:8443/providers/Microsoft.RedHatOpenShift/operations?api-version=2024-06-10-preview"
```

List HcpOpenShiftVersions Resources by Location

```bash
curl -X GET "localhost:8443/subscriptions/00000000-0000-0000-0000-000000000000/locations/YOUR_LOCATION/providers/Microsoft.RedHatOpenShift/hcpOpenShiftVersions?api-version=2024-06-10-preview"
```

List HcpOpenShiftClusterResource Resources by Subscription ID
```bash
curl -X GET "localhost:8443/subscriptions/00000000-0000-0000-0000-000000000000/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters?api-version=2024-06-10-preview"
```

Get a HcpOpenShiftClusterResource
```bash
curl -X GET "localhost:8443/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dev-test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/dev-test-cluster?api-version=2024-06-10-preview"
```

Create or Update a HcpOpenShiftClusterResource
```bash
curl -X PUT "localhost:8443/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dev-test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/dev-test-cluster?api-version=2024-06-10-preview" \
  -H "X-Ms-Arm-Resource-System-Data: {\"createdBy\": \"aro-hcp-local-testing\", \"createdByType\": \"User\", \"createdAt\": \"2024-06-06T19:26:56+00:00\"}" \
  --json @cluster.json
```

Delete a HcpOpenShiftClusterResource
```bash
curl -X DELETE "localhost:8443/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dev-test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/dev-test-cluster?api-version=2024-06-10-preview"
```

Execute deployment preflight checks
```bash
curl -X POST "localhost:8443/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dev-test-rg/providers/Microsoft.RedHatOpenShift/deployments/YOUR_DEPLOYMENT_NAME/preflight?api-version=2020-06-01" --json preflight.json
```

NodePool operations:

Create nodepool
```bash
curl -X PUT "localhost:8443/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dev-test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/dev-test-cluster/nodepools/dev-nodepool?api-version=2024-06-10-preview" \
  -H "X-Ms-Arm-Resource-System-Data: {\"createdBy\": \"aro-hcp-local-testing\", \"createdByType\": \"User\", \"createdAt\": \"2024-06-06T19:26:56+00:00\"}" --json @node_pool.json
```

Get nodepool
```bash
curl "localhost:8443/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dev-test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/dev-test-cluster/nodepools/dev-nodepool?api-version=2024-06-10-preview"
```

Delete nodepool
```bash
curl -X DELETE "localhost:8443/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dev-test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/dev-test-cluster/nodepools/dev-nodepool?api-version=2024-06-10-preview"
```