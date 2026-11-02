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

@description('Node subnet resource ID from svc-cluster-infra.')
param nodeSubnetId string

@description('VNet resource ID from svc-cluster-infra.')
param vnetId string

@description('Frontend ingress public IP from svc-cluster-infra.')
param istioIngressGatewayIP string

@description('Operations ingress public IP from svc-cluster-infra.')
param opsIngressGatewayIP string

@description('The resource ID of the OCP ACR')
param ocpAcrResourceId string

@description('The resource ID of the SVC ACR')
param svcAcrResourceId string

@description('The name of the keyvault for AKS.')
@maxLength(24)
param aksKeyVaultName string

@description('The name of the Cosmos DB for the RP')
param rpCosmosDbName string

@description('If true, make the Cosmos DB instance private')
param rpCosmosDbPrivate bool

@description('The resource ID of the Cosmos DB account for the RP')
param rpCosmosDbAccountId string

@description('The resourcegroup for regional infrastructure')
param regionalResourceGroup string

@description('The name of the eventgrid namespace for Maestro.')
param maestroEventGridNamespacesName string

@description('The SAN and CN for the Maestro server EventGrid certificate.')
param maestroServerCertSAN string

@description('The issuer of the Maestro certificate.')
param maestroCertIssuer string

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

@description('The SKU of the Postgres server for CS')
param csPostgresServerSku string

@description('Enable enhanced metrics for the CS PostgreSQL server')
param csPostgresEnhancedMetricsEnabled bool

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

@description('The SKU of the Postgres server for Maestro')
param maestroPostgresServerSku string

@description('The name of the Maestro Postgres database')
param maestroPostgresDatabaseName string

@description('The name of Maestro Server MQTT client')
param maestroServerMqttClientName string

@description('Enable enhanced metrics for the Maestro PostgreSQL server')
param maestroPostgresEnhancedMetricsEnabled bool

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

@description('The subscription ID where the service keyvault resource group lives. Defaults to the current subscription. Set when the keyvault is shared across subscriptions.')
param serviceKeyVaultSubscription string = subscription().subscriptionId

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

@description('The SAN and CN for the frontend ingress certificate')
param frontendIngressCertSAN string

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

@description('The Azure Resource ID of the Azure Monitor Workspace (stores prometheus metrics)')
param azureMonitoringWorkspaceId string

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

@description('Name of certificate in Keyvault and hostname used in SAN')
param genevaRpLogsName string

@description('The name of the Admin API managed identity')
param adminApiMIName string

@description('The namespace of the Admin API managed identity')
param adminApiNamespace string

@description('The service account name of the Admin API managed identity')
param adminApiServiceAccountName string

@description('The name of the Admin API certificate')
param adminApiIngressCertName string

@description('The SAN and CN for the admin API ingress certificate')
param adminApiIngressCertSAN string

@description('The name of the Fleet managed identity')
param fleetMIName string

@description('The namespace of the Fleet managed identity')
param fleetNamespace string

@description('The service account name of the Fleet managed identity')
param fleetServiceAccountName string

@description('CoscmosDB autoscaling parameters')
param resourceContainerMaxScale int
param billingContainerMaxScale int
param locksContainerMaxScale int
param fleetContainerMaxScale int
param azureResourcesContainerMaxScale int

@description('The name of the Exporter managed identity')
param exporterMIName string

@description('The namespace of the Exporter managed identity')
param exporterNamespace string

@description('The service account name of the Exporter managed identity')
param exporterServiceAccountName string

@description('Event Hub name for AKS audit logs')
param auditLogsEventHubName string

@description('Resource ID of the event hub authorization rule for AKS audit logs')
param auditLogsEventHubAuthRuleId string

@description('The name of the Session Gate managed identity')
param sessiongateMIName string

@description('The namespace of the Session Gate managed identity')
param sessiongateNamespace string

@description('The service account name of the Session Gate managed identity')
param sessiongateServiceAccountName string

@description('The name of the Session Gate ingress certificate')
param sessiongateIngressCertName string

@description('The SAN and CN for the sessiongate ingress certificate')
param sessiongateIngressCertSAN string

@description('Reference pre-created infrastructure identities instead of creating identities in the service resource group.')
param useLeasedInfrastructureIdentities bool = false

@description('Resource group containing the pre-created service infrastructure identities in the service deployment subscription.')
param infrastructureIdentityResourceGroup string = ''

resource serviceKeyVault 'Microsoft.KeyVault/vaults@2024-04-01-preview' existing = {
  name: serviceKeyVaultName
  scope: resourceGroup(serviceKeyVaultSubscription, serviceKeyVaultResourceGroup)
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
  sessiongate_wi: {
    uamiName: sessiongateMIName
    namespace: sessiongateNamespace
    serviceAccountName: sessiongateServiceAccountName
  }
  exporter_wi: {
    uamiName: exporterMIName
    namespace: exporterNamespace
    serviceAccountName: exporterServiceAccountName
  }
  fleet_wi: {
    uamiName: fleetMIName
    namespace: fleetNamespace
    serviceAccountName: fleetServiceAccountName
  }
})

module managedIdentities '../modules/managed-identities.bicep' = {
  name: 'managed-identities'
  params: {
    location: location
    manageIdentityNames: [for wi in workloadIdentities: wi.value.uamiName]
    useLeasedIdentities: useLeasedInfrastructureIdentities
    identityResourceGroupName: infrastructureIdentityResourceGroup
  }
}

// The Go cluster-create step owns the cluster and pools. This deployment owns
// resources that consume the cluster's live identities and OIDC issuer.
resource existingSvcCluster 'Microsoft.ContainerService/managedClusters@2026-04-02-preview' existing = {
  name: aksClusterName
}

var aksClusterKeyVaultSecretsProviderPrincipalId = existingSvcCluster.properties.addonProfiles.azureKeyvaultSecretsProvider.identity.objectId

resource aksKeyVault 'Microsoft.KeyVault/vaults@2023-07-01' existing = {
  name: aksKeyVaultName
}

module svcCluster '../modules/aks-cluster-post.bicep' = {
  name: 'cluster-${uniqueString(resourceGroup().name)}'
  params: {
    aksClusterName: aksClusterName
    location: location
    deploymentMsiId: globalMSIId
    pullAcrResourceIds: [svcAcrResourceId]
    workloadIdentities: workloadIdentities
    useLeasedInfrastructureIdentities: useLeasedInfrastructureIdentities
    infrastructureIdentityResourceGroup: infrastructureIdentityResourceGroup
  }
  dependsOn: [
    managedIdentities
  ]
}

output aksClusterName string = aksClusterName

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
}

// Declare this service cluster in the authoritative underlay-cluster inventory (see the module
// for details). Lets absence alerts detect when this cluster stops reporting to the workspace.
module underlayClusterMetric '../modules/metrics/underlay-clusters-metric.bicep' = {
  name: 'underlay-clusters-metric'
  params: {
    azureMonitoringWorkspaceId: azureMonitoringWorkspaceId
    clusterName: aksClusterName
  }
}

var frontendMI = mi.getManagedIdentityByName(managedIdentities.outputs.managedIdentities, frontendMIName)
var backendMI = mi.getManagedIdentityByName(managedIdentities.outputs.managedIdentities, backendMIName)
var adminApiMI = mi.getManagedIdentityByName(managedIdentities.outputs.managedIdentities, adminApiMIName)
var fleetMI = mi.getManagedIdentityByName(managedIdentities.outputs.managedIdentities, fleetMIName)

module rpCosmosDb '../modules/rp-cosmos.bicep' = if (rpCosmosDbAccountId != '') {
  name: 'rp_cosmos_db'
  scope: resourceGroup(regionalResourceGroup)
  params: {
    cosmosDBAccountName: rpCosmosDbName
    userAssignedMIs: [frontendMI, backendMI, adminApiMI, fleetMI]
    csManagedIdentityPrincipalId: csManagedIdentityPrincipalId
    resourceContainerMaxScale: resourceContainerMaxScale
    billingContainerMaxScale: billingContainerMaxScale
    locksContainerMaxScale: locksContainerMaxScale
    fleetContainerMaxScale: fleetContainerMaxScale
    azureResourcesContainerMaxScale: azureResourcesContainerMaxScale
  }
}

module rpCosmosdbPrivateEndpoint '../modules/private-endpoint.bicep' = if (rpCosmosDbPrivate && rpCosmosDbAccountId != '') {
  name: 'rp-pe-${uniqueString(deployment().name)}'
  params: {
    location: location
    subnetIds: [nodeSubnetId]
    vnetId: vnetId
    privateLinkServiceId: rpCosmosDbAccountId
    serviceType: 'cosmosdb'
    groupId: 'Sql'
  }
}

output cosmosDBName string = rpCosmosDbName
output frontend_mi_client_id string = frontendMI.uamiClientID

//
//   M A E S T R O
//

module maestroServer '../modules/maestro/maestro-server.bicep' = {
  name: 'maestro-server'
  params: {
    regionalResourceGroup: regionalResourceGroup
    maestroInfraResourceGroup: regionalResourceGroup
    maestroEventGridNamespaceName: maestroEventGridNamespacesName
    mqttClientName: maestroServerMqttClientName
    certKeyVaultName: serviceKeyVaultName
    certKeyVaultResourceGroup: serviceKeyVaultResourceGroup
    certKeyVaultSubscription: serviceKeyVaultSubscription
    certificateSAN: maestroServerCertSAN
    certificateIssuer: maestroCertIssuer
    deployPostgres: deployMaestroPostgres
    postgresServerName: maestroPostgresServerName
    postgresServerVersion: maestroPostgresServerVersion
    postgresServerMinTLSVersion: maestroPostgresServerMinTLSVersion
    postgresServerStorageSizeGB: maestroPostgresServerStorageSizeGB
    postgresServerSku: maestroPostgresServerSku
    postgresZoneRedundantMode: determineZoneRedundancyForRegion(location, maestroPostgresZoneRedundantMode)
      ? 'ZoneRedundant'
      : 'SameZone'
    postgresBackupRetentionDays: maestroPostgresBackupRetentionDays
    postgresGeoRedundantBackup: maestroPostgresGeoRedundantBackup
    privateEndpointSubnetId: nodeSubnetId
    privateEndpointVnetId: vnetId
    privateEndpointResourceGroup: resourceGroup().name
    maestroDatabaseName: maestroPostgresDatabaseName
    postgresServerPrivate: maestroPostgresPrivate
    postgresAdministrationManagedIdentityId: globalMSIId
    postgresEnhancedMetricsEnabled: maestroPostgresEnhancedMetricsEnabled
    maestroServerManagedIdentityPrincipalId: mi.getManagedIdentityByName(
      managedIdentities.outputs.managedIdentities,
      maestroMIName
    ).uamiPrincipalID
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
    postgresServerSku: csPostgresServerSku
    postgresEnhancedMetricsEnabled: csPostgresEnhancedMetricsEnabled
    csDatabaseName: csPostgresDatabaseName
    privateEndpointSubnetId: nodeSubnetId
    privateEndpointVnetId: vnetId
    privateEndpointResourceGroup: resourceGroup().name
    deployPostgres: csPostgresDeploy
    postgresZoneRedundantMode: determineZoneRedundancyForRegion(location, csPostgresZoneRedundantMode)
      ? 'ZoneRedundant'
      : 'SameZone'
    postgresBackupRetentionDays: csPostgresBackupRetentionDays
    postgresGeoRedundantBackup: csPostgresGeoRedundantBackup
    postgresServerPrivate: clusterServicePostgresPrivate
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
  scope: resourceGroup(serviceKeyVaultSubscription, serviceKeyVaultResourceGroup)
  params: {
    keyVaultName: serviceKeyVaultName
    roleName: 'Key Vault Secrets User'
    managedIdentityPrincipalIds: [csManagedIdentityPrincipalId, backendMI.uamiPrincipalID, adminApiMI.uamiPrincipalID]
  }
}

module serviceKeyVaultCertUserAccess '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: 'kv-cert-user-${uniqueString(resourceGroup().name)}'
  scope: resourceGroup(serviceKeyVaultSubscription, serviceKeyVaultResourceGroup)
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
    subnetIds: [nodeSubnetId]
    privateLinkServiceId: eventGridNamespace.id
    serviceType: 'eventgrid'
    groupId: 'topicspace'
    vnetId: vnetId
  }
}

//
//   F R O N T E N D
//

var frontendDns = res.dnsRecordRefFromFqdn(frontendIngressCertSAN)

module frontendIngressCertCSIAccess '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: 'aks-svc-kv-access-${frontendIngressCertName}'
  scope: resourceGroup(serviceKeyVaultSubscription, serviceKeyVaultResourceGroup)
  params: {
    keyVaultName: serviceKeyVaultName
    roleName: 'Key Vault Secrets User'
    managedIdentityPrincipalIds: [aksClusterKeyVaultSecretsProviderPrincipalId]
    secretName: frontendIngressCertName
  }
}

module frontendDNS '../modules/dns/a-record.bicep' = {
  name: 'frontend-dns'
  scope: resourceGroup(regionalResourceGroup)
  params: {
    zoneName: frontendDns.zoneName
    recordName: frontendDns.recordName
    ipAddress: istioIngressGatewayIP
    ttl: 300
  }
}

//
//   A D M I N   A P I
//

var adminApiDns = res.dnsRecordRefFromFqdn(adminApiIngressCertSAN)

module adminApiIngressCertCSIAccess '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: 'aks-svc-kv-access-${adminApiIngressCertName}'
  scope: resourceGroup(serviceKeyVaultSubscription, serviceKeyVaultResourceGroup)
  params: {
    keyVaultName: serviceKeyVaultName
    roleName: 'Key Vault Secrets User'
    managedIdentityPrincipalIds: [aksClusterKeyVaultSecretsProviderPrincipalId]
    secretName: adminApiIngressCertName
  }
}

module adminApiDNS '../modules/dns/a-record.bicep' = {
  name: 'admin-api-dns'
  scope: resourceGroup(regionalResourceGroup)
  params: {
    zoneName: adminApiDns.zoneName
    recordName: adminApiDns.recordName
    ipAddress: opsIngressGatewayIP
    ttl: 300
  }
}

//
//   S E S S I O N G A T E
//

var sessiongateDns = res.dnsRecordRefFromFqdn(sessiongateIngressCertSAN)

module sessiongateIngressCertCSIAccess '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: 'aksSPCRead-${sessiongateIngressCertName}'
  scope: resourceGroup(serviceKeyVaultSubscription, serviceKeyVaultResourceGroup)
  params: {
    keyVaultName: serviceKeyVaultName
    roleName: 'Key Vault Secrets User'
    managedIdentityPrincipalIds: [aksClusterKeyVaultSecretsProviderPrincipalId]
    secretName: sessiongateIngressCertName
  }
}

module sessiongateDNS '../modules/dns/a-record.bicep' = {
  name: 'sessiongate-dns'
  scope: resourceGroup(regionalResourceGroup)
  params: {
    zoneName: sessiongateDns.zoneName
    recordName: sessiongateDns.recordName
    ipAddress: opsIngressGatewayIP
    ttl: 300
  }
}

//
//   G E N E V A   C E R T I F I C A T E   A C C E S S
//

module genevaRpLogsCertCSIAccess '../modules/keyvault/key-vault-secret-access.bicep' = {
  name: 'geneva-crt-access-${uniqueString(resourceGroup().name)}'
  scope: resourceGroup(serviceKeyVaultSubscription, serviceKeyVaultResourceGroup)
  params: {
    keyVaultName: serviceKeyVaultName
    principalId: aksClusterKeyVaultSecretsProviderPrincipalId
    secretName: genevaRpLogsName
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

module svcClusterNSPProfile '../modules/network/nsp-profile.bicep' = {
  name: 'profile-${uniqueString(resourceGroup().name)}'
  params: {
    accessMode: svcNSPAccessMode
    nspName: svcNSPName
    profileName: svcNSPName
    location: location
    associatedResources: [
      aksKeyVault.id
      rpCosmosDbAccountId
    ]
    // TODO Add EV2 access here
    subscriptions: [
      subscription().id
    ]
  }
  dependsOn: [
    svcNSP
    rpCosmosdbPrivateEndpoint
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

//
//  A K S   D I A G N O S T I C   S E T T I N G S
//

module diagnosticSetting '../modules/aks/diagnostic-setting.bicep' = if (auditLogsEventHubAuthRuleId != '') {
  name: 'aks-diagnostic-setting'
  params: {
    aksClusterName: aksClusterName
    auditLogsEventHubName: auditLogsEventHubName
    auditLogsEventHubAuthRuleId: auditLogsEventHubAuthRuleId
  }
}
