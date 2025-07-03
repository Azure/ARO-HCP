@description('The name of the Hypershift cluster to which the node pool will be attached.')
param clusterName string

@description('The name of the node pool')
param nodePoolName string

resource hcp 'Microsoft.RedHatOpenShift/hcpOpenShiftClusters@2024-06-10-preview' existing = {
  name: clusterName
}

resource nodepool 'Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nodePools@2024-06-10-preview' = {
  parent: hcp
  name: nodePoolName
  location: resourceGroup().location
  properties: {
    version: {
      id: 'openshift-v4.18.1'
      channelGroup: 'stable'
    }
    platform: {
      subnetId: hcp.properties.platform.subnetId
      vmSize: 'Standard_D8s_v3'
      osDisk: {
        diskSizeGiB: 64
        diskStorageAccountType: 'StandardSSD_LRS'
      } 
    }
    replicas: 2
  }
}
