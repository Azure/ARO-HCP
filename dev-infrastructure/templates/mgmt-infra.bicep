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
param aksClusterName string = 'aro-hcp-aks'

@description('Subnet address prefix')
param subnetPrefix string

@description('Specifies the address prefix of the subnet hosting the pods of the AKS cluster.')
param podSubnetPrefix string

@description('IPTags to be set on the cluster outbound IP address in the format of ipTagType:tag,ipTagType:tag')
param aksClusterOutboundIPAddressIPTags string = ''

@description('The name of the keyvault for AKS.')
@maxLength(24)
param aksKeyVaultName string

@description('The tag key for the AKS keyvault')
param aksKeyVaultTagName string

@description('The tag value for the AKS keyvault')
param aksKeyVaultTagValue string

@description('Manage soft delete setting for AKS etcd key-value store')
param aksEtcdKVEnableSoftDelete bool = true

@description('The name of the CX KeyVault')
param cxKeyVaultName string

@description('Defines if the CX KeyVault is private')
param cxKeyVaultPrivate bool

@description('Defines if the CX KeyVault has soft delete enabled')
param cxKeyVaultSoftDelete bool

// CX KV tagging
param cxKeyVaultTagName string
param cxKeyVaultTagValue string

@description('The name of the MSI KeyVault')
param msiKeyVaultName string

@description('Defines if the MSI KeyVault is private')
param msiKeyVaultPrivate bool

@description('Defines if the MSI KeyVault has soft delete enabled')
param msiKeyVaultSoftDelete bool

// MSI KV tagging
param msiKeyVaultTagName string
param msiKeyVaultTagValue string

@description('The name of the MGMT KeyVault')
param mgmtKeyVaultName string

@description('Defines if the MGMT KeyVault is private')
param mgmtKeyVaultPrivate bool

@description('Defines if the MGMT KeyVault has soft delete enabled')
param mgmtKeyVaultSoftDelete bool

// MGMT KV tagging
param mgmtKeyVaultTagName string
param mgmtKeyVaultTagValue string

@description('KV certificate officer principal ID')
param kvCertOfficerPrincipalId string

@description('MSI that will be used during pipeline runs')
param globalMSIId string

// Storage Account for HCP Backups
@minLength(3)
// @maxLength(24) Fails on EV2 pipelines, probably because the EV2 placeholder is longer than 24.
param hcpBackupsStorageAccountName string
param hcpBackupsStorageAccountContainerName string = 'backups'
param hcpBackupsStorageAccountZoneRedundantMode string = 'Auto'
param hcpBackupsStorageAccountPublic bool = true

// Reader role
// https://www.azadvertizer.net/azrolesadvertizer/acdd72a7-3385-48ef-bd42-f606fba81ae7.html
var readerRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'acdd72a7-3385-48ef-bd42-f606fba81ae7'
)

// service deployments running as the aroDevopsMsi need to lookup metadata about all kinds
// of resources, e.g. AKS metadata, database metadata, MI metadata, etc.
resource aroDevopsMSIReader 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, globalMSIId, readerRoleId)
  properties: {
    principalId: reference(globalMSIId, '2023-01-31').principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

//
//   K E Y V A U L T S
//

module cxKeyVault '../modules/keyvault/keyvault.bicep' = {
  name: 'cx-kv-${uniqueString(cxKeyVaultName)}'
  params: {
    location: location
    keyVaultName: cxKeyVaultName
    private: cxKeyVaultPrivate
    enableSoftDelete: cxKeyVaultSoftDelete
    tagKey: cxKeyVaultTagName
    tagValue: cxKeyVaultTagValue
  }
}

module cxKeyVaultAccess '../modules/keyvault/keyvault-secret-access.bicep' = [
  for role in [
    'Key Vault Secrets Officer'
    'Key Vault Certificates Officer'
  ]: {
    name: guid(cxKeyVaultName, kvCertOfficerPrincipalId, role)
    params: {
      keyVaultName: cxKeyVaultName
      roleName: role
      managedIdentityPrincipalIds: [kvCertOfficerPrincipalId]
    }
    dependsOn: [
      cxKeyVault
    ]
  }
]

output cxKeyVaultUrl string = cxKeyVault.outputs.kvUrl

module msiKeyVault '../modules/keyvault/keyvault.bicep' = {
  name: 'msi-kv-${uniqueString(msiKeyVaultName)}'
  params: {
    location: location
    keyVaultName: msiKeyVaultName
    private: msiKeyVaultPrivate
    enableSoftDelete: msiKeyVaultSoftDelete
    tagKey: msiKeyVaultTagName
    tagValue: msiKeyVaultTagValue
  }
}

output msiKeyVaultUrl string = msiKeyVault.outputs.kvUrl

module mgmtKeyVault '../modules/keyvault/keyvault.bicep' = {
  name: 'mgmt-kv-${uniqueString(mgmtKeyVaultName)}'
  params: {
    location: location
    keyVaultName: mgmtKeyVaultName
    private: mgmtKeyVaultPrivate
    enableSoftDelete: mgmtKeyVaultSoftDelete
    tagKey: mgmtKeyVaultTagName
    tagValue: mgmtKeyVaultTagValue
  }
}

module mgmtKeyVaultAccess '../modules/keyvault/keyvault-secret-access.bicep' = [
  for role in [
    'Key Vault Secrets Officer'
    'Key Vault Certificates Officer'
  ]: {
    name: guid(mgmtKeyVaultName, kvCertOfficerPrincipalId, role)
    params: {
      keyVaultName: mgmtKeyVaultName
      roleName: role
      managedIdentityPrincipalIds: [kvCertOfficerPrincipalId]
    }
    dependsOn: [
      mgmtKeyVault
    ]
  }
]

output mgmtKeyVaultUrl string = mgmtKeyVault.outputs.kvUrl

//
// H C P   B A C K U P S   S T O R A G E
//

module hcpBackupsStorage '../modules/hcp-backups/storage.bicep' = {
  name: 'hcp-backups-storage'
  params: {
    storageAccountName: hcpBackupsStorageAccountName
    location: location
    containerName: hcpBackupsStorageAccountContainerName
    zoneRedundantMode: hcpBackupsStorageAccountZoneRedundantMode
    public: hcpBackupsStorageAccountPublic
  }
}

output hcpBackupsStorageAccountId string = hcpBackupsStorage.outputs.storageAccountId

//
//   A K S   C L U S T E R   P R E R E Q U I S I T E S
//
// The ManagedCluster resource + its node pools are created by the aks-cluster-create Go
// tool (dev-infrastructure/scripts/aks-cluster-create), which runs as its own pipeline
// step between this one and mgmt-cluster.bicep -- a bicep resource can't depend on a
// resource a separate binary creates outside this deployment, so everything the Go tool
// needs to exist beforehand is created here instead.

// Network Contributor Role
// https://www.azadvertizer.net/azrolesadvertizer/4d97b98b-1d4f-4787-a291-c67834d212e7.html
var networkContributorRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions/',
  '4d97b98b-1d4f-4787-a291-c67834d212e7'
)

@description('Perform cryptographic operations using keys. Only works for key vaults that use the Azure role-based access control permission model.')
var keyVaultCryptoUserId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  '12338af0-0e69-4776-bea7-57ae8d297424'
)

resource aksClusterUserDefinedManagedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${aksClusterName}-msi'
  location: location
}

//
//   N E T W O R K
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

// The VNet itself is created and Swift-tagged by the dedicated swift-vnet pipeline step
// (scripts/swift-vnet.sh, running as the Swift-registered globalMSI), which this step
// depends on -- see this template's dependsOn in mgmt-pipeline.yaml. It is only ever
// looked up here, never created by bicep.
resource vnet 'Microsoft.Network/virtualNetworks@2024-05-01' existing = {
  name: vnetName
}

module nodeSubnetCreation '../modules/network/aks-node-subnet.bicep' = {
  name: 'subnet-${nodeSubnetName}-creation'
  params: {
    vnetName: vnetName
    subnetName: nodeSubnetName
    subnetNSGId: mgmtClusterNSG.id
    subnetPrefix: subnetPrefix
  }
}

resource aksPodSubnet 'Microsoft.Network/virtualNetworks/subnets@2023-11-01' = {
  parent: vnet
  name: 'PodSubnet-001'
  dependsOn: [
    nodeSubnetCreation
  ]
  properties: {
    addressPrefix: podSubnetPrefix
    privateEndpointNetworkPolicies: 'Disabled'
    serviceEndpoints: [
      {
        service: 'Microsoft.Storage'
      }
    ]
    defaultOutboundAccess: false
    delegations: [
      {
        name: 'AKS'
        properties: {
          serviceName: 'Microsoft.ContainerService/managedClusters'
        }
      }
    ]
  }
}

resource aksNetworkContributorRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: vnet
  name: guid(
    aksClusterUserDefinedManagedIdentity.id,
    networkContributorRoleId,
    resourceId('Microsoft.Network/virtualNetworks/subnets', vnetName, nodeSubnetName)
  )
  properties: {
    roleDefinitionId: networkContributorRoleId
    principalId: aksClusterUserDefinedManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
  }
}

//
//   E G R E S S
//

var aksClusterOutboundIPAddressName = '${aksClusterName}-outbound-ip'
module aksClusterOutboundIPAddress '../modules/network/publicipaddress.bicep' = {
  name: aksClusterOutboundIPAddressName
  params: {
    name: aksClusterOutboundIPAddressName
    ipTags: aksClusterOutboundIPAddressIPTags
    location: location
    zones: length(locationAvailabilityZoneList) > 0 ? locationAvailabilityZoneList : null
    // Role Assignment needed for the public IP address to be used on the Load Balancer
    roleAssignmentProperties: {
      principalId: aksClusterUserDefinedManagedIdentity.properties.principalId
      principalType: 'ServicePrincipal'
      roleDefinitionId: networkContributorRoleId
    }
  }
}

//
//   E T C D   K E Y V A U L T
//

module aks_keyvault_builder '../modules/keyvault/keyvault.bicep' = {
  name: aksKeyVaultName
  params: {
    location: location
    keyVaultName: aksKeyVaultName
    // todo: change for higher environments
    private: false
    enableSoftDelete: aksEtcdKVEnableSoftDelete
    tagKey: aksKeyVaultTagName
    tagValue: aksKeyVaultTagValue
  }
}

resource aks_keyvault 'Microsoft.KeyVault/vaults@2023-07-01' existing = {
  name: aks_keyvault_builder.name
}

resource aks_etcd_kms 'Microsoft.KeyVault/vaults/keys@2023-07-01' = {
  parent: aks_keyvault
  name: 'aks-etcd-encryption'
  properties: {
    kty: 'RSA'
    keyOps: [
      'encrypt'
      'decrypt'
    ]
    keySize: 2048
    rotationPolicy: {
      lifetimeActions: [
        {
          action: {
            type: 'notify'
          }
          trigger: {
            timeBeforeExpiry: 'P30D'
          }
        }
      ]
    }
  }
}

resource aks_keyvault_crypto_user 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(aksClusterUserDefinedManagedIdentity.id, keyVaultCryptoUserId, aks_keyvault.id)
  scope: aks_keyvault
  properties: {
    roleDefinitionId: keyVaultCryptoUserId
    principalId: aksClusterUserDefinedManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
  }
}

// Outputs consumed by the aks-cluster-create pipeline step
output nodeSubnetId string = nodeSubnetCreation.outputs.subnetId
output podSubnetId string = aksPodSubnet.id
output outboundIPResourceId string = aksClusterOutboundIPAddress.outputs.resourceId
output etcdKeyUriWithVersion string = aks_etcd_kms.properties.keyUriWithVersion
output managedIdentityId string = aksClusterUserDefinedManagedIdentity.id

// Outputs consumed by mgmt-cluster.bicep
output vnetId string = vnet.id
