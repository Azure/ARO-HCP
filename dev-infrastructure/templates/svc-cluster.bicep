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

module svcCluster '../modules/aks-cluster-base.bicep' = {
  name: 'aks_base_cluster'
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
  }
}

module rpCosmosDb '../modules/rp-cosmos.bicep' =
  if (deployFrontendCosmos) {
    name: 'rp_cosmos_db'
    scope: resourceGroup()
    params: {
      location: location
      aksNodeSubnetId: svcCluster.outputs.aksNodeSubnetId
      vnetId: svcCluster.outputs.aksVnetId
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
    issuer: svcCluster.outputs.aksOidcIssuerUrl
    subject: 'system:serviceaccount:aro-hcp:frontend'
  }
}

output frontend_mi_client_id string = frontend_mi.properties.clientId
