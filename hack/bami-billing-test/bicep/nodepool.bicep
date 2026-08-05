// Parameterized node pool for BAMI billing/SKU testing. 

@description('The name of the Hypershift cluster to which the node pool will be attached.')
param clusterName string

@description('The name of the node pool')
param nodePoolName string

@description('The version of OpenShift to use for the node pool (e.g. 4.20.16)')
param nodePoolVersion string

@description('The VM SKU for the node pool (e.g. Standard_D8s_v3)')
param vmSize string = 'Standard_D8s_v3'

@description('Number of nodes in the node pool')
param replicas int = 2

@description('OS disk size in GiB')
param osDiskSizeGiB int = 64

@description('OS disk storage account type')
param osDiskStorageAccountType string = 'StandardSSD_LRS'

resource hcp 'Microsoft.RedHatOpenShift/hcpOpenShiftClusters@2025-12-23-preview' existing = {
  name: clusterName
}

resource nodepool 'Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nodePools@2025-12-23-preview' = {
  parent: hcp
  name: nodePoolName
  location: resourceGroup().location
  properties: {
    platform: {
      subnetId: hcp.properties.platform.subnetId
      vmSize: vmSize
      osDisk: {
        sizeGiB: osDiskSizeGiB
        diskStorageAccountType: osDiskStorageAccountType
      }
    }
    replicas: replicas
    version: {
      id: nodePoolVersion
    }
  }
}
