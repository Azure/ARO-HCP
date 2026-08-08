@description('The name of the Hypershift cluster to which the node pool will be attached.')
param clusterName string

@description('The name of the node pool')
param nodePoolName string

@description('Number of replicas in the node pool')
param replicas int = 2

@description('OpenShift Version ID to use')
param openshiftVersionId string
@description('Size of the osDisk for the node pool in GiB')
param osDiskSizeGiB int = 64

@description('VM size for the nodepool VMs')
param vmSize string = 'Standard_D8s_v3'

resource hcp 'Microsoft.RedHatOpenShift/hcpOpenShiftClusters@2024-06-10-preview' existing = {
  name: clusterName
}

resource nodepool 'Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nodePools@2024-06-10-preview' = {
  parent: hcp
  name: nodePoolName
  location: resourceGroup().location
  properties: {
    version: {
      id: openshiftVersionId
      channelGroup: 'stable'
    }
    platform: {
      subnetId: hcp.properties.platform.subnetId
      vmSize: vmSize
      osDisk: {
        sizeGiB: osDiskSizeGiB
        diskStorageAccountType: 'StandardSSD_LRS'
      }
    }
    replicas: replicas
  }
}
