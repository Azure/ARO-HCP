# Azure CLI Arohcp Extension #

This is an Azure CLI extension to manage ARO HCP (Azure Red Hat OpenShift Hosted Control Planes) resources.

## Install ##

```bash
az extension add --source <path-to-whl-or-extension-dir>
```

## How to use ##

Run `az arohcp -h` to see all available commands.

### HCP OpenShift Clusters ###

```bash
# List clusters in a resource group
az arohcp hcp-open-shift-cluster list --resource-group <resourceGroup>

# Show a cluster
az arohcp hcp-open-shift-cluster show --resource-group <resourceGroup> --hcp-open-shift-cluster-name <clusterName>

# Create a cluster
az arohcp hcp-open-shift-cluster create \
  --resource-group <resourceGroup> \
  --hcp-open-shift-cluster-name <clusterName> \
  --location <location> \
  --version "{channel-group:stable,id:4.17}" \
  --dns "{base-domain-prefix:<prefix>}" \
  --network "{network-type:OVNKubernetes,pod-cidr:10.128.0.0/14,service-cidr:172.30.0.0/16,machine-cidr:10.0.0.0/16,host-prefix:23}" \
  --api "{visibility:Public}" \
  --platform "{managed-resource-group:<managedResourceGroup>,subnet-id:/subscriptions/<subscriptionId>/resourceGroups/<resourceGroup>/providers/Microsoft.Network/virtualNetworks/<vnetName>/subnets/<subnetName>,outbound-type:LoadBalancer,network-security-group-id:/subscriptions/<subscriptionId>/resourceGroups/<resourceGroup>/providers/Microsoft.Network/networkSecurityGroups/<nsgName>,operators-authentication:{user-assigned-identities:{control-plane-operators:{},data-plane-operators:{},service-managed-identity:/subscriptions/<subscriptionId>/resourceGroups/<identityResourceGroup>/providers/Microsoft.ManagedIdentity/userAssignedIdentities/<identityName>}}}"

# Delete a cluster
az arohcp hcp-open-shift-cluster delete --resource-group <resourceGroup> --hcp-open-shift-cluster-name <clusterName>
```

### Node Pools ###

```bash
# List node pools in a cluster
az arohcp hcp-open-shift-cluster node-pool list --resource-group <resourceGroup> --hcp-open-shift-cluster-name <clusterName>

# Show a node pool
az arohcp hcp-open-shift-cluster node-pool show --resource-group <resourceGroup> --hcp-open-shift-cluster-name <clusterName> --node-pool-name <nodePoolName>

# Create a node pool
az arohcp hcp-open-shift-cluster node-pool create \
  --resource-group <resourceGroup> \
  --hcp-open-shift-cluster-name <clusterName> \
  --node-pool-name <nodePoolName> \
  --location <location>
```

### HCP OpenShift Versions ###

```bash
# List available versions in a location
az arohcp hcp-open-shift-version list --location <location>

# Show a specific version
az arohcp hcp-open-shift-version show --location <location> --version-name <versionName>
```