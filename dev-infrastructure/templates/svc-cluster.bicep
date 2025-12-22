import {
  csvToArray
  determineZoneRedundancy
  determineZoneRedundancyForRegion
  getLocationAvailabilityZonesCSV
} from '../modules/common.bicep'
import * as res from '../modules/resource.bicep'
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

@description('The resource ID of the OCP ACR')
param ocpAcrResourceId string

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

@description('Istio control plane versions to use with AKS. CSV format')
param istioVersions string

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

@description('The name of the Istio Ingress Gateway IP address resource')
param istioIngressGatewayIPAddressName string = ''

@description('IPTags to be set on the Istio Ingress Gateway IP address in the format of ipTagType:tag,ipTagType:tag')
param istioIngressGatewayIPAddressIPTags string = ''

// TODO: When the work around workload identity for the RP is finalized, change this to true
@description('disableLocalAuth for the ARO HCP RP CosmosDB')
param disableLocalAuth bool

@description('Deploy ARO HCP RP Azure Cosmos DB if true')
param deployFrontendCosmos bool

@description('The name of the Cosmos DB for the RP')
param rpCosmosDbName string

@description('If true, make the Cosmos DB instance private')
param rpCosmosDbPrivate bool

@description('The zone redundant mode of Cosmos DB instance')
param rpCosmosZoneRedundantMode string

@description('The resourcegroup for regional infrastructure')
param regionalResourceGroup string

@description('The domain to use to use for the maestro certificate. Relevant only for environments where OneCert can be used.')
param maestroCertDomain string

@description('The issuer of the maestro certificate.')
param maestroCertIssuer string

@description('The name of the eventgrid namespace for Maestro.')
param maestroEventGridNamespacesName string

@description('Deploy CS Postgres if true')
param csPostgresDeploy bool

@description('The zone redundant mode of the Maestro Postgres Database')
param csPostgresZoneRedundantMode string

@description('The number of days to retain backups for the CS Postgres server')
param csPostgresBackupRetentionDays int

@description('Enable geo-redundant backups for the CS Postgres server')
param csPostgresGeoRedundantBackup bool

@description('The name of the Postgres server for CS')
@maxLength(60)
param csPostgresServerName string

@description('The name of the CS Postgres database')
param csPostgresDatabaseName string

@description('The minimum TLS version for the Postgres server for CS')
param csPostgresServerMinTLSVersion string

@description('The version of the Postgres server for CS')
param csPostgresServerVersion string

@description('The size of the Postgres server for CS')
param csPostgresServerStorageSizeGB int

@description('If true, make the CS Postgres instance private')
param clusterServicePostgresPrivate bool = true

@description('Deploy ARO HCP Maestro Postgres if true')
param deployMaestroPostgres bool = true

@description('The zone redundant mode of the Maestro Postgres Database')
param maestroPostgresZoneRedundantMode string

@description('The number of days to retain backups for the Maestro Postgres server')
param maestroPostgresBackupRetentionDays int

@description('Enable geo-redundant backups for the Maestro Postgres server')
param maestroPostgresGeoRedundantBackup bool

@description('If true, make the Maestro Postgres instance private')
param maestroPostgresPrivate bool = true

@description('The name of the Postgres server for Maestro')
@maxLength(60)
param maestroPostgresServerName string

@description('The version of the Postgres server for Maestro')
param maestroPostgresServerVersion string

@description('The minimum TLS version for the Postgres server for Maestro')
param maestroPostgresServerMinTLSVersion string

@description('The size of the Postgres server for Maestro')
param maestroPostgresServerStorageSizeGB int

@description('The name of the Maestro Postgres database')
param maestroPostgresDatabaseName string

@description('The name of Maestro Server MQTT client')
param maestroServerMqttClientName string

@description('The name of the maestro managed identity')
param maestroMIName string

@description('The namespace of the maestro managed identity')
param maestroNamespace string

@description('The service account name of the maestro managed identity')
param maestroServiceAccountName string

@description('The name of the service keyvault')
param serviceKeyVaultName string

@description('The name of the resourcegroup for the service keyvault')
param serviceKeyVaultResourceGroup string = resourceGroup().name

@description('OIDC Storage Account name')
param oidcStorageAccountName string

@description('The location of the OIDC storage account private link')
param oidcStoragePrivateLinkLocation string

@description('Whether the OIDC storage account is public or private. If private, it can only be accessed via Azure Front Door')
param oidcStorageAccountPublic bool

@description('The zone redundant mode of the OIDC storage account')
param oidcZoneRedundantMode string

@description('The name of the global Azure Front Door profile fronting the OIDC storage account')
param azureFrontDoorResourceId string

@description('The name of the global Azure Front Door parent DNS zone')
param azureFrontDoorParentDnsZoneName string

@description('The regional subdomain for the Azure Front Door')
param azureFrontDoorRegionalSubdomain string

@description('The name of the Azure Front Door global Key Vault')
param azureFrontDoorKeyVaultName string

@description('The tag key for the Azure Front Door Key Vault')
param azureFrontDoorKeyTagKey string

@description('The tag value for the Azure Front Door Key Vault')
param azureFrontDoorKeyTagValue string

@description('Whether to use managed certificates for the Azure Front Door')
param azureFrontDoorUseManagedCertificates bool

@description('Whether to manage the Azure Front Door integration with the OIDC storage account')
param azureFrontDoorManage bool

@description('MSI that will be used to run the deploymentScript')
param globalMSIId string

@description('The parent SVC DNS zone name')
param svcDNSZoneName string

@description('The regional DNS zone to hold ARO HCP customer cluster DNS records')
param regionalCXDNSZoneName string

@description('This is a regional DNS zone name to hold records for ARO HCP service components, e.g. the RP')
param regionalSvcDNSZoneName string

@description('Frontend Ingress Certificate Name')
param frontendIngressCertName string

@description('Frontend Ingress Certificate Issuer')
param frontendIngressCertIssuer string

@description('The name of the frontend managed identity')
param frontendMIName string

@description('The namespace of the frontend managed identity')
param frontendNamespace string

@description('The service account name of the frontend managed identity')
param frontendServiceAccountName string

@description('The name of the backend managed identity')
param backendMIName string

@description('The namespace of the backend managed identity')
param backendNamespace string

@description('The service account name of the backend managed identity')
param backendServiceAccountName string

@description('The name of the FPA certificate in the SVC keyvault')
param fpaCertificateName string

@description('The issuer of the FPA certificate')
param fpaCertificateIssuer string

@description('Whether to create the FPA certificate in the SVC keyvault')
param manageFpaCertificate bool

@description('The service tag for Geneva Actions')
param genevaActionsServiceTag string

@description('The Azure Resource ID of the Azure Monitor Workspace (stores prometheus metrics)')
param azureMonitoringWorkspaceId string

@description('The Grafana resource ID')
param grafanaResourceId string

@description('The Grafana managed identity principal ID')
param grafanaPrincipalId string

@description('The name of the CS managed identity')
param csMIName string

@description('The namespace of the CS managed identity')
param csNamespace string

@description('The service account name of the CS managed identity')
param csServiceAccountName string

@description('The name of the MSI refresher managed identity')
param msiRefresherMIName string

@description('The namespace of the MSI refresher managed identity')
param msiRefresherNamespace string

@description('The service account name of the MSI refresher managed identity')
param msiRefresherServiceAccountName string

// logs
@description('The namespace of the logs')
param logsNamespace string

@description('The managed identity name of the logs')
param logsMSI string

@description('The service account name of the logs managed identity')
param logsServiceAccount string

@description('Tha name of the SVC NSP')
param svcNSPName string

@description('Access mode for this NSP')
param svcNSPAccessMode string

@description('Access mode for this NSP')
param serviceKeyVaultAsignNSP bool = true

@description('Domain used for creation of geneva auth certificates')
param genevaCertificateDomain string

@description('Issuer of certificate for Geneva Authentication')
param genevaCertificateIssuer string = 'Self'

@description('Name of certificate in Keyvault and hostname used in SAN')
param genevaRpLogsName string

@description('Should geneva certificates be managed')
param genevaManageCertificates bool

@description('The name of the Admin API managed identity')
param adminApiMIName string

@description('The namespace of the Admin API managed identity')
param adminApiNamespace string

@description('The service account name of the Admin API managed identity')
param adminApiServiceAccountName string

@description('The name of the Admin API certificate')
param adminApiIngressCertName string

@description('The issuer of the Admin API certificate')
param adminApiIngressCertIssuer string

@description('The cluster tag value for the owning team')
param owningTeamTagValue string

resource serviceKeyVault 'Microsoft.KeyVault/vaults@2024-04-01-preview' existing = {
  name: serviceKeyVaultName
  scope: resourceGroup(serviceKeyVaultResourceGroup)
}

//
//   M A N A G E D   I D E N T I T I E S
//

var workloadIdentities = items({
  frontend_wi: {
    uamiName: frontendMIName
    namespace: frontendNamespace
    serviceAccountName: frontendServiceAccountName
  }
  backend_wi: {
    uamiName: backendMIName
    namespace: backendNamespace
    serviceAccountName: backendServiceAccountName
  }
  billing_wi: {
    uamiName: 'aro-billing'
    namespace: 'billing'
    serviceAccountName: 'aro-billing'
  }
  maestro_wi: {
    uamiName: maestroMIName
    namespace: maestroNamespace
    serviceAccountName: maestroServiceAccountName
  }
  cs_wi: {
    uamiName: csMIName
    namespace: csNamespace
    serviceAccountName: csServiceAccountName
  }
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
  msi_refresher_wi: {
    uamiName: msiRefresherMIName
    namespace: msiRefresherNamespace
    serviceAccountName: msiRefresherServiceAccountName
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

resource svcClusterNSG 'Microsoft.Network/networkSecurityGroups@2023-11-01' = {
  location: location
  name: 'svc-cluster-node-nsg'
  properties: {
    securityRules: [
      {
        name: 'rp-in-arm'
        properties: {
          access: 'Allow'
          destinationAddressPrefix: '*'
          destinationPortRange: '443'
          direction: 'Inbound'
          priority: 120
          protocol: 'Tcp'
          sourceAddressPrefix: 'AzureResourceManager'
          sourcePortRange: '*'
        }
      }
      {
        name: 'admin-in-geneva'
        properties: {
          access: 'Allow'
          destinationAddressPrefix: '*'
          destinationPortRange: '443'
          direction: 'Inbound'
          priority: 130
          protocol: 'Tcp'
          sourceAddressPrefix: genevaActionsServiceTag
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
    enableSwift: false
    deploymentMsiId: globalMSIId
  }
}

module nodeSubnetCreation '../modules/network/aks-node-subnet.bicep' = {
  name: 'subnet-${nodeSubnetName}-creation'
  params: {
    vnetName: vnetName
    subnetName: nodeSubnetName
    subnetNSGId: svcClusterNSG.id
    subnetPrefix: subnetPrefix
  }
  dependsOn: [
    vnetCreation
  ]
}

module svcCluster '../modules/aks-cluster-base.bicep' = {
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
    deployIstio: true
    istioVersions: split(istioVersions, ',')
    istioIngressGatewayIPAddressName: istioIngressGatewayIPAddressName
    istioIngressGatewayIPAddressIPTags: istioIngressGatewayIPAddressIPTags
    vnetName: vnetName
    nodeSubnetId: nodeSubnetCreation.outputs.subnetId
    podSubnetPrefix: podSubnetPrefix
    clusterType: 'svc-cluster'
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
    workloadIdentities: workloadIdentities
    aksKeyVaultName: aksKeyVaultName
    aksKeyVaultTagName: aksKeyVaultTagName
    aksKeyVaultTagValue: aksKeyVaultTagValue
    pullAcrResourceIds: [svcAcrResourceId]
    deploymentMsiId: globalMSIId
    enableSwiftV2Nodepools: false
    owningTeamTagValue: owningTeamTagValue
  }
  dependsOn: [
    managedIdentities
  ]
}

output aksClusterName string = svcCluster.outputs.aksClusterName

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
    svcCluster
  ]
}

var frontendMI = mi.getManagedIdentityByName(managedIdentities.outputs.managedIdentities, frontendMIName)
var backendMI = mi.getManagedIdentityByName(managedIdentities.outputs.managedIdentities, backendMIName)
var adminApiMI = mi.getManagedIdentityByName(managedIdentities.outputs.managedIdentities, adminApiMIName)

module rpCosmosDb '../modules/rp-cosmos.bicep' = if (deployFrontendCosmos) {
  name: 'rp_cosmos_db'
  scope: resourceGroup(regionalResourceGroup)
  params: {
    name: rpCosmosDbName
    location: location
    zoneRedundant: determineZoneRedundancy(locationAvailabilityZoneList, rpCosmosZoneRedundantMode)
    disableLocalAuth: disableLocalAuth
    userAssignedMIs: [frontendMI, backendMI]
    readOnlyUserAssignedMIs: [adminApiMI]
    private: rpCosmosDbPrivate
  }
}

module rpCosmosdbPrivateEndpoint '../modules/private-endpoint.bicep' = if (deployFrontendCosmos) {
  name: 'rp-pe-${uniqueString(deployment().name)}'
  params: {
    location: location
    subnetIds: [nodeSubnetCreation.outputs.subnetId]
    vnetId: vnetCreation.outputs.vnetId
    privateLinkServiceId: rpCosmosDb.outputs.cosmosDBAccountId
    serviceType: 'cosmosdb'
    groupId: 'Sql'
  }
}

output cosmosDBName string = deployFrontendCosmos ? rpCosmosDb.outputs.cosmosDBName : ''
output frontend_mi_client_id string = frontendMI.uamiClientID

//
//   M A E S T R O
//

var effectiveMaestroCertDomain = !empty(maestroCertDomain) ? maestroCertDomain : 'maestro.${regionalSvcDNSZoneName}'

module maestroServer '../modules/maestro/maestro-server.bicep' = {
  name: 'maestro-server'
  params: {
    regionalResourceGroup: regionalResourceGroup
    maestroInfraResourceGroup: regionalResourceGroup
    maestroEventGridNamespaceName: maestroEventGridNamespacesName
    mqttClientName: maestroServerMqttClientName
    certKeyVaultName: serviceKeyVaultName
    certKeyVaultResourceGroup: serviceKeyVaultResourceGroup
    keyVaultOfficerManagedIdentityName: globalMSIId
    maestroCertificateDomain: effectiveMaestroCertDomain
    maestroCertificateIssuer: maestroCertIssuer
    deployPostgres: deployMaestroPostgres
    postgresServerName: maestroPostgresServerName
    postgresServerVersion: maestroPostgresServerVersion
    postgresServerMinTLSVersion: maestroPostgresServerMinTLSVersion
    postgresServerStorageSizeGB: maestroPostgresServerStorageSizeGB
    postgresZoneRedundantMode: determineZoneRedundancyForRegion(location, maestroPostgresZoneRedundantMode)
      ? 'ZoneRedundant'
      : 'SameZone'
    postgresBackupRetentionDays: maestroPostgresBackupRetentionDays
    postgresGeoRedundantBackup: maestroPostgresGeoRedundantBackup
    privateEndpointSubnetId: nodeSubnetCreation.outputs.subnetId
    privateEndpointVnetId: vnetCreation.outputs.vnetId
    privateEndpointResourceGroup: resourceGroup().name
    maestroDatabaseName: maestroPostgresDatabaseName
    postgresServerPrivate: maestroPostgresPrivate
    postgresAdministrationManagedIdentityId: globalMSIId
    maestroServerManagedIdentityPrincipalId: mi.getManagedIdentityByName(
      managedIdentities.outputs.managedIdentities,
      maestroMIName
    ).uamiPrincipalID
    maestroServerManagedIdentityName: maestroMIName
  }
  dependsOn: [
    serviceKeyVault
  ]
}

//
//   C L U S T E R   S E R V I C E
//

var csManagedIdentityPrincipalId = mi.getManagedIdentityByName(managedIdentities.outputs.managedIdentities, csMIName).uamiPrincipalID

module cs '../modules/cluster-service.bicep' = {
  name: 'cluster-service'
  params: {
    postgresServerName: csPostgresServerName
    postgresServerMinTLSVersion: csPostgresServerMinTLSVersion
    postgresServerVersion: csPostgresServerVersion
    postgresServerStorageSizeGB: csPostgresServerStorageSizeGB
    csDatabaseName: csPostgresDatabaseName
    privateEndpointSubnetId: nodeSubnetCreation.outputs.subnetId
    privateEndpointVnetId: vnetCreation.outputs.vnetId
    privateEndpointResourceGroup: resourceGroup().name
    deployPostgres: csPostgresDeploy
    postgresZoneRedundantMode: determineZoneRedundancyForRegion(location, csPostgresZoneRedundantMode)
      ? 'ZoneRedundant'
      : 'SameZone'
    postgresBackupRetentionDays: csPostgresBackupRetentionDays
    postgresGeoRedundantBackup: csPostgresGeoRedundantBackup
    postgresServerPrivate: clusterServicePostgresPrivate
    clusterServiceManagedIdentityPrincipalId: csManagedIdentityPrincipalId
    clusterServiceManagedIdentityName: csMIName
    serviceKeyVaultName: serviceKeyVault.name
    serviceKeyVaultResourceGroup: serviceKeyVaultResourceGroup
    regionalCXDNSZoneName: regionalCXDNSZoneName
    regionalResourceGroup: regionalResourceGroup
    ocpAcrResourceId: ocpAcrResourceId
    postgresAdministrationManagedIdentityId: globalMSIId
  }
  dependsOn: csPostgresDeploy && deployMaestroPostgres ? [maestroServer] : []
}

//
//   S V C   K E Y V A U L T   A C C E S S
//

module serviceKeyVaultSecretsUserAccess '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: 'kv-sec-user-${uniqueString(resourceGroup().name)}'
  scope: resourceGroup(serviceKeyVaultResourceGroup)
  params: {
    keyVaultName: serviceKeyVaultName
    roleName: 'Key Vault Secrets User'
    managedIdentityPrincipalIds: [csManagedIdentityPrincipalId, backendMI.uamiPrincipalID, adminApiMI.uamiPrincipalID]
  }
}

module serviceKeyVaultCertUserAccess '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: 'kv-cert-user-${uniqueString(resourceGroup().name)}'
  scope: resourceGroup(serviceKeyVaultResourceGroup)
  params: {
    keyVaultName: serviceKeyVaultName
    roleName: 'Key Vault Certificate User'
    managedIdentityPrincipalIds: [
      mi.getManagedIdentityByName(managedIdentities.outputs.managedIdentities, logsMSI).uamiPrincipalID
    ]
  }
}

//
//   D N S   A C C E S S
//

module cxDnsZoneContributor '../modules/dns/zone-contributor.bicep' = {
  name: 'cs-dns-zone-contributor'
  scope: resourceGroup(regionalResourceGroup)
  params: {
    zoneName: regionalCXDNSZoneName
    zoneContributerManagedIdentityPrincipalIds: [csManagedIdentityPrincipalId, backendMI.uamiPrincipalID]
  }
}

//
//   O C P   A C R   P E R M I S S I O N S
//

var ocpAcrRef = res.acrRefFromId(ocpAcrResourceId)
module acrManageTokenRole '../modules/acr/acr-permissions.bicep' = {
  name: 'ocp-acr-manage-tokens-${uniqueString(resourceGroup().name)}'
  scope: resourceGroup(ocpAcrRef.resourceGroup.subscriptionId, ocpAcrRef.resourceGroup.name)
  params: {
    principalIds: [csManagedIdentityPrincipalId, backendMI.uamiPrincipalID]
    grantManageTokenAccess: true
    acrName: ocpAcrRef.name
  }
}

//
//   O I D C
//

var frontDoorRef = res.frontdoorProfileRefFromId(azureFrontDoorResourceId)

// Grant Grafana permissions to query AFD metrics directly from Azure Monitor
// This enables real-time AFD metrics visualization in Grafana dashboards
module grafanaAfdPermissions '../modules/grafana/observability-permissions.bicep' = {
  name: 'grafana-afd-permissions'
  scope: resourceGroup(frontDoorRef.resourceGroup.subscriptionId, frontDoorRef.resourceGroup.name)
  params: {
    grafanaPrincipalId: grafanaPrincipalId
    frontDoorProfileId: azureFrontDoorResourceId
  }
}

module oidc '../modules/oidc/region/main.bicep' = {
  name: 'oidc-storage'
  scope: resourceGroup(regionalResourceGroup)
  params: {
    gblRgName: frontDoorRef.resourceGroup.name
    gblSubscription: frontDoorRef.resourceGroup.subscriptionId
    location: location
    zoneName: azureFrontDoorParentDnsZoneName
    frontDoorProfileName: frontDoorRef.name
    storageAccountName: oidcStorageAccountName
    customDomainName: azureFrontDoorRegionalSubdomain
    routeName: azureFrontDoorRegionalSubdomain
    originGroupName: azureFrontDoorRegionalSubdomain
    originName: azureFrontDoorRegionalSubdomain
    privateLinkLocation: oidcStoragePrivateLinkLocation
    storageAccountAccessPrincipalIds: [csManagedIdentityPrincipalId, backendMI.uamiPrincipalID]
    skuName: determineZoneRedundancy(locationAvailabilityZoneList, oidcZoneRedundantMode)
      ? 'Standard_ZRS'
      : 'Standard_LRS'
    keyVaultName: azureFrontDoorKeyVaultName
    useManagedCertificates: azureFrontDoorUseManagedCertificates
    globalMSIId: globalMSIId
    deploymentScriptLocation: location
    storageAccountBlobPublicAccess: oidcStorageAccountPublic
    frontDoorManage: azureFrontDoorManage
  }
}

//
//  E V E N T   G R I D   P R I V A T E   E N D P O I N T   C O N N E C T I O N
//

resource eventGridNamespace 'Microsoft.EventGrid/namespaces@2024-06-01-preview' existing = {
  name: maestroEventGridNamespacesName
  scope: resourceGroup(regionalResourceGroup)
}

// todo manage only if maestro.eventgrid is not set to private
module eventGrindPrivateEndpoint '../modules/private-endpoint.bicep' = {
  name: 'eventGridPrivateEndpoint'
  params: {
    location: location
    subnetIds: [nodeSubnetCreation.outputs.subnetId]
    privateLinkServiceId: eventGridNamespace.id
    serviceType: 'eventgrid'
    groupId: 'topicspace'
    vnetId: vnetCreation.outputs.vnetId
  }
}

//
//   F R O N T E N D
//

var frontendDnsName = 'rp'
var frontendDnsFQDN = '${frontendDnsName}.${regionalSvcDNSZoneName}'

module frontendIngressCert '../modules/keyvault/key-vault-cert.bicep' = {
  name: 'frontend-cert-${uniqueString(resourceGroup().name)}'
  scope: resourceGroup(serviceKeyVaultResourceGroup)
  params: {
    keyVaultName: serviceKeyVaultName
    subjectName: 'CN=${frontendDnsFQDN}'
    certName: frontendIngressCertName
    keyVaultManagedIdentityId: globalMSIId
    dnsNames: [
      frontendDnsFQDN
    ]
    issuerName: frontendIngressCertIssuer
  }
}

module frontendIngressCertCSIAccess '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: 'aksClusterKeyVaultSecretsProviderMI-${frontendIngressCertName}'
  scope: resourceGroup(serviceKeyVaultResourceGroup)
  params: {
    keyVaultName: serviceKeyVaultName
    roleName: 'Key Vault Secrets User'
    managedIdentityPrincipalIds: [svcCluster.outputs.aksClusterKeyVaultSecretsProviderPrincipalId]
    secretName: frontendIngressCertName
  }
}

module frontendDNS '../modules/dns/a-record.bicep' = {
  name: 'frontend-dns'
  scope: resourceGroup(regionalResourceGroup)
  params: {
    zoneName: regionalSvcDNSZoneName
    recordName: frontendDnsName
    ipAddress: svcCluster.outputs.istioIngressGatewayIPAddress
    ttl: 300
  }
}

//
//   A D M I N   A P I
//

var adminApiDnsName = 'admin'
var adminApiDnsFQDN = '${adminApiDnsName}.${regionalSvcDNSZoneName}'

module adminApiCert '../modules/keyvault/key-vault-cert.bicep' = {
  name: 'admin-api-cert-${uniqueString(resourceGroup().name)}'
  scope: resourceGroup(serviceKeyVaultResourceGroup)
  params: {
    keyVaultName: serviceKeyVaultName
    subjectName: 'CN=${adminApiDnsFQDN}'
    certName: adminApiIngressCertName
    keyVaultManagedIdentityId: globalMSIId
    dnsNames: [
      adminApiDnsFQDN
    ]
    issuerName: adminApiIngressCertIssuer
  }
}

module adminApiIngressCertCSIAccess '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: 'aksClusterKeyVaultSecretsProviderMI-${adminApiIngressCertName}'
  scope: resourceGroup(serviceKeyVaultResourceGroup)
  params: {
    keyVaultName: serviceKeyVaultName
    roleName: 'Key Vault Secrets User'
    managedIdentityPrincipalIds: [svcCluster.outputs.aksClusterKeyVaultSecretsProviderPrincipalId]
    secretName: adminApiIngressCertName
  }
}

module adminApiDNS '../modules/dns/a-record.bicep' = {
  name: 'admin-api-dns'
  scope: resourceGroup(regionalResourceGroup)
  params: {
    zoneName: regionalSvcDNSZoneName
    recordName: adminApiDnsName
    ipAddress: svcCluster.outputs.istioIngressGatewayIPAddress
    ttl: 300
  }
}

//
//   F P A   C E R T I F I C A T E
//

var fpaCertificateSNI = '${fpaCertificateName}.${svcDNSZoneName}'

module fpaCertificate '../modules/keyvault/key-vault-cert.bicep' = if (manageFpaCertificate) {
  name: 'fpa-certificate-${uniqueString(resourceGroup().name)}'
  scope: resourceGroup(serviceKeyVaultResourceGroup)
  params: {
    keyVaultName: serviceKeyVaultName
    subjectName: 'CN=${fpaCertificateSNI}'
    certName: fpaCertificateName
    keyVaultManagedIdentityId: globalMSIId
    dnsNames: [
      fpaCertificateSNI
    ]
    issuerName: fpaCertificateIssuer
  }
}

//
//   G E N E V A   C E R T I F I C A T E
//

module genevaRPCertificate '../modules/keyvault/key-vault-cert-with-access.bicep' = if (genevaManageCertificates) {
  name: 'geneva-rp-certificate-${uniqueString(resourceGroup().name)}'
  scope: resourceGroup(serviceKeyVaultResourceGroup)
  params: {
    keyVaultName: serviceKeyVaultName
    kvCertOfficerManagedIdentityResourceId: globalMSIId
    certDomain: genevaCertificateDomain
    certificateIssuer: genevaCertificateIssuer
    hostName: genevaRpLogsName
    keyVaultCertificateName: genevaRpLogsName
    certificateAccessManagedIdentityPrincipalId: svcCluster.outputs.aksClusterKeyVaultSecretsProviderPrincipalId
  }
}

//
//   N E T W O R K    S E C U R I T Y    P E R I M E T E R
//

module svcNSP '../modules/network/nsp.bicep' = {
  name: 'nsp-${uniqueString(resourceGroup().name)}'
  params: {
    nspName: svcNSPName
    location: location
  }
}

var nspAssociatedResources = deployFrontendCosmos
  ? [
      svcCluster.outputs.etcKeyVaultId
      rpCosmosDb.outputs.cosmosDBAccountId
    ]
  : [
      svcCluster.outputs.etcKeyVaultId
    ]

module svcClusterNSPProfile '../modules/network/nsp-profile.bicep' = {
  name: 'profile-${uniqueString(resourceGroup().name)}'
  params: {
    accessMode: svcNSPAccessMode
    nspName: svcNSPName
    profileName: svcNSPName
    location: location
    associatedResources: nspAssociatedResources
    // TODO Add EV2 access here
    subscriptions: [
      subscription().id
    ]
  }
  dependsOn: [
    svcNSP
  ]
}

module svcKVNSPProfile '../modules/network/nsp-profile.bicep' = if (serviceKeyVaultAsignNSP) {
  name: 'profile-svc-kv-${uniqueString(resourceGroup().name)}'
  params: {
    accessMode: svcNSPAccessMode
    nspName: svcNSPName
    profileName: '${svcNSPName}-svc-kv'
    location: location
    associatedResources: [
      serviceKeyVault.id
    ]
    // TODO Add EV2 access here
    subscriptions: [
      subscription().id
    ]
  }
  dependsOn: [
    svcNSP
  ]
}
