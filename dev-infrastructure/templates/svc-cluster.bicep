@description('Azure Region Location')
param location string = resourceGroup().location

@description('Set to true to prevent resources from being pruned after 48 hours')
param persist bool = false

@description('Captures logged in users UID')
param currentUserId string

@description('AKS cluster name')
param aksClusterName string

@description('Names of additional resource group contains ACRs the AKS cluster will get pull permissions on')
param additionalAcrResourceGroups array = [resourceGroup().name]

@description('Name of the resource group for the AKS nodes')
param aksNodeResourceGroupName string = '${resourceGroup().name}-aks1'

@description('VNET address prefix')
param vnetAddressPrefix string

@description('Subnet address prefix')
param subnetPrefix string

@description('Specifies the address prefix of the subnet hosting the pods of the AKS cluster.')
param podSubnetPrefix string

@description('(Optional) boolean flag to configure public/private AKS Cluster')
param enablePrivateCluster bool

@description('Kuberentes version to use with AKS')
param kubernetesVersion string

@description('Istio control plane version to use with AKS')
param istioVersion array

@description('The name of the keyvault for AKS.')
@maxLength(24)
param aksKeyVaultName string

@description('Manage soft delete setting for AKS etcd key-value store')
param aksEtcdKVEnableSoftDelete bool = true

// TODO: When the work around workload identity for the RP is finalized, change this to true
@description('disableLocalAuth for the ARO HCP RP CosmosDB')
param disableLocalAuth bool

@description('Deploy ARO HCP RP Azure Cosmos DB if true')
param deployFrontendCosmos bool

@description('List of workload identities to create and their required values')
param workloadIdentities array

@description('Deploy ARO HCP Maestro Infrastructure if true')
param deployMaestroInfra bool

@description('The domain to use to use for the maestro certificate. Relevant only for environments where OneCert can be used.')
param maestroCertDomain string

@description('The name of the eventgrid namespace for Maestro.')
param maestroEventGridNamespacesName string

@description('The name of the keyvault for Maestro Eventgrid namespace certificates.')
@maxLength(24)
param maestroKeyVaultName string

@description('The name of the managed identity that will manage certificates in maestros keyvault.')
param maestroKeyVaultCertOfficerMSIName string = '${maestroKeyVaultName}-cert-officer-msi'

@description('The resourcegroups where the Maestro infrastructure will be deployed.')
param maestroInfraResourceGroup string = resourceGroup().name

@description('Deploy ARO HCP CS Infrastructure if true')
param deployCsInfra bool

@description('The name of the Postgres server for CS')
@maxLength(60)
param csPostgresServerName string

@description('Deploy ARO HCP Maestro Postgres if true')
param deployMaestroPostgres bool = true

@description('The name of the Postgres server for Maestro')
@maxLength(60)
param maestroPostgresServerName string

@description('The version of the Postgres server for Maestro')
param maestroPostgresServerVersion string

@description('The size of the Postgres server for Maestro')
param maestroPostgresServerStorageSizeGB int

@description('The maximum client sessions per authentication name for the EventGrid MQTT broker')
param maxClientSessionsPerAuthName int

@description('The name of the service keyvault')
param serviceKeyVaultName string

@description('Soft delete setting for service keyvault')
param serviceKeyVaultSoftDelete bool = true

@description('If true, make the service keyvault private and only accessible by the svc cluster via private link.')
param serviceKeyVaultPrivate bool = true

@description('This is the region name in dev/staging/production, can be overriden for testing')
param regionalDNSSubdomain string = resourceGroup().location

@description('This is a global DNS zone name that will be the parent of regional DNS zones to host ARO HCP customer cluster DNS records')
param baseDNSZoneName string

@description('The resource group to deploy the base DNS zone to')
param baseDNSZoneResourceGroup string = 'global'

module svcCluster '../modules/aks-cluster-base.bicep' = {
  name: 'svc-cluster'
  scope: resourceGroup()
  params: {
    location: location
    persist: persist
    aksClusterName: aksClusterName
    aksNodeResourceGroupName: aksNodeResourceGroupName
    aksEtcdKVEnableSoftDelete: aksEtcdKVEnableSoftDelete
    currentUserId: currentUserId
    enablePrivateCluster: enablePrivateCluster
    kubernetesVersion: kubernetesVersion
    deployIstio: true
    istioVersion: istioVersion
    vnetAddressPrefix: vnetAddressPrefix
    subnetPrefix: subnetPrefix
    podSubnetPrefix: podSubnetPrefix
    clusterType: 'svc-cluster'
    workloadIdentities: workloadIdentities
    aksKeyVaultName: aksKeyVaultName
    deployUserAgentPool: true
    additionalAcrResourceGroups: additionalAcrResourceGroups
  }
}

output aksClusterName string = svcCluster.outputs.aksClusterName
var frontendMI = filter(svcCluster.outputs.userAssignedIdentities, id => id.uamiName == 'frontend')[0]

module rpCosmosDb '../modules/rp-cosmos.bicep' = if (deployFrontendCosmos) {
  name: 'rp_cosmos_db'
  scope: resourceGroup()
  params: {
    location: location
    aksNodeSubnetId: svcCluster.outputs.aksNodeSubnetId
    vnetId: svcCluster.outputs.aksVnetId
    disableLocalAuth: disableLocalAuth
    userAssignedMI: frontendMI.uamiID
    uamiPrincipalId: frontendMI.uamiPrincipalID
  }
}

output cosmosDBName string = deployFrontendCosmos ? rpCosmosDb.outputs.cosmosDBName : ''
output frontend_mi_client_id string = frontendMI.uamiClientID

//
//   M A E S T R O
//

module maestroInfra '../modules/maestro/maestro-infra.bicep' = if (deployMaestroInfra) {
  name: 'maestro-infra'
  scope: resourceGroup(maestroInfraResourceGroup)
  params: {
    eventGridNamespaceName: maestroEventGridNamespacesName
    location: location
    maxClientSessionsPerAuthName: maxClientSessionsPerAuthName
    maestroKeyVaultName: maestroKeyVaultName
    kvCertOfficerManagedIdentityName: maestroKeyVaultCertOfficerMSIName
    postgresServerName: maestroPostgresServerName
    postgresServerVersion: maestroPostgresServerVersion
    postgresServerStorageSizeGB: maestroPostgresServerStorageSizeGB
    maestroServerManagedIdentityPrincipalId: filter(
      svcCluster.outputs.userAssignedIdentities,
      id => id.uamiName == 'maestro-server'
    )[0].uamiPrincipalID
    maestroServerManagedIdentityName: filter(
      svcCluster.outputs.userAssignedIdentities,
      id => id.uamiName == 'maestro-server'
    )[0].uamiName
    deployPostgres: deployMaestroPostgres
  }
}

module maestroServer '../modules/maestro/maestro-server.bicep' = if (deployMaestroInfra) {
  name: 'maestro-server'
  params: {
    maestroServerManagedIdentityPrincipalId: filter(
      svcCluster.outputs.userAssignedIdentities,
      id => id.uamiName == 'maestro-server'
    )[0].uamiPrincipalID
    maestroInfraResourceGroup: maestroInfraResourceGroup
    maestroEventGridNamespaceName: maestroEventGridNamespacesName
    maestroKeyVaultName: maestroKeyVaultName
    maestroKeyVaultOfficerManagedIdentityName: maestroKeyVaultCertOfficerMSIName
    maestroKeyVaultCertificateDomain: maestroCertDomain
    location: location
  }
  dependsOn: [
    maestroInfra
  ]
}

//
//   D N S
//
resource regionalZone 'Microsoft.Network/dnsZones@2018-05-01' = {
  name: '${regionalDNSSubdomain}.${baseDNSZoneName}'
  location: 'global'
}

module regionalZoneDelegation '../modules/dns/zone-delegation.bicep' = {
  name: 'regional-zone-delegation'
  scope: resourceGroup(baseDNSZoneResourceGroup)
  params: {
    childZoneName: regionalDNSSubdomain
    childZoneNameservers: regionalZone.properties.nameServers
    parentZoneName: baseDNSZoneName
  }
}

//
//   K E Y V A U L T S
//

module serviceKeyVault '../modules/keyvault/keyvault.bicep' = {
  name: 'service-keyvault'
  params: {
    location: location
    keyVaultName: serviceKeyVaultName
    private: serviceKeyVaultPrivate
    enableSoftDelete: serviceKeyVaultSoftDelete
    subnetId: svcCluster.outputs.aksNodeSubnetId
    vnetId: svcCluster.outputs.aksVnetId
  }
}

//
//   C L U S T E R   S E R V I C E
//

var csManagedIdentityPrincipalId = filter(
  svcCluster.outputs.userAssignedIdentities,
  id => id.uamiName == 'clusters-service'
)[0].uamiPrincipalID

module cs '../modules/cluster-service.bicep' = if (deployCsInfra) {
  name: 'cluster-service'
  params: {
    location: location
    postgresServerName: csPostgresServerName
    clusterServiceManagedIdentityPrincipalId: csManagedIdentityPrincipalId
    clusterServiceManagedIdentityName: filter(
      svcCluster.outputs.userAssignedIdentities,
      id => id.uamiName == 'clusters-service'
    )[0].uamiName
  }
}

module csServiceKeyVaultAccess '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: guid(serviceKeyVaultName, 'cs', 'read')
  params: {
    keyVaultName: serviceKeyVaultName
    roleName: 'Key Vault Secrets User'
    managedIdentityPrincipalId: csManagedIdentityPrincipalId
  }
  dependsOn: [
    serviceKeyVault
  ]
}
