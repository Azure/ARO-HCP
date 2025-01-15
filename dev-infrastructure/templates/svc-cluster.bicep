@description('Azure Region Location')
param location string = resourceGroup().location

@description('Set to true to prevent resources from being pruned after 48 hours')
param persist bool = false

@description('AKS cluster name')
param aksClusterName string

@description('Disk size for the AKS system nodes')
param aksSystemOsDiskSizeGB int = 32

@description('Disk size for the AKS user nodes')
param aksUserOsDiskSizeGB int = 32

@description('Min replicas for the worker nodes')
param userAgentMinCount int = 1

@description('Max replicas for the worker nodes')
param userAgentMaxCount int = 3

@description('VM instance type for the worker nodes')
param userAgentVMSize string = 'Standard_D2s_v3'

@description('Availability Zone count for worker nodes')
param userAgentPoolAZCount int = 3

@description('Names of additional resource group contains ACRs the AKS cluster will get pull permissions on')
param acrPullResourceGroups array = []

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

@description('Manage soft delete setting for AKS etcd key-value store')
param aksEtcdKVEnableSoftDelete bool = true

// TODO: When the work around workload identity for the RP is finalized, change this to true
@description('disableLocalAuth for the ARO HCP RP CosmosDB')
param disableLocalAuth bool

@description('Deploy ARO HCP RP Azure Cosmos DB if true')
param deployFrontendCosmos bool

@description('The name of the Cosmos DB for the RP')
param rpCosmosDbName string

@description('If true, make the Cosmos DB instance private')
param rpCosmosDbPrivate bool

@description('The resourcegroup for regional infrastructure')
param regionalResourceGroup string

@description('The domain to use to use for the maestro certificate. Relevant only for environments where OneCert can be used.')
param maestroCertDomain string

@description('The name of the eventgrid namespace for Maestro.')
param maestroEventGridNamespacesName string

@description('Deploy CS Postgres if true')
param csPostgresDeploy bool

@description('The name of the Postgres server for CS')
@maxLength(60)
param csPostgresServerName string

@description('The minimum TLS version for the Postgres server for CS')
param csPostgresServerMinTLSVersion string

@description('If true, make the CS Postgres instance private')
param clusterServicePostgresPrivate bool = true

@description('Deploy ARO HCP Maestro Postgres if true')
param deployMaestroPostgres bool = true

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

@description('OIDC Storage Account SKU')
param oidcStorageAccountSku string = 'Standard_ZRS'

@description('Clusters Service ACR RG names')
param clustersServiceAcrResourceGroupNames array = []

@description('''
  Defines if the custom ACR token management role should be used to grant
  CS token management permissions on the OCP ACR
  ''')
param useCustomACRTokenManagementRole bool

@description('MSI that will be used to run the deploymentScript')
param aroDevopsMsiId string

@description('The regional DNS zone to hold ARO HCP customer cluster DNS records')
param regionalCXDNSZoneName string

@description('Frontend Ingress Certificate Name')
param frontendIngressCertName string

@description('The name of the Azure Monitor Workspace (stores prometheus metrics)')
param azureMonitorWorkspaceName string

@description('The name of the CS managed identity')
param csMIName string

@description('The namespace of the CS managed identity')
param csNamespace string

@description('The service account name of the CS managed identity')
param csServiceAccountName string

resource serviceKeyVault 'Microsoft.KeyVault/vaults@2024-04-01-preview' existing = {
  name: serviceKeyVaultName
  scope: resourceGroup(serviceKeyVaultResourceGroup)
}

module svcCluster '../modules/aks-cluster-base.bicep' = {
  name: 'cluster'
  scope: resourceGroup()
  params: {
    location: location
    persist: persist
    aksClusterName: aksClusterName
    aksNodeResourceGroupName: aksNodeResourceGroupName
    aksEtcdKVEnableSoftDelete: aksEtcdKVEnableSoftDelete
    kubernetesVersion: kubernetesVersion
    deployIstio: true
    istioVersions: split(istioVersions, ',')
    vnetAddressPrefix: vnetAddressPrefix
    subnetPrefix: subnetPrefix
    podSubnetPrefix: podSubnetPrefix
    clusterType: 'svc-cluster'
    systemOsDiskSizeGB: aksSystemOsDiskSizeGB
    userOsDiskSizeGB: aksUserOsDiskSizeGB
    userAgentMinCount: userAgentMinCount
    userAgentPoolAZCount: userAgentPoolAZCount
    userAgentMaxCount: userAgentMaxCount
    userAgentVMSize: userAgentVMSize
    workloadIdentities: items({
      frontend_wi: {
        uamiName: 'frontend'
        namespace: 'aro-hcp'
        serviceAccountName: 'frontend'
      }
      backend_wi: {
        uamiName: 'backend'
        namespace: 'aro-hcp'
        serviceAccountName: 'backend'
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
      image_sync_wi: {
        uamiName: 'image-sync'
        namespace: 'image-sync'
        serviceAccountName: 'image-sync'
      }
    })
    aksKeyVaultName: aksKeyVaultName
    acrPullResourceGroups: acrPullResourceGroups
    aroDevopsMsiId: aroDevopsMsiId
    dcrId: dataCollection.outputs.dcrId
  }
}

output aksClusterName string = svcCluster.outputs.aksClusterName

//
// M E T R I C S
//

module dataCollection '../modules/metrics/datacollection.bicep' = {
  name: '${resourceGroup().name}-${aksClusterName}'
  params: {
    azureMonitorWorkspaceLocation: location
    azureMonitorWorkspaceName: azureMonitorWorkspaceName
    regionalResourceGroup: regionalResourceGroup
    aksClusterName: aksClusterName
  }
}

var frontendMI = filter(svcCluster.outputs.userAssignedIdentities, id => id.uamiName == 'frontend')[0]
var backendMI = filter(svcCluster.outputs.userAssignedIdentities, id => id.uamiName == 'backend')[0]

module rpCosmosDb '../modules/rp-cosmos.bicep' = if (deployFrontendCosmos) {
  name: 'rp_cosmos_db'
  scope: resourceGroup()
  params: {
    name: rpCosmosDbName
    location: location
    aksNodeSubnetId: svcCluster.outputs.aksNodeSubnetId
    vnetId: svcCluster.outputs.aksVnetId
    disableLocalAuth: disableLocalAuth
    userAssignedMIs: [frontendMI, backendMI]
    private: rpCosmosDbPrivate
  }
}

output cosmosDBName string = deployFrontendCosmos ? rpCosmosDb.outputs.cosmosDBName : ''
output frontend_mi_client_id string = frontendMI.uamiClientID

//
//   M A E S T R O
//

module maestroServer '../modules/maestro/maestro-server.bicep' = {
  name: 'maestro-server'
  params: {
    maestroInfraResourceGroup: regionalResourceGroup
    maestroEventGridNamespaceName: maestroEventGridNamespacesName
    mqttClientName: maestroServerMqttClientName
    certKeyVaultName: serviceKeyVaultName
    certKeyVaultResourceGroup: serviceKeyVaultResourceGroup
    keyVaultOfficerManagedIdentityName: aroDevopsMsiId
    maestroCertificateDomain: maestroCertDomain
    deployPostgres: deployMaestroPostgres
    postgresServerName: maestroPostgresServerName
    postgresServerVersion: maestroPostgresServerVersion
    postgresServerMinTLSVersion: maestroPostgresServerMinTLSVersion
    postgresServerStorageSizeGB: maestroPostgresServerStorageSizeGB
    privateEndpointSubnetId: svcCluster.outputs.aksNodeSubnetId
    privateEndpointVnetId: svcCluster.outputs.aksVnetId
    maestroDatabaseName: maestroPostgresDatabaseName
    postgresServerPrivate: maestroPostgresPrivate
    postgresAdministrationManagedIdentityId: aroDevopsMsiId
    maestroServerManagedIdentityPrincipalId: filter(
      svcCluster.outputs.userAssignedIdentities,
      id => id.uamiName == maestroMIName
    )[0].uamiPrincipalID
    maestroServerManagedIdentityName: maestroMIName
  }
  dependsOn: [
    serviceKeyVault
  ]
}

//
//   K E Y V A U L T S
//

module serviceKeyVaultPrivateEndpoint '../modules/private-endpoint.bicep' = {
  name: '${deployment().name}-svcs-kv-pe'
  params: {
    location: location
    subnetIds: [svcCluster.outputs.aksNodeSubnetId]
    vnetId: svcCluster.outputs.aksVnetId
    privateLinkServiceId: serviceKeyVault.id
    serviceType: 'keyvault'
    groupId: 'vault'
  }
}

//
//   C L U S T E R   S E R V I C E
//

var csManagedIdentityPrincipalId = filter(svcCluster.outputs.userAssignedIdentities, id => id.uamiName == csMIName)[0].uamiPrincipalID

module cs '../modules/cluster-service.bicep' = {
  name: 'cluster-service'
  params: {
    postgresServerName: csPostgresServerName
    postgresServerMinTLSVersion: csPostgresServerMinTLSVersion
    privateEndpointSubnetId: svcCluster.outputs.aksNodeSubnetId
    privateEndpointVnetId: svcCluster.outputs.aksVnetId
    deployPostgres: csPostgresDeploy
    postgresServerPrivate: clusterServicePostgresPrivate
    clusterServiceManagedIdentityPrincipalId: csManagedIdentityPrincipalId
    clusterServiceManagedIdentityName: csMIName
    serviceKeyVaultName: serviceKeyVault.name
    serviceKeyVaultResourceGroup: serviceKeyVaultResourceGroup
    regionalCXDNSZoneName: regionalCXDNSZoneName
    regionalResourceGroup: regionalResourceGroup
    acrResourceGroupNames: clustersServiceAcrResourceGroupNames
    postgresAdministrationManagedIdentityId: aroDevopsMsiId
    useCustomACRTokenManagementRole: useCustomACRTokenManagementRole
  }
  dependsOn: [
    maestroServer
  ]
}

// oidc

module oidc '../modules/oidc/main.bicep' = {
  name: '${deployment().name}-oidc'
  params: {
    location: location
    storageAccountName: oidcStorageAccountName
    rpMsiName: csMIName
    skuName: oidcStorageAccountSku
    msiId: aroDevopsMsiId
    deploymentScriptLocation: location
  }
  dependsOn: [
    svcCluster
  ]
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
    subnetIds: [svcCluster.outputs.aksNodeSubnetId]
    privateLinkServiceId: eventGridNamespace.id
    serviceType: 'eventgrid'
    groupId: 'topicspace'
    vnetId: svcCluster.outputs.aksVnetId
  }
}

//
//  C E R T I F I C A T E   A C C E S S   P E R M I S S I O N
//

module certificateOfficerAccess '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: 'aksClusterKeyVaultSecretsProviderMI-${frontendIngressCertName}'
  scope: resourceGroup(serviceKeyVaultResourceGroup)
  params: {
    keyVaultName: serviceKeyVaultName
    roleName: 'Key Vault Secrets User'
    managedIdentityPrincipalId: svcCluster.outputs.aksClusterKeyVaultSecretsProviderPrincipalId
    secretName: frontendIngressCertName
  }
}
