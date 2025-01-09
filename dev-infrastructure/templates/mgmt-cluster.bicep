@description('Azure Region Location')
param location string = resourceGroup().location

@description('Set to true to prevent resources from being pruned after 48 hours')
param persist bool = false

@description('AKS cluster name')
param aksClusterName string = 'aro-hcp-aks'

@description('Disk size for the AKS system nodes')
param aksSystemOsDiskSizeGB int

@description('Disk size for the AKS user nodes')
param aksUserOsDiskSizeGB int

@description('Names of additional resource group contains ACRs the AKS cluster will get pull permissions on')
param acrPullResourceGroups array = []

@description('Name of the resource group for the AKS nodes')
param aksNodeResourceGroupName string = '${resourceGroup().name}-aks1'

@description('VNET address prefix')
param vnetAddressPrefix string

@description('Min replicas for the worker nodes')
param userAgentMinCount int = 1

@description('Max replicas for the worker nodes')
param userAgentMaxCount int = 3

@description('VM instance type for the worker nodes')
param userAgentVMSize string = 'Standard_D2s_v3'

@description('Availability Zone count for worker nodes')
param userAgentPoolAZCount int = 3

@description('Min replicas for the system nodes')
param systemAgentMinCount int = 2

@description('Max replicas for the system nodes')
param systemAgentMaxCount int = 3

@description('VM instance type for the system nodes')
param systemAgentVMSize string = 'Standard_D2s_v3'

@description('Subnet address prefix')
param subnetPrefix string

@description('Specifies the address prefix of the subnet hosting the pods of the AKS cluster.')
param podSubnetPrefix string

@description('Kuberentes version to use with AKS')
param kubernetesVersion string

@description('The name of the keyvault for AKS.')
@maxLength(24)
param aksKeyVaultName string

@description('Manage soft delete setting for AKS etcd key-value store')
param aksEtcdKVEnableSoftDelete bool = true

@description('The name of the maestro consumer.')
param maestroConsumerName string

@description('The domain to use to use for the maestro certificate. Relevant only for environments where OneCert can be used.')
param maestroCertDomain string

@description('The name of the eventgrid namespace for Maestro.')
param maestroEventGridNamespacesName string

@description('The resource group that hosts the regional zone')
param regionalResourceGroup string

@description('The name of the CX KeyVault')
param cxKeyVaultName string

@description('The name of the MSI KeyVault')
param msiKeyVaultName string

@description('The name of the MGMT KeyVault')
param mgmtKeyVaultName string

@description('MSI that will be used to run deploymentScripts')
param aroDevopsMsiId string

@description('The name of the Azure Monitor Workspace (stores prometheus metrics)')
param azureMonitorWorkspaceName string

module mgmtCluster '../modules/aks-cluster-base.bicep' = {
  name: 'cluster'
  scope: resourceGroup()
  params: {
    location: location
    persist: persist
    aksClusterName: aksClusterName
    aksNodeResourceGroupName: aksNodeResourceGroupName
    aksEtcdKVEnableSoftDelete: aksEtcdKVEnableSoftDelete
    deployIstio: false
    kubernetesVersion: kubernetesVersion
    vnetAddressPrefix: vnetAddressPrefix
    subnetPrefix: subnetPrefix
    podSubnetPrefix: podSubnetPrefix
    clusterType: 'mgmt-cluster'
    workloadIdentities: items({
      maestro_wi: {
        uamiName: 'maestro-consumer'
        namespace: 'maestro'
        serviceAccountName: 'maestro'
      }
      package_operator: {
        uamiName: 'package-operator'
        namespace: 'package-operator-system'
        serviceAccountName: 'package-operator'
      }
    })
    aksKeyVaultName: aksKeyVaultName
    acrPullResourceGroups: acrPullResourceGroups
    userAgentMinCount: userAgentMinCount
    userAgentPoolAZCount: userAgentPoolAZCount
    userAgentMaxCount: userAgentMaxCount
    userAgentVMSize: userAgentVMSize
    systemAgentMinCount: systemAgentMinCount
    systemAgentMaxCount: systemAgentMaxCount
    systemAgentVMSize: systemAgentVMSize
    systemOsDiskSizeGB: aksSystemOsDiskSizeGB
    userOsDiskSizeGB: aksUserOsDiskSizeGB
    aroDevopsMsiId: aroDevopsMsiId
    dcrId: dataCollection.outputs.dcrId
  }
}

output aksClusterName string = mgmtCluster.outputs.aksClusterName

//
// M E T R I C S
//

module dataCollection '../modules/metrics/datacollection.bicep' = {
  name: '${resourceGroup().name}-aksClusterName'
  params: {
    azureMonitorWorkspaceLocation: location
    azureMonitorWorkspaceName: azureMonitorWorkspaceName
    regionalResourceGroup: regionalResourceGroup
    aksClusterName: aksClusterName
  }
}

//
// K E Y V A U L T S
//

module cxCSIKeyVaultAccess '../modules/keyvault/keyvault-secret-access.bicep' = [
  for role in [
    'Key Vault Secrets Officer'
    'Key Vault Certificate User'
    'Key Vault Certificates Officer'
  ]: {
    name: guid(cxKeyVaultName, 'aks-kv-csi-mi', role)
    params: {
      keyVaultName: cxKeyVaultName
      roleName: role
      managedIdentityPrincipalId: mgmtCluster.outputs.aksClusterKeyVaultSecretsProviderPrincipalId
    }
  }
]

module msiCSIKeyVaultAccess '../modules/keyvault/keyvault-secret-access.bicep' = [
  for role in [
    'Key Vault Secrets Officer'
    'Key Vault Certificate User'
    'Key Vault Certificates Officer'
  ]: {
    name: guid(msiKeyVaultName, 'aks-kv-csi-mi', role)
    params: {
      keyVaultName: msiKeyVaultName
      roleName: role
      managedIdentityPrincipalId: mgmtCluster.outputs.aksClusterKeyVaultSecretsProviderPrincipalId
    }
  }
]

resource mgmtKeyVault 'Microsoft.KeyVault/vaults@2024-04-01-preview' existing = {
  name: mgmtKeyVaultName
}

//
//   M A E S T R O
//

module maestroConsumer '../modules/maestro/maestro-consumer.bicep' = {
  name: 'maestro-consumer'
  params: {
    maestroAgentManagedIdentityPrincipalId: filter(
      mgmtCluster.outputs.userAssignedIdentities,
      id => id.uamiName == 'maestro-consumer'
    )[0].uamiPrincipalID
    maestroInfraResourceGroup: regionalResourceGroup
    maestroConsumerName: maestroConsumerName
    maestroEventGridNamespaceName: maestroEventGridNamespacesName
    certKeyVaultName: mgmtKeyVaultName
    keyVaultOfficerManagedIdentityName: aroDevopsMsiId
    maestroCertificateDomain: maestroCertDomain
  }
  dependsOn: [
    mgmtKeyVault
  ]
}

//
//  E V E N T   G R I D   P R I V A T E   E N D P O I N T   C O N N E C T I O N
//

resource eventGridNamespace 'Microsoft.EventGrid/namespaces@2024-06-01-preview' existing = {
  name: maestroEventGridNamespacesName
  scope: resourceGroup(regionalResourceGroup)
}

module eventGrindPrivateEndpoint '../modules/private-endpoint.bicep' = {
  name: 'eventGridPrivateEndpoint'
  params: {
    location: location
    subnetIds: [mgmtCluster.outputs.aksNodeSubnetId]
    privateLinkServiceId: eventGridNamespace.id
    vnetId: mgmtCluster.outputs.aksVnetId
    serviceType: 'eventgrid'
    groupId: 'topicspace'
  }
}
