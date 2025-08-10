param aksClusterName string
param poolBaseName string

param poolZones array
param poolCount int

param poolRole string
param enableSwitftV2 bool

param vmSize string
param minCount int
param maxCount int
param osDiskSizeGB int

param vnetSubnetId string
param podSubnetId string

param maxPods int

param taints array = []

type Pool = {
  name: string
  zones: array
}

// Helper functions for pool naming
func getZonalPoolName(poolType string, zone string) string => '${poolType}${zone}'

func getNonZonalPoolName(poolType string, counter int) string => '${poolType}nz${counter}'

func isZonalPool(poolName string) bool => !contains(poolName, 'nz')

// Helper functions for calculating pool counts
func getZonalPoolCount(availableZones array, requiredPools int) int =>
  min(length(availableZones), requiredPools)

func getNonZonalPoolCount(availableZones array, requiredPools int) int =>
  max(0, requiredPools - length(availableZones))


//
//   P O O L   S T R A T E G Y
//

// Implementation of AKSZoneStrategy using helper functions
// Creates pool configurations for user and infra pools based on available zones

var zonalPools = [
  for i in range(0, getZonalPoolCount(poolZones, poolCount)): {
    name: getZonalPoolName(poolBaseName, poolZones[i])
    zones: [poolZones[i]]
  }
]

var nonZonalPools = [
  for i in range(0, getNonZonalPoolCount(poolZones, poolCount)): {
    name: getNonZonalPoolName(poolBaseName, i + 1)
    zones: []
  }
]

var userPools = concat(zonalPools, nonZonalPools)

//
//   P O O L   C R E A T I O N
//

resource aksCluster 'Microsoft.ContainerService/managedClusters@2024-10-01' existing = {
  name: aksClusterName
}

var swiftNodepoolTags = enableSwitftV2
  ? {
      'aks-nic-enable-multi-tenancy': 'true'
    }
  : null

resource userAgentPools 'Microsoft.ContainerService/managedClusters/agentPools@2024-10-01' = [
  for (pool, i) in userPools: {
    parent: aksCluster
    name: take(pool.name, 12)
    properties: {
      osType: 'Linux'
      osSKU: 'AzureLinux'
      mode: 'User'
      enableAutoScaling: true
      enableEncryptionAtHost: true
      enableFIPS: true
      enableNodePublicIP: false
      kubeletDiskType: 'OS'
      osDiskType: 'Ephemeral'
      osDiskSizeGB: osDiskSizeGB
      count: minCount
      minCount: minCount
      maxCount: maxCount
      vmSize: vmSize
      type: 'VirtualMachineScaleSets'
      upgradeSettings: {
        maxSurge: '10%'
      }
      vnetSubnetID: vnetSubnetId
      podSubnetID: podSubnetId
      maxPods: maxPods
      availabilityZones: length(pool.zones) > 0 ? pool.zones : null
      securityProfile: {
        enableSecureBoot: false
        enableVTPM: false
      }
      nodeLabels: {
        'aro-hcp.azure.com/role': poolRole
      }
      nodeTaints: length(taints) > 0 ? taints : null
      tags: swiftNodepoolTags
    }
  }
]
