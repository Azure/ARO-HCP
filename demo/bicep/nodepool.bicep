@description('The name of the Hypershift cluster to which the node pool will be attached.')
param clusterName string

@description('The name of the node pool')
param nodePoolName string

@description('Whether to enable autoscaling for the node pool')
@allowed([true, false])
param autoscale bool = false

@description('Number of replicas for static node pool (used when autoscale is false)')
param replica int = 2

@description('Minimum replica count for autoscale node pool (used when autoscale is true)')
param minReplica int = 1

@description('Maximum replica count for autoscale node pool (used when autoscale is true)')
param maxReplica int = 2

resource hcp 'Microsoft.RedHatOpenShift/hcpOpenShiftClusters@2024-06-10-preview' existing = {
  name: clusterName
}

resource nodepool 'Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nodePools@2024-06-10-preview' = {
  parent: hcp
  name: nodePoolName
  location: resourceGroup().location
  properties: {
    platform: {
      subnetId: hcp.properties.platform.subnetId
      vmSize: 'Standard_D8s_v3'
      osDisk: {
        sizeGiB: 64
        diskStorageAccountType: 'StandardSSD_LRS'
      }
    }
    // Conditional: use replicas for static, autoScaling for autoscale
    replicas: !autoscale ? replica : null
    autoScaling: autoscale ? {
      min: minReplica
      max: maxReplica
    } : null
  }
}
