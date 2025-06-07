@description('Name of the hypershift cluster')
param clusterName string

@description('Location for the hypershift cluster')
param location string

resource hcp 'Microsoft.RedHatOpenShift/hcpOpenShiftClusters@2024-06-10-preview' existing = {
  name: clusterName
}

resource nodepool 'Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nodePools@2024-06-10-preview' = {
  name: '${clusterName}/${clusterName}-pool'
  // scope: hcp
  location: location
  properties: {
    version: {
      id: 'openshift-v4.18.1'
      channelGroup: 'stable'
    }
    platform: {
      subnetId: hcp.properties.platform.subnetId
      vmSize: 'Standard_D8s_v3'
      diskSizeGiB: 64
      diskStorageAccountType: 'StandardSSD_LRS'
    }
    replicas: 2
  }
}
