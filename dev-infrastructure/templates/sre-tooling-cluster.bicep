import {
  csvToArray
  getLocationAvailabilityZonesCSV
} from '../modules/common.bicep'
import * as mi from '../modules/managed-identities.bicep'

@description('Azure Region Location')
param location string = resourceGroup().location

@description('Availability Zones to use for the infrastructure, as a CSV string. Defaults to all the zones of the location')
param locationAvailabilityZones string = getLocationAvailabilityZonesCSV(location)
var locationAvailabilityZoneList = csvToArray(locationAvailabilityZones)

@description('AKS cluster name')
param aksClusterName string

@description('Minimum node count for system agent pool')
param systemAgentMinCount int

@description('Maximum node count for system agent pool')
param systemAgentMaxCount int

@description('VM instance type for the system nodes')
param systemAgentVMSize string

@description('Number of pools to create for system nodes')
param systemAgentPoolCount int

@description('Zones to use for the system nodes')
param systemAgentPoolZones string

@description('Zone redundant mode for the system nodes')
param systemZoneRedundantMode string

@description('Disk size for the AKS system nodes')
param aksSystemOsDiskSizeGB int

@description('Disk size for the AKS user nodes')
param userOsDiskSizeGB int

@description('Network dataplane plugin for the AKS cluster')
param aksNetworkDataplane string

@description('Network policy plugin for the AKS cluster')
param aksNetworkPolicy string

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

@description('Disk size for the AKS infra nodes')
param infraOsDiskSizeGB int

@description('Zone redundant mode for the infra nodes')
param infraZoneRedundantMode string

@description('The resource ID of the SVC ACR')
param svcAcrResourceId string

@description('Name of the resource group for the AKS nodes')
param aksNodeResourceGroupName string = '${resourceGroup().name}-aks1'

@description('VNET address prefix')
param vnetAddressPrefix string

@description('Subnet address prefix')
param subnetPrefix string

@description('Specifies the address prefix of the subnet hosting the pods of the AKS cluster.')
param podSubnetPrefix string

@description('Kubernetes version to use with AKS')
param kubernetesVersion string

@description('The name of the keyvault for AKS.')
@maxLength(24)
param aksKeyVaultName string

@description('The tag key for the AKS keyvault')
param aksKeyVaultTagName string

@description('The tag value for the AKS keyvault')
param aksKeyVaultTagValue string

@description('Manage soft delete setting for AKS etcd key-value store')
param aksEtcdKVEnableSoftDelete bool = true

@description('IPTags to be set on the cluster outbound IP address in the format of ipTagType:tag,ipTagType:tag')
param aksClusterOutboundIPAddressIPTags string = ''

@description('The resourcegroup for regional infrastructure')
param regionalResourceGroup string

@description('The name of the service keyvault')
param serviceKeyVaultName string

@description('The name of the resourcegroup for the service keyvault')
param serviceKeyVaultResourceGroup string = resourceGroup().name

@description('MSI that will be used to run the deploymentScript')
param globalMSIId string

@description('The Azure Resource ID of the Azure Monitor Workspace (stores prometheus metrics)')
param azureMonitoringWorkspaceId string

// logs
@description('The namespace of the logs')
param logsNamespace string

@description('The managed identity name of the logs')
param logsMSI string

@description('The service account name of the logs managed identity')
param logsServiceAccount string

@description('The name of the Admin API managed identity')
param adminApiMIName string

@description('The namespace of the Admin API managed identity')
param adminApiNamespace string

@description('The service account name of the Admin API managed identity')
param adminApiServiceAccountName string

//
//   M A N A G E D   I D E N T I T I E S
//

var workloadIdentities = items({
  logs_wi: {
    uamiName: logsMSI
    namespace: logsNamespace
    serviceAccountName: logsServiceAccount
  }
  prom_wi: {
    uamiName: 'prometheus'
    namespace: 'prometheus'
    serviceAccountName: 'prometheus'
  }
  admin_api_wi: {
    uamiName: adminApiMIName
    namespace: adminApiNamespace
    serviceAccountName: adminApiServiceAccountName
  }
})

module managedIdentities '../modules/managed-identities.bicep' = {
  name: 'managed-identities'
  params: {
    location: location
    manageIdentityNames: [for wi in workloadIdentities: wi.value.uamiName]
  }
}

//
//   A K S
//

resource sreToolingClusterNSG 'Microsoft.Network/networkSecurityGroups@2023-11-01' = {
  location: location
  name: 'sre-tooling-cluster-node-nsg'
  properties: {
    securityRules: []
  }
}

var vnetName = 'aks-net'
var nodeSubnetName = 'ClusterSubnet-001'

module vnetCreation '../modules/network/vnet.bicep' = {
  name: 'vnet-${vnetName}-creation'
  params: {
    location: location
    vnetName: vnetName
    vnetAddressPrefix: vnetAddressPrefix
    enableSwift: false
    deploymentMsiId: globalMSIId
  }
}

module nodeSubnetCreation '../modules/network/aks-node-subnet.bicep' = {
  name: 'subnet-${nodeSubnetName}-creation'
  params: {
    vnetName: vnetName
    subnetName: nodeSubnetName
    subnetNSGId: sreToolingClusterNSG.id
    subnetPrefix: subnetPrefix
  }
  dependsOn: [
    vnetCreation
  ]
}

module sreToolingCluster '../modules/aks-cluster-base.bicep' = {
  name: 'cluster-${uniqueString(resourceGroup().name)}'
  scope: resourceGroup()
  params: {
    location: location
    ipResourceGroup: regionalResourceGroup
    ipZones: locationAvailabilityZoneList
    aksClusterName: aksClusterName
    aksNodeResourceGroupName: aksNodeResourceGroupName
    aksEtcdKVEnableSoftDelete: aksEtcdKVEnableSoftDelete
    aksClusterOutboundIPAddressIPTags: aksClusterOutboundIPAddressIPTags
    kubernetesVersion: kubernetesVersion
    vnetName: vnetName
    nodeSubnetId: nodeSubnetCreation.outputs.subnetId
    podSubnetPrefix: podSubnetPrefix
    clusterType: 'sre-tooling-cluster'
    userOsDiskSizeGB: userOsDiskSizeGB
    userAgentMinCount: userAgentMinCount
    userAgentMaxCount: userAgentMaxCount
    userAgentVMSize: userAgentVMSize
    userAgentPoolCount: userAgentPoolCount
    userAgentPoolZones: length(csvToArray(userAgentPoolZones)) > 0
      ? csvToArray(userAgentPoolZones)
      : locationAvailabilityZoneList
    userZoneRedundantMode: userZoneRedundantMode
    infraAgentMinCount: infraAgentMinCount
    infraAgentMaxCount: infraAgentMaxCount
    infraAgentVMSize: infraAgentVMSize
    infraAgentPoolCount: infraAgentPoolCount
    infraAgentPoolZones: length(csvToArray(infraAgentPoolZones)) > 0
      ? csvToArray(infraAgentPoolZones)
      : locationAvailabilityZoneList
    infraOsDiskSizeGB: infraOsDiskSizeGB
    infraZoneRedundantMode: infraZoneRedundantMode
    systemOsDiskSizeGB: aksSystemOsDiskSizeGB
    systemAgentMinCount: systemAgentMinCount
    systemAgentMaxCount: systemAgentMaxCount
    systemAgentVMSize: systemAgentVMSize
    systemAgentPoolCount: systemAgentPoolCount
    systemAgentPoolZones: length(csvToArray(systemAgentPoolZones)) > 0
      ? csvToArray(systemAgentPoolZones)
      : locationAvailabilityZoneList
    systemZoneRedundantMode: systemZoneRedundantMode
    networkDataplane: aksNetworkDataplane
    networkPolicy: aksNetworkPolicy
    workloadIdentities: workloadIdentities
    aksKeyVaultName: aksKeyVaultName
    aksKeyVaultTagName: aksKeyVaultTagName
    aksKeyVaultTagValue: aksKeyVaultTagValue
    pullAcrResourceIds: [svcAcrResourceId]
    deploymentMsiId: globalMSIId
    enableSwiftV2Nodepools: false
    deployIstio: false
  }
  dependsOn: [
    managedIdentities
  ]
}

output aksClusterName string = sreToolingCluster.outputs.aksClusterName

//
// L O G S
//

//
// M E T R I C S
//

module dataCollection '../modules/metrics/datacollection.bicep' = {
  name: 'metrics-infra'
  params: {
    azureMonitorWorkspaceLocation: location
    azureMonitoringWorkspaceId: azureMonitoringWorkspaceId
    aksClusterName: aksClusterName
    prometheusPrincipalId: mi.getManagedIdentityByName(managedIdentities.outputs.managedIdentities, 'prometheus').uamiPrincipalID
  }
  dependsOn: [
    sreToolingCluster
  ]
}

//
//   K E Y V A U L T S
//

module logsServiceKeyVaultAccess '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: guid(serviceKeyVaultName, logsMSI, 'certuser')
  scope: resourceGroup(serviceKeyVaultResourceGroup)
  params: {
    keyVaultName: serviceKeyVaultName
    roleName: 'Key Vault Certificate User'
    managedIdentityPrincipalIds: [
      mi.getManagedIdentityByName(managedIdentities.outputs.managedIdentities, logsMSI).uamiPrincipalID
    ]
  }
}
