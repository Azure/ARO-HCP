@description('Azure Region Location')
param location string = resourceGroup().location

@description('Set to true to prevent resources from being pruned after 48 hours')
param persist bool = false

@description('Captures logged in users UID')
param currentUserId string

@description('VNET address prefix')
param vnetAddressPrefix string

@description('Subnet address prefix')
param subnetPrefix string

@description('Specifies the address prefix of the subnet hosting the pods of the AKS cluster.')
param podSubnetPrefix string

@description('(Optional) boolean flag to configure public/private AKS Cluster')
param enablePrivateCluster bool

@description('Kuberentes version to use with AKS')
param kubernetesVersion string

// TODO: When the work around workload identity for the RP is finalized, change this to true
@description('disableLocalAuth for the ARO HCP RP CosmosDB')
param disableLocalAuth bool

@description('Deploy ARO HCP RP Azure Cosmos DB if true')
param deployFrontendCosmos bool

@description('List of workload identities to create and their required values')
param workloadIdentities array

module svcCluster '../modules/aks-cluster-base.bicep' = {
  name: 'svc-cluster'
  scope: resourceGroup()
  params: {
    location: location
    persist: persist
    currentUserId: currentUserId
    enablePrivateCluster: enablePrivateCluster
    kubernetesVersion: kubernetesVersion
    vnetAddressPrefix: vnetAddressPrefix
    subnetPrefix: subnetPrefix
    podSubnetPrefix: podSubnetPrefix
    clusterType: 'svc'
    workloadIdentities: workloadIdentities
  }
}
var frontendMI = filter(svcCluster.outputs.userAssignedIdentities, id => id.uamiName == 'frontend')[0]

module rpCosmosDb '../modules/rp-cosmos.bicep' = 
if (deployFrontendCosmos) {
  name: 'rp_cosmos_db'
  scope: resourceGroup()  
  params: {
    location: location
    aksNodeSubnetId: svcCluster.outputs.aksNodeSubnetId
    vnetId: svcCluster.outputs.aksVnetId
    disableLocalAuth: disableLocalAuth
    userAssignedMI: frontendMI.uamiID
    uamiPrincipalId: frontendMI.uamiPrincipalID
  }
}

output frontend_mi_client_id string = frontendMI.uamiClientID
