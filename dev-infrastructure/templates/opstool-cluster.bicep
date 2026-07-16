import {
  csvToArray
  getLocationAvailabilityZonesCSV
} from '../modules/common.bicep'

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

@description('Number of worker agent pools to create')
param userAgentPoolCount int = 0

@description('Minimum node count for worker agent pool')
param userAgentMinCount int = 0

@description('Maximum node count for worker agent pool')
param userAgentMaxCount int = 0

@description('VM instance type for the worker nodes')
param userAgentVMSize string = 'Standard_D4s_v3'

@description('Zones to use for the worker nodes')
param userAgentPoolZones string = ''

@description('Zone redundant mode for the worker nodes')
param userZoneRedundantMode string = 'Disabled'

@description('Disk size for the AKS worker nodes')
param userOsDiskSizeGB int = 64

@description('Number of infra agent pools to create')
param infraAgentPoolCount int = 1

@description('Minimum node count for infra agent pool')
param infraAgentMinCount int = 1

@description('Maximum node count for infra agent pool')
param infraAgentMaxCount int = 3

@description('VM instance type for the infra nodes')
param infraAgentVMSize string = 'Standard_D4s_v3'

@description('Zones to use for the infra nodes')
param infraAgentPoolZones string = ''

@description('Zone redundant mode for the infra nodes')
param infraZoneRedundantMode string = 'Disabled'

@description('Disk size for the AKS infra nodes')
param infraOsDiskSizeGB int = 64

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

@description('Istio control plane versions to use with AKS. CSV format')
param istioVersions string

@description('Network dataplane plugin for the AKS cluster')
param aksNetworkDataplane string = 'cilium'

@description('Network policy plugin for the AKS cluster')
param aksNetworkPolicy string = 'cilium'

@description('Maximum surge for AKS node pool upgrades')
param aksUpgradeSettingsMaxSurge string

@description('Maximum unavailable for AKS node pool upgrades')
param aksUpgradeSettingsMaxUnavailable string

@description('IPTags to be set on the cluster outbound IP address')
param aksClusterOutboundIPAddressIPTags string = ''

@description('Azure Monitor Workspace name for Prometheus remote write')
param azureMonitorWorkspaceName string

@description('Maximum active time series limit for Azure Monitor Workspace in millions (2M initial, bump when hitting 50% utilization)')
param amwMaxActiveTimeSeriesMillions int = 2

@description('Maximum events per minute limit for Azure Monitor Workspace in millions (2M initial, bump when hitting 50% utilization)')
param amwMaxEventsPerMinuteMillions int = 2

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

@description('The name of the shared SVC ACR for image pulls')
param svcAcrName string = ''

@description('The resource group containing the shared SVC ACR')
param svcAcrResourceGroupName string = ''

var nsgName = 'opstool-cluster-nsg'
var systemAgentPoolName = 'system'
var userAgentPoolName = 'user'
var infraAgentPoolName = 'infra'

resource opstoolClusterNSG 'Microsoft.Network/networkSecurityGroups@2023-11-01' existing = {
  name: nsgName
}

var vnetName = 'opstool-aks-net'
var nodeSubnetName = 'ClusterSubnet-001'

resource opstoolMI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: 'opstool'
}

resource cihealthMI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: 'cihealth'
}

resource certManagerMI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: 'cert-manager'
}

resource prometheusMI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: 'prometheus'
}

resource tenantQuotaMI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: 'tenant-quota'
}

var workloadIdentities = items({
  cihealth_wi: {
    uamiName: cihealthMI.name
    namespace: 'cihealth'
    serviceAccountName: 'cihealth'
  }
  cert_manager_wi: {
    uamiName: certManagerMI.name
    namespace: 'cert-manager'
    serviceAccountName: 'cert-manager'
  }
  prometheus_wi: {
    uamiName: prometheusMI.name
    namespace: 'prometheus'
    serviceAccountName: 'prometheus'
  }
  tenant_quota_wi: {
    uamiName: tenantQuotaMI.name
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

resource aksClusterUserDefinedManagedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: '${aksClusterName}-msi'
}

module opstoolCluster '../modules/aks-cluster-base.bicep' = {
  name: 'cluster-${uniqueString(resourceGroup().name)}'
  scope: resourceGroup()
  params: {
    location: location
    ipResourceGroup: resourceGroup().name
    ipZones: locationAvailabilityZoneList
    aksClusterName: aksClusterName
    systemAgentPoolName: systemAgentPoolName
    userAgentPoolName: userAgentPoolName
    infraAgentPoolName: infraAgentPoolName
    aksNodeResourceGroupName: aksNodeResourceGroupName
    aksEtcdKVEnableSoftDelete: aksEtcdKVEnableSoftDelete
    aksClusterOutboundIPAddressIPTags: aksClusterOutboundIPAddressIPTags
    kubernetesVersion: kubernetesVersion
    deployIstio: true
    istioVersions: split(istioVersions, ',')
    vnetName: vnetName
    nodeSubnetId: nodeSubnetCreation.outputs.subnetId
    podSubnetPrefix: podSubnetPrefix
    clusterType: 'opstool-cluster'
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
    systemAgentPoolZones: length(csvToArray(systemAgentPoolZones)) > 0
      ? csvToArray(systemAgentPoolZones)
      : locationAvailabilityZoneList
    systemZoneRedundantMode: systemZoneRedundantMode
    networkDataplane: aksNetworkDataplane
    networkPolicy: aksNetworkPolicy
    upgradeSettingsMaxSurge: aksUpgradeSettingsMaxSurge
    upgradeSettingsMaxUnavailable: aksUpgradeSettingsMaxUnavailable
    workloadIdentities: workloadIdentities
    aksKeyVaultName: aksKeyVaultName
    aksKeyVaultTagName: aksKeyVaultTagName
    aksKeyVaultTagValue: aksKeyVaultTagValue
    pullAcrResourceIds: empty(svcAcrName) || empty(svcAcrResourceGroupName)
      ? []
      : [resourceId(svcAcrResourceGroupName, 'Microsoft.ContainerRegistry/registries', svcAcrName)]
    deploymentMsiId: opstoolMI.id
    enableSwiftV2Nodepools: false
    owningTeamTagValue: owningTeamTagValue
    aksClusterUserDefinedManagedIdentityName: aksClusterUserDefinedManagedIdentity.name
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

module amwIngestionLimits '../modules/metrics/amw-ingestion-limits.bicep' = {
  name: 'amw-ingestion-limits'
  params: {
    azureMonitorWorkspaceName: azureMonitorWorkspaceName
    location: location
    maxActiveTimeSeriesMillions: amwMaxActiveTimeSeriesMillions
    maxEventsPerMinuteMillions: amwMaxEventsPerMinuteMillions
  }
  dependsOn: [
    azureMonitorWorkspace
  ]
}

module dataCollection '../modules/metrics/datacollection.bicep' = {
  name: 'opstool-datacollection'
  params: {
    azureMonitorWorkspaceLocation: location
    azureMonitoringWorkspaceId: azureMonitorWorkspace.id
    hcpAzureMonitoringWorkspaceId: ''
    aksClusterName: aksClusterName
    prometheusPrincipalId: prometheusMI.properties.principalId
  }
  dependsOn: [
    opstoolCluster
  ]
}

//
//   W O R K L O A D   S E C R E T S   K E Y   V A U L T
//

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
    managedIdentityPrincipalIds: [tenantQuotaMI.properties.principalId]
  }
  dependsOn: [
    workloadKV
  ]
}

module cihealthKVAccess '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: 'cihealth-kv-access'
  params: {
    keyVaultName: workloadKVName
    roleName: 'Key Vault Secrets User'
    managedIdentityPrincipalIds: [cihealthMI.properties.principalId]
  }
  dependsOn: [
    workloadKV
  ]
}

output aksClusterName string = opstoolCluster.outputs.aksClusterName
output azureMonitorWorkspaceId string = azureMonitorWorkspace.id
output workloadKVName string = workloadKV.outputs.kvName
output workloadKVUrl string = workloadKV.outputs.kvUrl
