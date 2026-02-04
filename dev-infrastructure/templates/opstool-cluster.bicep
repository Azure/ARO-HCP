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

@description('Zones to use for the system nodes')
param systemAgentPoolZones string

@description('Zone redundant mode for the system nodes')
param systemZoneRedundantMode string

@description('Disk size for the AKS system nodes')
param aksSystemOsDiskSizeGB int

@description('Name of the resource group for the AKS nodes')
param aksNodeResourceGroupName string = '${resourceGroup().name}-aks1'

@description('VNET address prefix')
param vnetAddressPrefix string

@description('Subnet address prefix')
param subnetPrefix string

@description('Pod subnet address prefix')
param podSubnetPrefix string

@description('Kubernetes version')
param kubernetesVersion string

@description('Network dataplane plugin for the AKS cluster')
param aksNetworkDataplane string = 'cilium'

@description('Network policy plugin for the AKS cluster')
param aksNetworkPolicy string = 'cilium'

@description('IPTags to be set on the cluster outbound IP address')
param aksClusterOutboundIPAddressIPTags string = ''

@description('Azure Monitor Workspace name for Prometheus remote write')
param azureMonitorWorkspaceName string

@description('Owning team tag value')
param owningTeamTagValue string = 'ARO-HCP-SRE'

@description('AKS Key Vault name for etcd encryption')
@maxLength(24)
param aksKeyVaultName string

@description('AKS Key Vault tag name')
param aksKeyVaultTagName string

@description('AKS Key Vault tag value')
param aksKeyVaultTagValue string

@description('Enable soft delete for AKS Key Vault')
param aksEtcdKVEnableSoftDelete bool = false

@description('Name for the workload Key Vault')
param workloadKVName string

@description('The resource ID of the SVC ACR for image pulls')
param svcAcrResourceId string = ''

module managedIdentities '../modules/managed-identities.bicep' = {
  name: 'opstool-managed-identities'
  params: {
    location: location
    manageIdentityNames: [
      'opstool'
      'prometheus'
      'tenant-quota'
    ]
  }
}

var nsgName = 'opstool-cluster-nsg'
resource opstoolClusterNSG 'Microsoft.Network/networkSecurityGroups@2023-11-01' = {
  name: nsgName
  location: location
  tags: {
    persist: 'true'
    owningTeam: owningTeamTagValue
  }
  properties: {
    securityRules: []
  }
}

var vnetName = 'opstool-aks-net'
var nodeSubnetName = 'ClusterSubnet-001'

var opstoolMI = mi.getManagedIdentityByName(managedIdentities.outputs.managedIdentities, 'opstool')
var prometheusMI = mi.getManagedIdentityByName(managedIdentities.outputs.managedIdentities, 'prometheus')

var workloadIdentities = items({
  prometheus_wi: {
    uamiName: 'prometheus'
    namespace: 'prometheus'
    serviceAccountName: 'prometheus'
  }
  tenant_quota_wi: {
    uamiName: 'tenant-quota'
    namespace: 'tenant-quota'
    serviceAccountName: 'tenant-quota-collector'
  }
})

module vnetCreation '../modules/network/vnet.bicep' = {
  name: 'vnet-${vnetName}-creation'
  params: {
    location: location
    vnetName: vnetName
    vnetAddressPrefix: vnetAddressPrefix
    enableSwift: false
    deploymentMsiId: opstoolMI.uamiID
  }
}

module nodeSubnetCreation '../modules/network/aks-node-subnet.bicep' = {
  name: 'subnet-${nodeSubnetName}-creation'
  params: {
    vnetName: vnetName
    subnetName: nodeSubnetName
    subnetNSGId: opstoolClusterNSG.id
    subnetPrefix: subnetPrefix
  }
  dependsOn: [
    vnetCreation
  ]
}

module opstoolCluster '../modules/aks-cluster-base.bicep' = {
  name: 'cluster-${uniqueString(resourceGroup().name)}'
  scope: resourceGroup()
  params: {
    location: location
    ipResourceGroup: resourceGroup().name
    ipZones: locationAvailabilityZoneList
    aksClusterName: aksClusterName
    aksNodeResourceGroupName: aksNodeResourceGroupName
    aksEtcdKVEnableSoftDelete: aksEtcdKVEnableSoftDelete
    aksClusterOutboundIPAddressIPTags: aksClusterOutboundIPAddressIPTags
    kubernetesVersion: kubernetesVersion
    deployIstio: false
    istioVersions: []
    istioIngressGatewayIPAddressName: ''
    istioIngressGatewayIPAddressIPTags: ''
    vnetName: vnetName
    nodeSubnetId: nodeSubnetCreation.outputs.subnetId
    podSubnetPrefix: podSubnetPrefix
    clusterType: 'opstool-cluster'
    userOsDiskSizeGB: 64
    userAgentMinCount: 0
    userAgentMaxCount: 0
    userAgentVMSize: 'Standard_D4s_v3'
    userAgentPoolCount: 0
    userAgentPoolZones: []
    userZoneRedundantMode: 'Disabled'
    infraAgentMinCount: 1
    infraAgentMaxCount: 3
    infraAgentVMSize: 'Standard_D4s_v3'
    infraAgentPoolCount: 1
    infraAgentPoolZones: length(csvToArray(systemAgentPoolZones)) > 0
      ? csvToArray(systemAgentPoolZones)
      : locationAvailabilityZoneList
    infraOsDiskSizeGB: 64
    infraZoneRedundantMode: 'Disabled'
    systemOsDiskSizeGB: aksSystemOsDiskSizeGB
    systemAgentMinCount: systemAgentMinCount
    systemAgentMaxCount: systemAgentMaxCount
    systemAgentVMSize: systemAgentVMSize
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
    pullAcrResourceIds: empty(svcAcrResourceId) ? [] : [svcAcrResourceId]
    deploymentMsiId: opstoolMI.uamiID
    enableSwiftV2Nodepools: false
    owningTeamTagValue: owningTeamTagValue
  }
}

resource azureMonitorWorkspace 'Microsoft.Monitor/accounts@2023-04-03' = {
  name: azureMonitorWorkspaceName
  location: location
  tags: {
    persist: 'true'
    owningTeam: owningTeamTagValue
    purpose: 'opstool-prometheus-metrics'
  }
}

module dataCollection '../modules/metrics/datacollection.bicep' = {
  name: 'opstool-datacollection'
  params: {
    azureMonitorWorkspaceLocation: location
    azureMonitoringWorkspaceId: azureMonitorWorkspace.id
    hcpAzureMonitoringWorkspaceId: ''
    aksClusterName: aksClusterName
    prometheusPrincipalId: prometheusMI.uamiPrincipalID
  }
  dependsOn: [
    opstoolCluster
  ]
}

//
//   W O R K L O A D   S E C R E T S   K E Y   V A U L T
//

var tenantQuotaMI = mi.getManagedIdentityByName(managedIdentities.outputs.managedIdentities, 'tenant-quota')

module workloadKV '../modules/keyvault/keyvault.bicep' = {
  name: 'opstool-workload-secrets-kv'
  params: {
    location: location
    keyVaultName: workloadKVName
    enableSoftDelete: false
    private: false
    tagKey: 'aroHCPPurpose'
    tagValue: 'opstool-workload-secrets'
  }
}

module tenantQuotaKVAccess '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: 'tenant-quota-kv-access'
  params: {
    keyVaultName: workloadKVName
    roleName: 'Key Vault Secrets User'
    managedIdentityPrincipalIds: [tenantQuotaMI.uamiPrincipalID]
  }
  dependsOn: [
    workloadKV
  ]
}

output aksClusterName string = opstoolCluster.outputs.aksClusterName
output azureMonitorWorkspaceId string = azureMonitorWorkspace.id
output workloadKVName string = workloadKV.outputs.kvName
output workloadKVUrl string = workloadKV.outputs.kvUrl
