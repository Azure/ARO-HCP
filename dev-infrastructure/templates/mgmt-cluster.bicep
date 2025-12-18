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
param aksClusterName string = 'aro-hcp-aks'

@description('Disk size for the AKS system nodes')
param systemOsDiskSizeGB int

@description('Disk size for the AKS user nodes')
param userOsDiskSizeGB int

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

@description('Min replicas for the system nodes')
param systemAgentMinCount int = 2

@description('Max replicas for the system nodes')
param systemAgentMaxCount int = 3

@description('VM instance type for the system nodes')
param systemAgentVMSize string = 'Standard_D2s_v3'

@description('Number of pools to create for system nodes')
param systemAgentPoolCount int

@description('Zones to use for the system nodes')
param systemAgentPoolZones string

@description('Zone redundant mode for the system nodes')
param systemZoneRedundantMode string

@description('Network dataplane plugin for the AKS cluster')
param aksNetworkDataplane string

@description('Network policy plugin for the AKS cluster')
param aksNetworkPolicy string

@description('Subnet address prefix')
param subnetPrefix string

@description('Specifies the address prefix of the subnet hosting the pods of the AKS cluster.')
param podSubnetPrefix string

@description('Kuberentes version to use with AKS')
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

@description('Enable Swift V2 for the AKS cluster VNET')
param aksEnableSwiftVnet bool

@description('Enable Swift V2 for the AKS cluster nodepools')
param aksEnableSwiftNodepools bool

@description('The name of the maestro consumer.')
param maestroConsumerName string

@description('The domain to use to use for the maestro certificate. Relevant only for environments where OneCert can be used.')
param maestroCertDomain string

@description('The issuer of the maestro certificate.')
param maestroCertIssuer string

@description('The Azure resource ID of the eventgrid namespace for Maestro.')
param maestroEventGridNamespaceId string

@description('The name of the maestro consumer.')
param maestroConsumerMIName string

@description('The namespace of the maestro consumer.')
param maestroConsumerNamespace string

@description('The service account name of the maestro consumer.')
param maestroConsumerServiceAccountName string

@description('The regional SVC DNS zone name.')
param regionalSvcDNSZoneName string

@description('The name of the CX KeyVault')
param cxKeyVaultName string

@description('The name of the MSI KeyVault')
param msiKeyVaultName string

@description('The name of the MGMT KeyVault')
param mgmtKeyVaultName string

@description('MSI that will be used to run deploymentScripts')
param globalMSIId string

@description('The Azure resource ID of the Azure Monitor Workspace (stores prometheus metrics for services/aks level metrics)')
param azureMonitoringWorkspaceId string

@description('The Azure resource ID of the Azure Monitor Workspace (stores prometheus metrics for hosted control planes)')
param hcpAzureMonitoringWorkspaceId string

// logs
@description('The namespace of the logs')
param logsNamespace string

@description('The managed identity name of the logs')
param logsMSI string

@description('The service account name of the logs managed identity')
param logsServiceAccount string

@description('Issuer of certificate for Geneva Authentication')
param genevaCertificateIssuer string = 'Self'

@description('Name of certificate in Keyvault and hostname used in SAN')
param genevaRpLogsName string

@description('Name of certificate in Keyvault and hostname used in SAN')
param genevaClusterLogsName string

@description('Domain used for creation of geneva auth certificates')
param genevaCertificateDomain string

@description('Should geneva certificates be managed')
param genevaManageCertificates bool

@description('Name of the MSI for the PKO')
param pkoMIName string

@description('Namespace of the PKO')
param pkoNamespace string

@description('Service account name of the PKO')
param pkoServiceAccountName string

@description('The name of the Azure Storage account to create for HCP Backups')
param hcpBackupsStorageAccountName string

@description('The cluster tag value for the owning team')
param owningTeamTagValue string

//
//   M A N A G E D   I D E N T I T I E S
//

var workloadIdentities = items({
  maestro_wi: {
    uamiName: maestroConsumerMIName
    namespace: maestroConsumerNamespace
    serviceAccountName: maestroConsumerServiceAccountName
  }
  logs_wi: {
    uamiName: logsMSI
    namespace: logsNamespace
    serviceAccountName: logsServiceAccount
  }
  pko_wi: {
    uamiName: pkoMIName
    namespace: pkoNamespace
    serviceAccountName: pkoServiceAccountName
  }
  prom_wi: {
    uamiName: 'prometheus'
    namespace: 'prometheus'
    serviceAccountName: 'prometheus'
  }
  velero_wi: {
    uamiName: 'velero'
    namespace: 'openshift-adp'
    serviceAccountName: 'velero'
  }
  oadp_wi: {
    uamiName: 'openshift-adp-controller-manager'
    namespace: 'openshift-adp'
    serviceAccountName: 'openshift-adp-controller-manager'
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

resource mgmtClusterNSG 'Microsoft.Network/networkSecurityGroups@2023-11-01' = {
  location: location
  name: 'mgmt-cluster-node-nsg'
  properties: {
    securityRules: [
      {
        name: 'kas-443-in-internet'
        properties: {
          access: 'Allow'
          destinationAddressPrefix: '*'
          destinationPortRange: '443'
          direction: 'Inbound'
          priority: 120
          protocol: 'Tcp'
          sourceAddressPrefix: '*'
          sourcePortRange: '*'
        }
      }
      {
        name: 'kas-6443-in-internet'
        properties: {
          access: 'Allow'
          destinationAddressPrefix: '*'
          destinationPortRange: '6443'
          direction: 'Inbound'
          priority: 130
          protocol: 'Tcp'
          sourceAddressPrefix: '*'
          sourcePortRange: '*'
        }
      }
    ]
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
    enableSwift: aksEnableSwiftVnet
    deploymentMsiId: globalMSIId
  }
}

module nodeSubnetCreation '../modules/network/aks-node-subnet.bicep' = {
  name: 'subnet-${nodeSubnetName}-creation'
  params: {
    vnetName: vnetName
    subnetName: nodeSubnetName
    subnetNSGId: mgmtClusterNSG.id
    subnetPrefix: subnetPrefix
  }
  dependsOn: [
    vnetCreation
  ]
}

module mgmtCluster '../modules/aks-cluster-base.bicep' = {
  name: 'cluster-${uniqueString(resourceGroup().name)}'
  scope: resourceGroup()
  params: {
    location: location
    ipZones: locationAvailabilityZoneList
    ipResourceGroup: resourceGroup().name
    aksClusterName: aksClusterName
    aksNodeResourceGroupName: aksNodeResourceGroupName
    aksEtcdKVEnableSoftDelete: aksEtcdKVEnableSoftDelete
    aksClusterOutboundIPAddressIPTags: aksClusterOutboundIPAddressIPTags
    deployIstio: false
    kubernetesVersion: kubernetesVersion
    vnetName: vnetName
    nodeSubnetId: nodeSubnetCreation.outputs.subnetId
    podSubnetPrefix: podSubnetPrefix
    clusterType: 'mgmt-cluster'
    workloadIdentities: workloadIdentities
    aksKeyVaultName: aksKeyVaultName
    aksKeyVaultTagName: aksKeyVaultTagName
    aksKeyVaultTagValue: aksKeyVaultTagValue
    pullAcrResourceIds: [ocpAcrResourceId, svcAcrResourceId]
    systemAgentMinCount: systemAgentMinCount
    systemAgentMaxCount: systemAgentMaxCount
    systemAgentVMSize: systemAgentVMSize
    systemAgentPoolCount: systemAgentPoolCount
    systemAgentPoolZones: length(csvToArray(systemAgentPoolZones)) > 0
      ? csvToArray(systemAgentPoolZones)
      : locationAvailabilityZoneList
    systemOsDiskSizeGB: systemOsDiskSizeGB
    systemZoneRedundantMode: systemZoneRedundantMode
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
    infraZoneRedundantMode: infraZoneRedundantMode
    infraOsDiskSizeGB: infraOsDiskSizeGB
    networkDataplane: aksNetworkDataplane
    networkPolicy: aksNetworkPolicy
    deploymentMsiId: globalMSIId
    enableSwiftV2Nodepools: aksEnableSwiftNodepools
    owningTeamTagValue: owningTeamTagValue
  }
  dependsOn: [
    managedIdentities
  ]
}

output aksClusterName string = mgmtCluster.outputs.aksClusterName

//
// M E T R I C S
//

module dataCollection '../modules/metrics/datacollection.bicep' = {
  name: 'metrics-infra'
  params: {
    azureMonitorWorkspaceLocation: location
    azureMonitoringWorkspaceId: azureMonitoringWorkspaceId
    hcpAzureMonitoringWorkspaceId: hcpAzureMonitoringWorkspaceId
    aksClusterName: aksClusterName
    prometheusPrincipalId: mi.getManagedIdentityByName(managedIdentities.outputs.managedIdentities, 'prometheus').uamiPrincipalID
  }
  dependsOn: [
    mgmtCluster
  ]
}

//
// K E Y V A U L T S
//

module logsMgmtKeyVaultAccess '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: guid(mgmtKeyVaultName, logsMSI, 'certuser')
  params: {
    keyVaultName: mgmtKeyVaultName
    roleName: 'Key Vault Certificate User'
    managedIdentityPrincipalIds: [
      mi.getManagedIdentityByName(managedIdentities.outputs.managedIdentities, logsMSI).uamiPrincipalID
    ]
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
      managedIdentityPrincipalIds: [mgmtCluster.outputs.aksClusterKeyVaultSecretsProviderPrincipalId]
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
      managedIdentityPrincipalIds: [mgmtCluster.outputs.aksClusterKeyVaultSecretsProviderPrincipalId]
    }
  }
]

resource mgmtKeyVault 'Microsoft.KeyVault/vaults@2024-04-01-preview' existing = {
  name: mgmtKeyVaultName
}

//
//   G E N E V A   C E R T I F I C A T E
//

module genevaRPCertificate '../modules/keyvault/key-vault-cert-with-access.bicep' = if (genevaManageCertificates) {
  name: 'geneva-mgmt-rp-certificate'
  params: {
    keyVaultName: mgmtKeyVaultName
    kvCertOfficerManagedIdentityResourceId: globalMSIId
    certDomain: genevaCertificateDomain
    certificateIssuer: genevaCertificateIssuer
    hostName: genevaRpLogsName
    keyVaultCertificateName: genevaRpLogsName
    certificateAccessManagedIdentityPrincipalId: mgmtCluster.outputs.aksClusterKeyVaultSecretsProviderPrincipalId
  }
}

module genevaClusterLogCertificate '../modules/keyvault/key-vault-cert-with-access.bicep' = if (genevaManageCertificates) {
  name: 'geneva-cluster-log-certificate'
  params: {
    keyVaultName: mgmtKeyVaultName
    kvCertOfficerManagedIdentityResourceId: globalMSIId
    certDomain: genevaCertificateDomain
    certificateIssuer: genevaCertificateIssuer
    hostName: genevaClusterLogsName
    keyVaultCertificateName: genevaClusterLogsName
    certificateAccessManagedIdentityPrincipalId: mgmtCluster.outputs.aksClusterKeyVaultSecretsProviderPrincipalId
  }
}

//
//   M A E S T R O
//

var effectiveMaestroCertDomain = !empty(maestroCertDomain) ? maestroCertDomain : 'maestro.${regionalSvcDNSZoneName}'

module maestroConsumer '../modules/maestro/maestro-consumer.bicep' = if (maestroEventGridNamespaceId != '') {
  name: 'maestro-consumer'
  params: {
    maestroAgentManagedIdentityPrincipalId: mi.getManagedIdentityByName(
      managedIdentities.outputs.managedIdentities,
      'maestro-consumer'
    ).uamiPrincipalID
    maestroConsumerName: maestroConsumerName
    maestroEventGridNamespaceId: maestroEventGridNamespaceId
    certKeyVaultName: mgmtKeyVaultName
    keyVaultOfficerManagedIdentityName: globalMSIId
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
    subnetIds: [nodeSubnetCreation.outputs.subnetId]
    privateLinkServiceId: maestroEventGridNamespaceId
    vnetId: vnetCreation.outputs.vnetId
    serviceType: 'eventgrid'
    groupId: 'topicspace'
  }
}

//
// O A D P  B A C K U P S
//

module hcpBackupsRbac '../modules/hcp-backups/storage-rbac.bicep' = {
  name: 'hcp-backups-rbac'
  params: {
    storageAccountName: hcpBackupsStorageAccountName
    veleroManagedIdentityPrincipalId: mi.getManagedIdentityByName(managedIdentities.outputs.managedIdentities, 'velero').uamiPrincipalID
    oadpControllerManagedIdentityPrincipalId: mi.getManagedIdentityByName(
      managedIdentities.outputs.managedIdentities,
      'openshift-adp-controller-manager'
    ).uamiPrincipalID
  }
}
