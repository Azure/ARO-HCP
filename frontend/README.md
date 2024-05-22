# ARO-HCP-FRONTEND

## Build the frontend container
```bash
# Note: until the ACR location is defined, you must set the image base
export ARO_HCP_BASE_IMAGE="quay.io/QUAY_USERNAME"
make image

# Push the image to a container registry
make push

# all in one option
make build-push
```

## Run the frontend container

**Locally**:
```bash
docker run -p 8443:8443 aro-hcp-frontend
```

**In Cluster:**
```bash
# Deploy
make deploy

# Undeploy
make undeploy

# If using a private cluster
make deploy-private

make undeploy-private
```

> To create a cluster, follow the instructions in [development-setup.md](../dev-infrastructure/docs/development-setup.md)

## Available endpoints

> Note: If you need a test cluster.json file for some of the below API calls, you can generate one using [utils/create.go](./utils/create.go)
>
> `go run utils/create.go`

Update a subscription state (Must be **Registered** for other calls to function)
```bash
curl -X PUT localhost:8443/subscriptions/YOUR_SUBSCRIPTION_ID?api-version=2.0 --json '{"state":"Registered"}'
```

List the Operations for the Provider
```bash
curl -X GET "https://localhost:8443/providers/Microsoft.RedHatOpenshift/operations?api-version=2024-06-10-preview"
```

List HcpOpenShiftVersions Resources by Location

```bash
curl -X GET "https://localhost:8443/subscriptions/YOUR_SUBSCRIPTION_ID/locations/YOUR_LOCATION/providers/Microsoft.RedHatOpenshift/hcpOpenShiftVersions?api-version=2024-06-10-preview"
```

List HcpOpenShiftClusterResource Resources by Subscription ID
```bash
curl -X GET "https://localhost:8443/subscriptions/YOUR_SUBSCRIPTION_ID/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters?api-version=2024-06-10-preview"
```

Get a HcpOpenShiftClusterResource
```bash
curl -X GET "https://localhost:8443/subscriptions/YOUR_SUBSCRIPTION_ID/resourceGroups/YOUR_RESOURCE_GROUP_NAME/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/YOUR_CLUSTER_NAME?api-version=2024-06-10-preview"
```

Create or Update a HcpOpenShiftClusterResource
```bash
curl -X PUT "https://localhost:8443/subscriptions/YOUR_SUBSCRIPTION_ID/resourceGroups/YOUR_RESOURCE_GROUP_NAME/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/YOUR_CLUSTER_NAME?api-version=2024-06-10-preview" --json @cluster.json
```

Delete a HcpOpenShiftClusterResource
```bash
curl -X DELETE "https://localhost:8443/subscriptions/YOUR_SUBSCRIPTION_ID/resourceGroups/YOUR_RESOURCE_GROUP_NAME/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/YOUR_CLUSTER_NAME?api-version=2024-06-10-preview"
```

Execute deployment preflight checks
```bash
curl -X POST "https://localhost:8443/subscriptions/YOUR_SUBSCRIPTION_ID/resourceGroups/YOUR_RESOURCE_GROUP_NAME/providers/Microsoft.RedHatOpenshift/deployments/YOUR_DEPLOYMENT_NAME/preflight?api-version=2020-06-01" --json preflight.json
```
