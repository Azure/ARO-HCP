@description('Azure Region Location')
param location string = resourceGroup().location

@description('Captures the bicep template that created it')
param createdByConfigTag string

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
    clusterType: 'mc'
  }
}

