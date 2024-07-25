@description('Azure Region Location')
param location string = resourceGroup().location

@description('Set to true to prevent resources from being pruned after 48 hours')
param persist bool = false

@description('Captures logged in users UID')
param currentUserId string

@description('AKS cluster name')
param aksClusterName string = 'aro-hcp-aks'

@description('Names of additional resource group contains ACRs the AKS cluster will get pull permissions on')
param additionalAcrResourceGroups array = [resourceGroup().name]

@description('Name of the resource group for the AKS nodes')
param aksNodeResourceGroupName string = '${resourceGroup().name}-aks1'

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

@description('The name of the keyvault for AKS.')
@maxLength(24)
param aksKeyVaultName string

@description('Manage soft delete setting for AKS etcd key-value store')
param aksEtcdKVEnableSoftDelete bool = true

@description('List of workload identities to create and their required values')
param workloadIdentities array

@description('Deploys a Maestro Consumer to the management cluster if set to true.')
param deployMaestroConsumer bool

@description('The domain to use to use for the maestro certificate. Relevant only for environments where OneCert can be used.')
param maestroCertDomain string

@description('The name of the keyvault for Maestro Eventgrid namespace certificates.')
@maxLength(24)
param maestroKeyVaultName string

@description('The name of the managed identity that will manage certificates in maestros keyvault.')
param maestroKeyVaultCertOfficerMSIName string = '${maestroKeyVaultName}-cert-officer-msi'

@description('The resourcegroups where the Maestro infrastructure is deployed.')
param maestroInfraResourceGroup string

@description('The name of the eventgrid namespace for Maestro.')
param maestroEventGridNamespacesName string

@description('This is a global DNS zone name that will be the parent of regional DNS zones to host ARO HCP customer cluster DNS records')
param baseDNSZoneName string = ''

@description('This is the region name in dev/staging/production')
var regionalDNSSubdomain = empty(currentUserId) ? location : '${location}-${take(uniqueString(currentUserId), 5)}'

@description('The resource group that hosts the regional zone')
param regionalZoneResourceGroup string

func isValidMaestroConsumerName(input string) bool => length(input) <= 90 && contains(input, '[^a-zA-Z0-9_-]') == false

module mgmtCluster '../modules/aks-cluster-base.bicep' = {
  name: 'aks_base_cluster'
  scope: resourceGroup()
  params: {
    location: location
    persist: persist
    currentUserId: currentUserId
    aksClusterName: aksClusterName
    aksNodeResourceGroupName: aksNodeResourceGroupName
    aksEtcdKVEnableSoftDelete: aksEtcdKVEnableSoftDelete
    enablePrivateCluster: enablePrivateCluster
    deployIstio: false
    kubernetesVersion: kubernetesVersion
    vnetAddressPrefix: vnetAddressPrefix
    subnetPrefix: subnetPrefix
    podSubnetPrefix: podSubnetPrefix
    clusterType: 'mgmt-cluster'
    workloadIdentities: workloadIdentities
    aksKeyVaultName: aksKeyVaultName
    deployUserAgentPool: true
    additionalAcrResourceGroups: additionalAcrResourceGroups
    userAgentMinCount: 3
    userAgentMaxCount: 9
  }
}

output aksClusterName string = mgmtCluster.outputs.aksClusterName

//
//   M A E S T R O
//

module maestroConsumer '../modules/maestro/maestro-consumer.bicep' = if (deployMaestroConsumer && maestroInfraResourceGroup != '') {
  name: 'maestro-consumer-${uniqueString(resourceGroup().name)}'
  scope: resourceGroup()
  params: {
    maestroServerManagedIdentityPrincipalId: filter(
      mgmtCluster.outputs.userAssignedIdentities,
      id => id.uamiName == 'maestro-consumer'
    )[0].uamiPrincipalID
    maestroInfraResourceGroup: maestroInfraResourceGroup
    maestroConsumerName: isValidMaestroConsumerName(resourceGroup().name) ? resourceGroup().name : ''
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
  id => id.uamiName == 'external-dns'
)[0].uamiPrincipalID

module dnsZoneContributor '../modules/dns/zone-contributor.bicep' = {
  name: guid(regionalDNSSubdomain, mgmtCluster.name, 'external-dns')
  scope: resourceGroup(regionalZoneResourceGroup)
  params: {
    zoneName: '${regionalDNSSubdomain}.${baseDNSZoneName}'
    zoneContributerManagedIdentityPrincipalId: externalDnsManagedIdentityPrincipalId
  }
}
