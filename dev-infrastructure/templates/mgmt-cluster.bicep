import { getLocationAvailabilityZonesCSV, csvToArray } from '../modules/common.bicep'

@description('Azure Region Location')
param location string = resourceGroup().location

@description('Availability Zones to use for the infrastructure, as a CSV string. Defaults to all the zones of the location')
param locationAvailabilityZones string = getLocationAvailabilityZonesCSV(location)
var locationAvailabilityZoneList = csvToArray(locationAvailabilityZones)

@description('AKS cluster name')
param aksClusterName string = 'aro-hcp-aks'

@description('Disk size for the AKS system nodes')
param aksSystemOsDiskSizeGB int

@description('Disk size for the AKS user nodes')
param aksUserOsDiskSizeGB int

@description('The resource ID of the OCP ACR')
param ocpAcrResourceId string

@description('The resource ID of the SVC ACR')
param svcAcrResourceId string

@description('Name of the resource group for the AKS nodes')
param aksNodeResourceGroupName string = '${resourceGroup().name}-aks1'

@description('VNET address prefix')
param vnetAddressPrefix string

@description('Min replicas for the worker nodes')
param userAgentMinCount int = 1

@description('Max replicas for the worker nodes')
param userAgentMaxCount int = 3

@description('VM instance type for the worker nodes')
param userAgentVMSize string = 'Standard_D2s_v3'

@description('Availability Zone count for worker nodes')
param userAgentPoolAZCount int = 3

@description('Min replicas for the system nodes')
param systemAgentMinCount int = 2

@description('Max replicas for the system nodes')
param systemAgentMaxCount int = 3

@description('VM instance type for the system nodes')
param systemAgentVMSize string = 'Standard_D2s_v3'

@description('Subnet address prefix')
param subnetPrefix string

@description('Specifies the address prefix of the subnet hosting the pods of the AKS cluster.')
param podSubnetPrefix string

@description('Kuberentes version to use with AKS')
param kubernetesVersion string

@description('The name of the keyvault for AKS.')
@maxLength(24)
param aksKeyVaultName string

@description('Manage soft delete setting for AKS etcd key-value store')
param aksEtcdKVEnableSoftDelete bool = true

@description('IPTags to be set on the cluster outbound IP address in the format of ipTagType:tag,ipTagType:tag')
param aksClusterOutboundIPAddressIPTags string = ''

@description('The name of the maestro consumer.')
param maestroConsumerName string

@description('The domain to use to use for the maestro certificate. Relevant only for environments where OneCert can be used.')
param maestroCertDomain string

@description('The issuer of the maestro certificate.')
param maestroCertIssuer string

@description('The Azure resource ID of the eventgrid namespace for Maestro.')
param maestroEventGridNamespaceId string

@description('The regional SVC DNS zone name.')
param regionalSvcDNSZoneName string

@description('The name of the CX KeyVault')
param cxKeyVaultName string

@description('The name of the MSI KeyVault')
param msiKeyVaultName string

@description('The name of the MGMT KeyVault')
param mgmtKeyVaultName string

@description('MSI that will be used to run deploymentScripts')
param aroDevopsMsiId string

@description('The Azure resource ID of the Azure Monitor Workspace (stores prometheus metrics)')
param azureMonitoringWorkspaceId string

// logs
@description('The namespace of the logs')
param logsNamespace string

@description('The managed identity name of the logs')
param logsMSI string

@description('The service account name of the logs managed identity')
param logsServiceAccount string

// Log Analytics Workspace ID will be passed from region pipeline if enabled in config
param logAnalyticsWorkspaceId string = ''

resource mgmtClusterNSG 'Microsoft.Network/networkSecurityGroups@2023-11-01' = {
  location: location
  name: 'mgmt-cluster-node-nsg'
  properties: {
    securityRules: []
  }
}

module mgmtCluster '../modules/aks-cluster-base.bicep' = {
  name: 'cluster'
  scope: resourceGroup()
  params: {
    location: location
    locationAvailabilityZones: locationAvailabilityZoneList
    aksClusterName: aksClusterName
    aksNodeResourceGroupName: aksNodeResourceGroupName
    aksEtcdKVEnableSoftDelete: aksEtcdKVEnableSoftDelete
    aksClusterOutboundIPAddressIPTags: aksClusterOutboundIPAddressIPTags
    deployIstio: false
    kubernetesVersion: kubernetesVersion
    vnetAddressPrefix: vnetAddressPrefix
    nodeSubnetNSGId: mgmtClusterNSG.id
    subnetPrefix: subnetPrefix
    podSubnetPrefix: podSubnetPrefix
    clusterType: 'mgmt-cluster'
    workloadIdentities: items({
      maestro_wi: {
        uamiName: 'maestro-consumer'
        namespace: 'maestro'
        serviceAccountName: 'maestro'
      }
      logs_wi: {
        uamiName: logsMSI
        namespace: logsNamespace
        serviceAccountName: logsServiceAccount
      }
      pko_wi: {
        uamiName: 'package-operator'
        namespace: 'package-operator-system'
        serviceAccountName: 'package-operator'
      }
    })
    aksKeyVaultName: aksKeyVaultName
    logAnalyticsWorkspaceId: logAnalyticsWorkspaceId
    pullAcrResourceIds: [ocpAcrResourceId, svcAcrResourceId]
    userAgentMinCount: userAgentMinCount
    userAgentPoolAZCount: userAgentPoolAZCount
    userAgentMaxCount: userAgentMaxCount
    userAgentVMSize: userAgentVMSize
    systemAgentMinCount: systemAgentMinCount
    systemAgentMaxCount: systemAgentMaxCount
    systemAgentVMSize: systemAgentVMSize
    systemOsDiskSizeGB: aksSystemOsDiskSizeGB
    userOsDiskSizeGB: aksUserOsDiskSizeGB
    aroDevopsMsiId: aroDevopsMsiId
    dcrId: dataCollection.outputs.dcrId
  }
}

output aksClusterName string = mgmtCluster.outputs.aksClusterName

//
// M E T R I C S
//

module dataCollection '../modules/metrics/datacollection.bicep' = {
  name: '${resourceGroup().name}-aksClusterName'
  params: {
    azureMonitorWorkspaceLocation: location
    azureMonitoringWorkspaceId: azureMonitoringWorkspaceId
    aksClusterName: aksClusterName
  }
}

//
// K E Y V A U L T S
//
var logsManagedIdentityPrincipalId = filter(mgmtCluster.outputs.userAssignedIdentities, id => id.uamiName == logsMSI)[0].uamiPrincipalID

module logsMgmtKeyVaultAccess '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: guid(mgmtKeyVaultName, logsMSI, 'certuser')
  params: {
    keyVaultName: mgmtKeyVaultName
    roleName: 'Key Vault Certificate User'
    managedIdentityPrincipalId: logsManagedIdentityPrincipalId
  }
}

module cxCSIKeyVaultAccess '../modules/keyvault/keyvault-secret-access.bicep' = [
  for role in [
    'Key Vault Secrets Officer'
    'Key Vault Certificate User'
    'Key Vault Certificates Officer'
  ]: {
    name: guid(cxKeyVaultName, 'aks-kv-csi-mi', role)
    params: {
      keyVaultName: cxKeyVaultName
      roleName: role
      managedIdentityPrincipalId: mgmtCluster.outputs.aksClusterKeyVaultSecretsProviderPrincipalId
    }
  }
]

module msiCSIKeyVaultAccess '../modules/keyvault/keyvault-secret-access.bicep' = [
  for role in [
    'Key Vault Secrets Officer'
    'Key Vault Certificate User'
    'Key Vault Certificates Officer'
  ]: {
    name: guid(msiKeyVaultName, 'aks-kv-csi-mi', role)
    params: {
      keyVaultName: msiKeyVaultName
      roleName: role
      managedIdentityPrincipalId: mgmtCluster.outputs.aksClusterKeyVaultSecretsProviderPrincipalId
    }
  }
]

resource mgmtKeyVault 'Microsoft.KeyVault/vaults@2024-04-01-preview' existing = {
  name: mgmtKeyVaultName
}

//
//   M A E S T R O
//

var effectiveMaestroCertDomain = !empty(maestroCertDomain) ? maestroCertDomain : 'maestro.${regionalSvcDNSZoneName}'

module maestroConsumer '../modules/maestro/maestro-consumer.bicep' = if (maestroEventGridNamespaceId != '') {
  name: 'maestro-consumer'
  params: {
    maestroAgentManagedIdentityPrincipalId: filter(
      mgmtCluster.outputs.userAssignedIdentities,
      id => id.uamiName == 'maestro-consumer'
    )[0].uamiPrincipalID
    maestroConsumerName: maestroConsumerName
    maestroEventGridNamespaceId: maestroEventGridNamespaceId
    certKeyVaultName: mgmtKeyVaultName
    keyVaultOfficerManagedIdentityName: aroDevopsMsiId
    maestroCertificateDomain: effectiveMaestroCertDomain
    maestroCertificateIssuer: maestroCertIssuer
  }
  dependsOn: [
    mgmtKeyVault
  ]
}

//
//  E V E N T   G R I D   P R I V A T E   E N D P O I N T   C O N N E C T I O N
//

module eventGrindPrivateEndpoint '../modules/private-endpoint.bicep' = if (maestroEventGridNamespaceId != '') {
  name: 'eventGridPrivateEndpoint'
  params: {
    location: location
    subnetIds: [mgmtCluster.outputs.aksNodeSubnetId]
    privateLinkServiceId: maestroEventGridNamespaceId
    vnetId: mgmtCluster.outputs.aksVnetId
    serviceType: 'eventgrid'
    groupId: 'topicspace'
  }
}
