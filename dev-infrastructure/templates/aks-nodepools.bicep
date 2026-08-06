//
// User and infra node pools for an existing AKS cluster.
//
// This template is deployed as a dedicated ARMStack step (actionOnUnmanage: delete)
// so that node pools removed from the template are pruned automatically, without
// giving the stack prune rights over the rest of the cluster infrastructure
// (see AROSLSRE-1635). The system pool stays inline in aks-cluster-base.bicep
// because AKS requires it at cluster creation time.
//

import {
  csvToArray
  getLocationAvailabilityZonesCSV
} from '../modules/common.bicep'

@description('Azure Region Location')
param location string = resourceGroup().location

@description('Availability Zones to use for the infrastructure, as a CSV string. Defaults to all the zones of the location')
param locationAvailabilityZones string = getLocationAvailabilityZonesCSV(location)
var locationAvailabilityZoneList = csvToArray(locationAvailabilityZones)

@description('Name of the AKS cluster to attach the node pools to')
param aksClusterName string

@description('Resource ID of the subnet hosting the AKS nodes')
param nodeSubnetId string

@description('Resource ID of the subnet hosting the pods of the AKS cluster')
param podSubnetId string

@description('Name of the user agent pool')
param userAgentPoolName string

@description('Min replicas for the worker nodes')
param userAgentMinCount int

@description('Max replicas for the worker nodes')
param userAgentMaxCount int

@description('VM instance type for the worker nodes')
param userAgentVMSize string

@description('Number of pools to create for user nodes')
param userAgentPoolCount int

@description('Zones to use for the user nodes')
param userAgentPoolZones string

@description('Zone redundant mode for the user nodes')
param userZoneRedundantMode string

@description('Disk size for the AKS user nodes')
param userOsDiskSizeGB int

@description('Secondary NIC count for the user nodes')
param userSecondaryNicCount int = 0

@description('Name of the infra agent pool')
param infraAgentPoolName string

@description('Min replicas for the infra worker nodes')
param infraAgentMinCount int

@description('Max replicas for the infra worker nodes')
param infraAgentMaxCount int

@description('VM instance type for the infra worker nodes')
param infraAgentVMSize string

@description('Number of pools to create for infra nodes')
param infraAgentPoolCount int

@description('Zones to use for the infra nodes')
param infraAgentPoolZones string

@description('Zone redundant mode for the infra nodes')
param infraZoneRedundantMode string

@description('Disk size for the AKS infra nodes')
param infraOsDiskSizeGB int

@description('Whether to enable Swift V2 on the user node pools')
param enableSwiftV2Nodepools bool = false

@description('Maximum surge for AKS node pool upgrades')
param aksUpgradeSettingsMaxSurge string

@description('Maximum unavailable for AKS node pool upgrades')
param aksUpgradeSettingsMaxUnavailable string

module userAgentPools '../modules/aks/pool.bicep' = {
  name: 'user-agent-pools'
  params: {
    aksClusterName: aksClusterName
    poolBaseName: userAgentPoolName
    poolZones: length(csvToArray(userAgentPoolZones)) > 0
      ? csvToArray(userAgentPoolZones)
      : locationAvailabilityZoneList
    poolCount: userAgentPoolCount
    poolRole: 'worker'
    enableSwiftV2: enableSwiftV2Nodepools
    secondaryNicCount: userSecondaryNicCount
    minCount: userAgentMinCount
    maxCount: userAgentMaxCount
    vmSize: userAgentVMSize
    osDiskSizeGB: userOsDiskSizeGB
    vnetSubnetId: nodeSubnetId
    podSubnetId: podSubnetId
    zoneRedundantMode: userZoneRedundantMode
    upgradeSettingsMaxSurge: aksUpgradeSettingsMaxSurge
    upgradeSettingsMaxUnavailable: aksUpgradeSettingsMaxUnavailable
    maxPods: 225
  }
}

module infraAgentPools '../modules/aks/pool.bicep' = {
  name: 'infra-agent-pools'
  params: {
    aksClusterName: aksClusterName
    poolBaseName: infraAgentPoolName
    poolZones: length(csvToArray(infraAgentPoolZones)) > 0
      ? csvToArray(infraAgentPoolZones)
      : locationAvailabilityZoneList
    poolCount: infraAgentPoolCount
    poolRole: 'infra'
    enableSwiftV2: false
    secondaryNicCount: 0
    minCount: infraAgentMinCount
    maxCount: infraAgentMaxCount
    vmSize: infraAgentVMSize
    osDiskSizeGB: infraOsDiskSizeGB
    vnetSubnetId: nodeSubnetId
    podSubnetId: podSubnetId
    zoneRedundantMode: infraZoneRedundantMode
    upgradeSettingsMaxSurge: aksUpgradeSettingsMaxSurge
    upgradeSettingsMaxUnavailable: aksUpgradeSettingsMaxUnavailable
    maxPods: 225
    taints: [
      'infra=true:NoSchedule'
    ]
  }
}
