# ARO-HCP-FRONTEND

## Build the frontend container
docker build -f Dockerfile.frontend -t aro-hcp-frontend .

## Run the frontend container
docker run -p 8443:8443 aro-hcp-frontend

## Run the frontend

```bash
make frontend
```

To create a cluster, follow the instructions in [development-setup.md][../dev-infrastructure/docs/development-setup.md]

## Deploy/Undeploy frontend in a cluster

```bash
# Deploy
make deploy-frontend

# Undeploy
make undeploy-frontend
```

## Available endpoints

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
curl -X PUT "https://localhost:8443/subscriptions/YOUR_SUBSCRIPTION_ID/resourceGroups/YOUR_RESOURCE_GROUP_NAME/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/YOUR_CLUSTER_NAME?api-version=2024-06-10-preview" --json @mycluster.json
```

Delete a HcpOpenShiftClusterResource
```bash
curl -X DELETE "https://localhost:8443/subscriptions/YOUR_SUBSCRIPTION_ID/resourceGroups/YOUR_RESOURCE_GROUP_NAME/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/YOUR_CLUSTER_NAME?api-version=2024-06-10-preview"
```


Update a subscription state
```bash
curl -X PUT localhost:8443/subscriptions/YOUR_SUBSCRIPTION_ID?api-version=2.0 --json '{"state":"Registered"}'
```

Execute deployment preflight checks
```bash
curl -X POST "https://localhost:8443/subscriptions/YOUR_SUBSCRIPTION_ID/resourceGroups/YOUR_RESOURCE_GROUP_NAME/providers/Microsoft.RedHatOpenshift/deployments/YOUR_DEPLOYMENT_NAME/preflight?api-version=2020-06-01" --json preflight.json
```
