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

@description('The name of the hypershift namespace.')
param hypershiftNamespace string

@description('The name of the external DNS managed identity.')
param externalDNSManagedIdentityName string

@description('The name of the external DNS service account.')
param externalDNSServiceAccountName string

@description('The name of the maestro consumer.')
param maestroConsumerName string

@description('The domain to use to use for the maestro certificate. Relevant only for environments where OneCert can be used.')
param maestroCertDomain string

@description('The name of the keyvault for Maestro Eventgrid namespace certificates.')
param maestroKeyVaultName string

@description('The name of the managed identity that will manage certificates in maestros keyvault.')
param maestroKeyVaultCertOfficerMSIName string = '${maestroKeyVaultName}-cert-officer-msi'

@description('The name of the eventgrid namespace for Maestro.')
param maestroEventGridNamespacesName string

@description('This is a regional DNS zone')
param regionalDNSZoneName string

@description('The resource group that hosts the regional zone')
param regionalResourceGroup string

@description('The name of the CX KeyVault')
param cxKeyVaultName string

@description('Defines if the CX KeyVault is private')
param cxKeyVaultPrivate bool

@description('Defines if the CX KeyVault has soft delete enabled')
param cxKeyVaultSoftDelete bool

@description('The name of the MSI KeyVault')
param msiKeyVaultName string

@description('Defines if the MSI KeyVault is private')
param msiKeyVaultPrivate bool

@description('Defines if the MSI KeyVault has soft delete enabled')
param msiKeyVaultSoftDelete bool

@description('The name of the MGMT KeyVault')
param mgmtKeyVaultName string

@description('Defines if the MGMT KeyVault is private')
param mgmtKeyVaultPrivate bool

@description('Defines if the MGMT KeyVault has soft delete enabled')
param mgmtKeyVaultSoftDelete bool

// Tags the resource group
resource subscriptionTags 'Microsoft.Resources/tags@2024-03-01' = {
  name: 'default'
  scope: resourceGroup()
  properties: {
    tags: {
      persist: toLower(string(persist))
    }
  }
}

module mgmtCluster '../modules/aks-cluster-base.bicep' = {
  name: 'mgmt-cluster'
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
      external_dns_wi: {
        uamiName: externalDNSManagedIdentityName
        namespace: hypershiftNamespace
        serviceAccountName: externalDNSServiceAccountName
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
  }
}

output aksClusterName string = mgmtCluster.outputs.aksClusterName

//
//   M A E S T R O
//

module maestroConsumer '../modules/maestro/maestro-consumer.bicep' = {
  name: 'maestro-consumer'
  params: {
    maestroServerManagedIdentityPrincipalId: filter(
      mgmtCluster.outputs.userAssignedIdentities,
      id => id.uamiName == 'maestro-consumer'
    )[0].uamiPrincipalID
    maestroInfraResourceGroup: regionalResourceGroup
    maestroConsumerName: maestroConsumerName
    maestroEventGridNamespaceName: maestroEventGridNamespacesName
    maestroKeyVaultName: maestroKeyVaultName
    maestroKeyVaultOfficerManagedIdentityName: maestroKeyVaultCertOfficerMSIName
    maestroKeyVaultCertificateDomain: maestroCertDomain
    location: location
  }
}

//
//  E X T E R N A L   D N S
//

var externalDnsManagedIdentityPrincipalId = filter(
  mgmtCluster.outputs.userAssignedIdentities,
  id => id.uamiName == externalDNSManagedIdentityName
)[0].uamiPrincipalID

module dnsZoneContributor '../modules/dns/zone-contributor.bicep' = {
  name: guid(regionalDNSZoneName, mgmtCluster.name, externalDNSManagedIdentityName)
  scope: resourceGroup(regionalResourceGroup)
  params: {
    zoneName: regionalDNSZoneName
    zoneContributerManagedIdentityPrincipalId: externalDnsManagedIdentityPrincipalId
  }
}

//
//   K E Y V A U L T S
//

module cxKeyVault '../modules/keyvault/keyvault.bicep' = {
  name: '${deployment().name}-cx-kv'
  params: {
    location: location
    keyVaultName: cxKeyVaultName
    private: cxKeyVaultPrivate
    enableSoftDelete: cxKeyVaultSoftDelete
    purpose: 'cx'
  }
}

module msiKeyVault '../modules/keyvault/keyvault.bicep' = {
  name: '${deployment().name}-msi-kv'
  params: {
    location: location
    keyVaultName: msiKeyVaultName
    private: msiKeyVaultPrivate
    enableSoftDelete: msiKeyVaultSoftDelete
    purpose: 'msi'
  }
}

module mgmtKeyVault '../modules/keyvault/keyvault.bicep' = {
  name: '${deployment().name}-mgmt-kv'
  params: {
    location: location
    keyVaultName: mgmtKeyVaultName
    private: mgmtKeyVaultPrivate
    enableSoftDelete: mgmtKeyVaultSoftDelete
    purpose: 'mgmt'
  }
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
