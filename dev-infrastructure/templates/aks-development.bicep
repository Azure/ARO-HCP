@description('Current user id')
param currentUserId string

param location string = resourceGroup().location
param createdByConfigTag string = 'None'

@description('VNET address prefix')
param vnetAddressPrefix string = '10.128.0.0/14'

@description('Subnet address prefix')
param subnetPrefix string = '10.128.8.0/21'

@description('Specifies the address prefix of the subnet hosting the pods of the AKS cluster.')
param podSubnetPrefix string = '10.128.64.0/18'

@description('(Optional) boolean flag to configure public/private AKS Cluster')
param enablePrivateCluster bool = true

@description('The version of Kubernetes.')
param kubernetesVersion string

@description('Cluster type based on its use (dev, service, or management cluster)')
@allowed(['dev', 'svc', 'mc'])
param clusterType string

@description('Deploy ARO HCP RP Azure Cosmos DB if true')
param deployFrontendCosmos bool = true

// TODO: When the work around workload identity for the RP is finalized, change this to true
@description('disableLocalAuth for the ARO HCP RP CosmosDB')
param disableLocalAuth bool = false

module aksBaseCluster '../modules/aks-cluster-base.bicep' = {
  name: 'aks_base_cluster'
  scope: resourceGroup()  
  params: {
    location: location
    createdByConfigTag: createdByConfigTag
    currentUserId: currentUserId
    enablePrivateCluster: enablePrivateCluster
    kubernetesVersion: kubernetesVersion
    vnetAddressPrefix: vnetAddressPrefix
    subnetPrefix: subnetPrefix
    podSubnetPrefix: podSubnetPrefix
    clusterType: clusterType
  }
}

module rpCosmosDb '../modules/rp-cosmos.bicep' = 
if (deployFrontendCosmos) {
  name: 'rp_cosmos_db'
  scope: resourceGroup()  
  params: {
    location: location
    aksNodeSubnetId: aksBaseCluster.outputs.aksNodeSubnetId
    vnetId: aksBaseCluster.outputs.aksVnetId
    disableLocalAuth: disableLocalAuth
    userAssignedMI: frontend_mi.id
    uamiPrincipalId: frontend_mi.properties.principalId
  }
}

resource frontend_mi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  location: location
  name: 'frontend-${location}'
}

resource frontend_mi_fedcred 'Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials@2023-01-31' = {
  name: 'frontend-${location}-fedcred'
  parent: frontend_mi
  properties: {
    audiences: [
      'api://AzureADTokenExchange'
    ]
    issuer: aksBaseCluster.outputs.aksOidcIssuerUrl
    subject: 'system:serviceaccount:aro-hcp:frontend'
  }
}

output frontend_mi_client_id string = frontend_mi.properties.clientId
