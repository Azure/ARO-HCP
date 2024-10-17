@description('Azure Region Location')
param location string = resourceGroup().location

@description('Set to true to prevent resources from being pruned after 48 hours')
param persist bool = false

@description('Captures logged in users UID')
param currentUserId string

@description('AKS cluster name')
param aksClusterName string

@description('Disk size for the AKS system nodes')
param aksSystemOsDiskSizeGB int = 32

@description('Disk size for the AKS user nodes')
param aksUserOsDiskSizeGB int = 32

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

@description('The resourcegroup for regional infrastructure')
param regionalResourceGroup string

@description('The domain to use to use for the maestro certificate. Relevant only for environments where OneCert can be used.')
param maestroCertDomain string

@description('The name of the eventgrid namespace for Maestro.')
param maestroEventGridNamespacesName string

@description('The name of the keyvault for Maestro Eventgrid namespace certificates.')
@maxLength(24)
param maestroKeyVaultName string

@description('The name of the managed identity that will manage certificates in maestros keyvault.')
param maestroKeyVaultCertOfficerMSIName string = '${maestroKeyVaultName}-cert-officer-msi'

@description('Deploy ARO HCP CS Infrastructure if true')
param deployCsInfra bool

@description('The name of the Postgres server for CS')
@maxLength(60)
param csPostgresServerName string

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

@description('The size of the Postgres server for Maestro')
param maestroPostgresServerStorageSizeGB int

@description('The name of the service keyvault')
param serviceKeyVaultName string

@description('The name of the resourcegroup for the service keyvault')
param serviceKeyVaultResourceGroup string = resourceGroup().name

@description('Soft delete setting for service keyvault')
param serviceKeyVaultSoftDelete bool = true

@description('If true, make the service keyvault private and only accessible by the svc cluster via private link.')
param serviceKeyVaultPrivate bool = true

@description('Image sync ACR RG name')
param imageSyncAcrResourceGroupNames array = []

@description('OIDC Storage Account name')
param oidcStorageAccountName string

@description('OIDC Storage Account SKU')
param oidcStorageAccountSku string = 'Standard_ZRS'

@description('Clusters Service ACR RG names')
param clustersServiceAcrResourceGroupNames array = []

@description('MSI that will be used to run the deploymentScript')
param aroDevopsMsiId string

@description('This is a global DNS zone name that will be the parent of regional DNS zones to host ARO HCP customer cluster DNS records')
param baseDNSZoneName string = ''

@description('This is the region name in dev/staging/production')
param regionalDNSSubdomain string = empty(currentUserId)
  ? location
  : '${location}-${take(uniqueString(currentUserId), 5)}'

var clusterServiceMIName = 'clusters-service'

// Tags the resource group
resource subscriptionTags 'Microsoft.Resources/tags@2024-03-01' = {
  name: 'default'
  scope: resourceGroup()
  properties: {
    tags: {
      persist: toLower(string(persist))
      deployedBy: currentUserId
    }
  }
}

module svcCluster '../modules/aks-cluster-base.bicep' = {
  name: 'svc-cluster'
  scope: resourceGroup()
  params: {
    location: location
    persist: persist
    aksClusterName: aksClusterName
    aksNodeResourceGroupName: aksNodeResourceGroupName
    aksEtcdKVEnableSoftDelete: aksEtcdKVEnableSoftDelete
    kubernetesVersion: kubernetesVersion
    deployIstio: true
    istioVersion: istioVersion
    vnetAddressPrefix: vnetAddressPrefix
    subnetPrefix: subnetPrefix
    podSubnetPrefix: podSubnetPrefix
    clusterType: 'svc-cluster'
    systemOsDiskSizeGB: aksSystemOsDiskSizeGB
    userOsDiskSizeGB: aksUserOsDiskSizeGB
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
        uamiName: 'maestro-server'
        namespace: 'maestro'
        serviceAccountName: 'maestro'
      }
      cs_wi: {
        uamiName: clusterServiceMIName
        namespace: 'cluster-service'
        serviceAccountName: 'clusters-service'
      }
      image_sync_wi: {
        uamiName: 'image-sync'
        namespace: 'image-sync'
        serviceAccountName: 'image-sync'
      }
    })
    aksKeyVaultName: aksKeyVaultName
    acrPullResourceGroups: acrPullResourceGroups
  }
}

output aksClusterName string = svcCluster.outputs.aksClusterName
var frontendMI = filter(svcCluster.outputs.userAssignedIdentities, id => id.uamiName == 'frontend')[0]
var backendMI = filter(svcCluster.outputs.userAssignedIdentities, id => id.uamiName == 'backend')[0]

module rpCosmosDb '../modules/rp-cosmos.bicep' = if (deployFrontendCosmos) {
  name: 'rp_cosmos_db'
  scope: resourceGroup()
  params: {
    location: location
    aksNodeSubnetId: svcCluster.outputs.aksNodeSubnetId
    vnetId: svcCluster.outputs.aksVnetId
    disableLocalAuth: disableLocalAuth
    userAssignedMIs: [frontendMI, backendMI]
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
    maestroKeyVaultName: maestroKeyVaultName
    maestroKeyVaultOfficerManagedIdentityName: maestroKeyVaultCertOfficerMSIName
    maestroKeyVaultCertificateDomain: maestroCertDomain
    deployPostgres: deployMaestroPostgres
    postgresServerName: maestroPostgresServerName
    postgresServerVersion: maestroPostgresServerVersion
    postgresServerStorageSizeGB: maestroPostgresServerStorageSizeGB
    privateEndpointSubnetId: svcCluster.outputs.aksNodeSubnetId
    privateEndpointVnetId: svcCluster.outputs.aksVnetId
    postgresServerPrivate: maestroPostgresPrivate
    maestroServerManagedIdentityPrincipalId: filter(
      svcCluster.outputs.userAssignedIdentities,
      id => id.uamiName == 'maestro-server'
    )[0].uamiPrincipalID
    maestroServerManagedIdentityName: filter(
      svcCluster.outputs.userAssignedIdentities,
      id => id.uamiName == 'maestro-server'
    )[0].uamiName
    location: location
  }
}

//
//   K E Y V A U L T S
//

module serviceKeyVault '../modules/keyvault/keyvault.bicep' = {
  name: 'service-keyvault'
  scope: resourceGroup(serviceKeyVaultResourceGroup)
  params: {
    location: location
    keyVaultName: serviceKeyVaultName
    private: serviceKeyVaultPrivate
    enableSoftDelete: serviceKeyVaultSoftDelete
    purpose: 'service'
  }
}

output svcKeyVaultName string = serviceKeyVault.outputs.kvName

module serviceKeyVaultPrivateEndpoint '../modules/keyvault/keyvault-private-endpoint.bicep' = {
  name: 'service-keyvault-pe'
  params: {
    location: location
    keyVaultName: serviceKeyVaultName
    subnetId: svcCluster.outputs.aksNodeSubnetId
    vnetId: svcCluster.outputs.aksVnetId
    keyVaultId: serviceKeyVault.outputs.kvId
  }
}

//
//   C L U S T E R   S E R V I C E
//

var csManagedIdentityPrincipalId = filter(
  svcCluster.outputs.userAssignedIdentities,
  id => id.uamiName == clusterServiceMIName
)[0].uamiPrincipalID

module cs '../modules/cluster-service.bicep' = if (deployCsInfra) {
  name: 'cluster-service'
  params: {
    location: location
    postgresServerName: csPostgresServerName
    privateEndpointSubnetId: svcCluster.outputs.aksNodeSubnetId
    privateEndpointVnetId: svcCluster.outputs.aksVnetId
    postgresServerPrivate: clusterServicePostgresPrivate
    clusterServiceManagedIdentityPrincipalId: csManagedIdentityPrincipalId
    clusterServiceManagedIdentityName: clusterServiceMIName
  }
  dependsOn: [
    maestroServer
    svcCluster
  ]
}

module csServiceKeyVaultAccess '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: guid(serviceKeyVaultName, 'cs', 'read')
  scope: resourceGroup(serviceKeyVaultResourceGroup)
  params: {
    keyVaultName: serviceKeyVaultName
    roleName: 'Key Vault Secrets User'
    managedIdentityPrincipalId: csManagedIdentityPrincipalId
  }
  dependsOn: [
    serviceKeyVault
    svcCluster
  ]
}

module csDnsZoneContributor '../modules/dns/zone-contributor.bicep' = {
  name: guid(regionalDNSSubdomain, svcCluster.name, 'cs')
  scope: resourceGroup(regionalResourceGroup)
  params: {
    zoneName: '${regionalDNSSubdomain}.${baseDNSZoneName}'
    zoneContributerManagedIdentityPrincipalId: csManagedIdentityPrincipalId
  }
}

//
//   I M A G E   S Y N C
//

var imageSyncManagedIdentityPrincipalId = filter(
  svcCluster.outputs.userAssignedIdentities,
  id => id.uamiName == 'image-sync'
)[0].uamiPrincipalID

module imageServiceKeyVaultAccess '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: guid(serviceKeyVaultName, 'imagesync', 'read')
  scope: resourceGroup(serviceKeyVaultResourceGroup)
  params: {
    keyVaultName: serviceKeyVaultName
    roleName: 'Key Vault Secrets User'
    managedIdentityPrincipalId: imageSyncManagedIdentityPrincipalId
  }
  dependsOn: [
    serviceKeyVault
    svcCluster
  ]
}

resource imageSyncAcrResourceGroups 'Microsoft.Resources/resourceGroups@2023-07-01' existing = [
  for rg in imageSyncAcrResourceGroupNames: {
    name: rg
    scope: subscription()
  }
]

module acrPushRole '../modules/acr-permissions.bicep' = [
  for (_, i) in imageSyncAcrResourceGroupNames: {
    name: guid(imageSyncAcrResourceGroups[i].id, resourceGroup().name, 'image-sync', 'push')
    scope: imageSyncAcrResourceGroups[i]
    params: {
      principalId: imageSyncManagedIdentityPrincipalId
      grantPushAccess: true
      acrResourceGroupid: imageSyncAcrResourceGroups[i].id
    }
  }
]

resource clustersServiceAcrResourceGroups 'Microsoft.Resources/resourceGroups@2023-07-01' existing = [
  for rg in clustersServiceAcrResourceGroupNames: {
    name: rg
    scope: subscription()
  }
]

module acrContributorRole '../modules/acr-permissions.bicep' = [
  for (_, i) in clustersServiceAcrResourceGroupNames: {
    name: guid(clustersServiceAcrResourceGroups[i].id, resourceGroup().name, 'clusters-service', 'contributor')
    scope: clustersServiceAcrResourceGroups[i]
    params: {
      principalId: csManagedIdentityPrincipalId
      grantContributorAccess: true
      acrResourceGroupid: clustersServiceAcrResourceGroups[i].id
    }
  }
]

// oidc

module oidc '../modules/oidc/main.bicep' = {
  name: 'oidc'
  params: {
    location: location
    storageAccountName: oidcStorageAccountName
    rpMsiName: clusterServiceMIName
    skuName: oidcStorageAccountSku
    aroDevopsMsiId: aroDevopsMsiId
    deploymentScriptLocation: location
  }
  dependsOn: [
    svcCluster
  ]
}
