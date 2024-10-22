@description('Azure Region Location')
param location string = resourceGroup().location

@description('Set to true to prevent resources from being pruned after 48 hours')
param persist bool = false

@description('Captures logged in users UID')
param currentUserId string

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

@description('This is a global DNS zone name that will be the parent of regional DNS zones to host ARO HCP customer cluster DNS records')
param baseDNSZoneName string = ''

@description('This is the region name in dev/staging/production')
param regionalDNSSubdomain string = empty(currentUserId)
  ? location
  : '${location}-${take(uniqueString(currentUserId), 5)}'

@description('The resource group that hosts the regional zone')
param regionalResourceGroup string

func isValidMaestroConsumerName(input string) bool => length(input) <= 90 && contains(input, '[^a-zA-Z0-9_-]') == false

// Tags the resource group
resource subscriptionTags 'Microsoft.Resources/tags@2024-03-01' = {
  name: 'default'
  scope: resourceGroup()
  properties: {
    tags: {
      persist: toLower(string(persist))
      deployedBy: currentUserId
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
        uamiName: 'external-dns'
        namespace: 'hypershift'
        serviceAccountName: 'external-dns'
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
//  E X T E R N A L   D N S
//

var externalDnsManagedIdentityPrincipalId = filter(
  mgmtCluster.outputs.userAssignedIdentities,
  id => id.uamiName == 'external-dns'
)[0].uamiPrincipalID

module dnsZoneContributor '../modules/dns/zone-contributor.bicep' = {
  name: guid(regionalDNSSubdomain, mgmtCluster.name, 'external-dns')
  scope: resourceGroup(regionalResourceGroup)
  params: {
    zoneName: '${regionalDNSSubdomain}.${baseDNSZoneName}'
    zoneContributerManagedIdentityPrincipalId: externalDnsManagedIdentityPrincipalId
  }
}
