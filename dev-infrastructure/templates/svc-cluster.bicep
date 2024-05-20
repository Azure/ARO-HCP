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

@description('Istio control plane version to use with AKS')
param istioVersion string

// TODO: When the work around workload identity for the RP is finalized, change this to true
@description('disableLocalAuth for the ARO HCP RP CosmosDB')
param disableLocalAuth bool

@description('Deploy ARO HCP RP Azure Cosmos DB if true')
param deployFrontendCosmos bool

@description('List of workload identities to create and their required values')
param workloadIdentities array

@description('Deploy ARO HCP Maestro Infrastructure if true')
param deployMaestroInfra bool

@description('The namespace where the maestro resources will be deployed.')
param maestroNamespace string

@description('The OneCertV2 domain to use to use for the maestro certificate.')
param maestroCertDomain string?

module svcCluster '../modules/aks-cluster-base.bicep' = {
  name: 'svc-cluster'
  scope: resourceGroup()
  params: {
    location: location
    persist: persist
    currentUserId: currentUserId
    enablePrivateCluster: enablePrivateCluster
    kubernetesVersion: kubernetesVersion
    istioVersion: istioVersion
    vnetAddressPrefix: vnetAddressPrefix
    subnetPrefix: subnetPrefix
    podSubnetPrefix: podSubnetPrefix
    clusterType: 'svc-cluster'
    workloadIdentities: workloadIdentities
  }
}
var frontendMI = filter(svcCluster.outputs.userAssignedIdentities, id => id.uamiName == 'frontend')[0]

module rpCosmosDb '../modules/rp-cosmos.bicep' = if (deployFrontendCosmos) {
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

//
//   M A E S T R O
//

module maestroConfig '../modules/maestro/maestro-config.bicep' = {
  name: 'maestro-config'
  params: {
    location: location
    resourceGroupName: resourceGroup().name
    certificateDomain: maestroCertDomain
  }
}

module maestroInfra '../modules/maestro/maestro-infra.bicep' = if (deployMaestroInfra) {
  name: 'maestro-infra'
  params: {
    eventGridNamespaceName: maestroConfig.outputs.maestroEventGridNamespaceName
    location: location
    currentUserId: currentUserId
    maestroKeyVaultName: maestroConfig.outputs.maestroKeyVaultName
    kvCertOfficerManagedIdentityName: maestroConfig.outputs.kvCertOfficerManagedIdentityName
  }
}

module maestroServer '../modules/maestro/maestro-server.bicep' = if (deployMaestroInfra) {
  name: 'maestro-server'
  params: {
    aksClusterName: svcCluster.outputs.aksClusterName
    maestroServerManagedIdentityPrincipalId: filter(
      svcCluster.outputs.userAssignedIdentities,
      id => id.uamiName == 'maestro-server'
    )[0].uamiPrincipalID
    maestroServerManagedIdentityClientId: filter(
      svcCluster.outputs.userAssignedIdentities,
      id => id.uamiName == 'maestro-server'
    )[0].uamiClientID
    namespace: maestroNamespace
    maestroInfraResourceGroup: resourceGroup().name
    maestroEventGridNamespaceName: maestroConfig.outputs.maestroEventGridNamespaceName
    maestroKeyVaultName: maestroConfig.outputs.maestroKeyVaultName
    maestroKeyVaultOfficerManagedIdentityName: maestroConfig.outputs.kvCertOfficerManagedIdentityName
    maestroKeyVaultCertificateDomain: maestroConfig.outputs.maestroCertificateDomain
    location: location
  }
  dependsOn: [
    maestroInfra
  ]
}
