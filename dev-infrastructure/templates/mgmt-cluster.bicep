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

@description('List of workload identities to create and their required values')
param workloadIdentities array

@description('Deploys a Maestro Consumer to the management cluster if set to true.')
param deployMaestroConsumer bool

@description('Namespace to deploy the Maestro Consumer to.')
param maestroNamespace string

@description('The OneCertV2 domain to use to use for the Maestro certificate.')
param maestroCertDomain string?

@description('The resourcegroups where the Maestro infrastructure is deployed.')
param maestroInfraResourceGroup string

module mgmtCluster '../modules/aks-cluster-base.bicep' = {
  name: 'aks_base_cluster'
  scope: resourceGroup()
  params: {
    location: location
    persist: persist
    currentUserId: currentUserId
    enablePrivateCluster: enablePrivateCluster
    istioVersion: istioVersion
    kubernetesVersion: kubernetesVersion
    vnetAddressPrefix: vnetAddressPrefix
    subnetPrefix: subnetPrefix
    podSubnetPrefix: podSubnetPrefix
    clusterType: 'mgmt-cluster'
    workloadIdentities: workloadIdentities
  }
}

//
//   M A E S T R O
//

module maestroConfig '../modules/maestro/maestro-config.bicep' = {
  name: 'maestro-config'
  params: {
    location: location
    resourceGroupName: maestroInfraResourceGroup
    certificateDomain: maestroCertDomain
  }
}

module maestroConsumer '../modules/maestro/maestro-consumer.bicep' = if (deployMaestroConsumer && maestroInfraResourceGroup != '') {
  name: 'maestro-consumer'
  scope: resourceGroup()
  params: {
    aksClusterName: mgmtCluster.outputs.aksClusterName
    maestroServerManagedIdentityPrincipalId: filter(
      mgmtCluster.outputs.userAssignedIdentities,
      id => id.uamiName == 'maestro-consumer'
    )[0].uamiPrincipalID
    maestroServerManagedIdentityClientId: filter(
      mgmtCluster.outputs.userAssignedIdentities,
      id => id.uamiName == 'maestro-consumer'
    )[0].uamiClientID
    namespace: maestroNamespace
    maestroInfraResourceGroup: maestroInfraResourceGroup
    maestroConsumerName: mgmtCluster.outputs.aksClusterName
    maestroEventGridNamespaceName: maestroConfig.outputs.maestroEventGridNamespaceName
    maestroKeyVaultName: maestroConfig.outputs.maestroKeyVaultName
    maestroKeyVaultOfficerManagedIdentityName: maestroConfig.outputs.kvCertOfficerManagedIdentityName
    maestroKeyVaultCertificateDomain: maestroConfig.outputs.maestroCertificateDomain
    location: location
  }
}
