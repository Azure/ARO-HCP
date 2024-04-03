# ARO-HCP-FRONTEND

## Run the frontend

```bash
make frontend
```

To create a cluster, follow the instructions in [development-setup.md][../dev-infrastructure/docs/development-setup.md]

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
curl -X PUT "https://localhost:8443/subscriptions/YOUR_SUBSCRIPTION_ID/resourceGroups/YOUR_RESOURCE_GROUP_NAME/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/YOUR_CLUSTER_NAME?api-version=2024-06-10-preview" -H "Content-Type: application/json" -d @mycluster.yaml
```

Delete a HcpOpenShiftClusterResource
```bash
curl -X DELETE "https://localhost:8443/subscriptions/YOUR_SUBSCRIPTION_ID/resourceGroups/YOUR_RESOURCE_GROUP_NAME/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/YOUR_CLUSTER_NAME?api-version=2024-06-10-preview"
```