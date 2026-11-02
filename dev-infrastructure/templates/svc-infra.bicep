import { csvToArray, getLocationAvailabilityZonesCSV } from '../modules/common.bicep'

@description('Azure Region Location')
param location string = resourceGroup().location

@description('Availability Zones to use for the infrastructure, as a CSV string.')
param locationAvailabilityZones string = getLocationAvailabilityZonesCSV(location)
var locationAvailabilityZoneList = csvToArray(locationAvailabilityZones)

param aksClusterName string
param vnetAddressPrefix string
param subnetPrefix string
param podSubnetPrefix string
@maxLength(24)
param aksKeyVaultName string
param aksKeyVaultTagName string
param aksKeyVaultTagValue string
param aksEtcdKVEnableSoftDelete bool = true
param aksClusterOutboundIPAddressIPTags string = ''
param regionalResourceGroup string
param istioIngressGatewayIPAddressName string
param istioIngressGatewayIPAddressIPTags string = ''
param opsIngressGatewayIPAddressName string = ''
param opsIngressGatewayIPAddressTags string = ''
param genevaActionsServiceTag string
param sreServiceTag string

@description('Reference the pre-created AKS identity in the service deployment subscription.')
param useLeasedInfrastructureIdentities bool = false
param infrastructureIdentityResourceGroup string = ''
param leasedAksIdentityName string = ''

@description('The name of the service keyvault')
param serviceKeyVaultName string

@description('The name of the resource group for the service keyvault')
param serviceKeyVaultResourceGroup string = resourceGroup().name

@description('The subscription ID where the service keyvault resource group lives. Defaults to the current subscription. Set when the keyvault is shared across subscriptions.')
param serviceKeyVaultSubscription string = subscription().subscriptionId

@description('The location of the resource group for the service keyvault')
param serviceKeyVaultLocation string = resourceGroup().location

@description('Soft delete setting for service keyvault')
param serviceKeyVaultSoftDelete bool = true

@description('If true, make the service keyvault private and only accessible by the svc cluster via private link.')
param serviceKeyVaultPrivate bool = true

// KV tagging
param serviceKeyVaultTagName string
param serviceKeyVaultTagValue string

@description('KV certificate officer principal ID')
param kvCertOfficerPrincipalId string

@description('MSI that will be used during pipeline runs')
param globalMSIId string

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

// this is mostly a situation where multiple svc-infra pipelines run towards
// a shared svc keyvault resource group ${serviceKeyVaultResourceGroup}. while
// the individual modules will not conflict with each other, the existence
// of same-named deployments fails one pipeline.
var deploymentNameSuffix = uniqueString(resourceGroup().id)

module serviceKeyVault '../modules/keyvault/keyvault.bicep' = {
  name: 'svc-kv-${deploymentNameSuffix}'
  scope: resourceGroup(serviceKeyVaultSubscription, serviceKeyVaultResourceGroup)
  params: {
    location: serviceKeyVaultLocation
    keyVaultName: serviceKeyVaultName
    private: serviceKeyVaultPrivate
    enableSoftDelete: serviceKeyVaultSoftDelete
    tagKey: serviceKeyVaultTagName
    tagValue: serviceKeyVaultTagValue
  }
}

module serviceKeyVaultCertOfficer '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: 'svc-kv-cert-officer-${deploymentNameSuffix}'
  scope: resourceGroup(serviceKeyVaultSubscription, serviceKeyVaultResourceGroup)
  params: {
    keyVaultName: serviceKeyVaultName
    roleName: 'Key Vault Certificates Officer'
    managedIdentityPrincipalIds: [kvCertOfficerPrincipalId]
  }
  dependsOn: [
    serviceKeyVault
  ]
}

module serviceKeyVaultSecretsOfficer '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: 'svc-kv-secret-officer-${deploymentNameSuffix}'
  scope: resourceGroup(serviceKeyVaultSubscription, serviceKeyVaultResourceGroup)
  params: {
    keyVaultName: serviceKeyVaultName
    roleName: 'Key Vault Secrets Officer'
    managedIdentityPrincipalIds: [kvCertOfficerPrincipalId]
  }
  dependsOn: [
    serviceKeyVault
  ]
}

module serviceKeyVaultDevopsSecretsOfficer '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: 'svc-kv-devops-secret-officer-${deploymentNameSuffix}'
  scope: resourceGroup(serviceKeyVaultSubscription, serviceKeyVaultResourceGroup)
  params: {
    keyVaultName: serviceKeyVaultName
    roleName: 'Key Vault Secrets Officer'
    managedIdentityPrincipalIds: [reference(globalMSIId, '2023-01-31').principalId]
  }
  dependsOn: [
    serviceKeyVault
  ]
}
resource aksClusterUserDefinedManagedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = if (!useLeasedInfrastructureIdentities) {
  name: '${aksClusterName}-msi'
  location: location
}

var aksClusterUserDefinedManagedIdentityName = useLeasedInfrastructureIdentities
  ? leasedAksIdentityName
  : aksClusterUserDefinedManagedIdentity.name

// A second resource declaration can alias the created identity when leasing is disabled.
var leasedAksClusterUserDefinedManagedIdentityId = resourceId(
  subscription().subscriptionId,
  infrastructureIdentityResourceGroup,
  'Microsoft.ManagedIdentity/userAssignedIdentities',
  aksClusterUserDefinedManagedIdentityName
)
var aksClusterUserDefinedManagedIdentityPrincipalId = useLeasedInfrastructureIdentities
  ? reference(leasedAksClusterUserDefinedManagedIdentityId, '2023-01-31').principalId
  : aksClusterUserDefinedManagedIdentity!.properties.principalId

module istioIngressGatewayIPAddress '../modules/network/publicipaddress.bicep' = {
  name: istioIngressGatewayIPAddressName
  scope: resourceGroup(regionalResourceGroup)
  params: {
    name: istioIngressGatewayIPAddressName
    ipTags: istioIngressGatewayIPAddressIPTags
    location: location
    zones: locationAvailabilityZoneList
    // Role Assignment needed for the public IP address to be used on the Load Balancer
    roleAssignmentProperties: {
      principalId: aksClusterUserDefinedManagedIdentityPrincipalId
      principalType: 'ServicePrincipal'
      // Network Contributor Role
      // https://www.azadvertizer.net/azrolesadvertizer/4d97b98b-1d4f-4787-a291-c67834d212e7.html
      roleDefinitionId: subscriptionResourceId(
        'Microsoft.Authorization/roleDefinitions/',
        '4d97b98b-1d4f-4787-a291-c67834d212e7'
      )
    }
  }
}

resource svcClusterNSG 'Microsoft.Network/networkSecurityGroups@2023-11-01' = {
  location: location
  name: 'svc-cluster-node-nsg'
  properties: {
    securityRules: [
      {
        name: 'rp-in-arm'
        properties: {
          access: 'Allow'
          destinationAddressPrefix: istioIngressGatewayIPAddress.outputs.ipAddress
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
          destinationAddressPrefix: opsIngressGatewayIPAddress.outputs.ipAddress
          destinationPortRange: '443'
          direction: 'Inbound'
          priority: 130
          protocol: 'Tcp'
          sourceAddressPrefix: genevaActionsServiceTag
          sourcePortRange: '*'
        }
      }
      {
        name: 'sre-in-ops'
        properties: {
          access: 'Allow'
          destinationAddressPrefix: opsIngressGatewayIPAddress.outputs.ipAddress
          destinationPortRange: '443'
          direction: 'Inbound'
          priority: 140
          protocol: 'Tcp'
          sourceAddressPrefix: sreServiceTag
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

module opsIngressGatewayIPAddress '../modules/network/publicipaddress.bicep' = if (!empty(opsIngressGatewayIPAddressName)) {
  name: opsIngressGatewayIPAddressName
  scope: resourceGroup(regionalResourceGroup)
  params: {
    name: opsIngressGatewayIPAddressName
    ipTags: opsIngressGatewayIPAddressTags
    location: location
    zones: length(locationAvailabilityZoneList) > 0 ? locationAvailabilityZoneList : null
    // Role Assignment needed for the public IP address to be used on the Load Balancer
    roleAssignmentProperties: {
      principalId: aksClusterUserDefinedManagedIdentityPrincipalId
      principalType: 'ServicePrincipal'
      // Network Contributor Role - needed for the AKS managed identity to use the public IP on the LoadBalancer
      // https://www.azadvertizer.net/azrolesadvertizer/4d97b98b-1d4f-4787-a291-c67834d212e7.html
      roleDefinitionId: subscriptionResourceId(
        'Microsoft.Authorization/roleDefinitions/',
        '4d97b98b-1d4f-4787-a291-c67834d212e7'
      )
    }
  }
}

resource vnet 'Microsoft.Network/virtualNetworks@2024-05-01' existing = {
  name: vnetName
}

resource aksPodSubnet 'Microsoft.Network/virtualNetworks/subnets@2023-11-01' = {
  parent: vnet
  name: 'PodSubnet-001'
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
  dependsOn: [
    nodeSubnetCreation
  ]
}

var networkContributorRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions/',
  '4d97b98b-1d4f-4787-a291-c67834d212e7'
)
var managedIdentityId = useLeasedInfrastructureIdentities
  ? leasedAksClusterUserDefinedManagedIdentityId
  : aksClusterUserDefinedManagedIdentity!.id

resource aksNetworkContributorRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: vnet
  name: guid(
    managedIdentityId,
    networkContributorRoleId,
    resourceId('Microsoft.Network/virtualNetworks/subnets', vnetName, nodeSubnetName)
  )
  properties: {
    roleDefinitionId: networkContributorRoleId
    principalId: aksClusterUserDefinedManagedIdentityPrincipalId
    principalType: 'ServicePrincipal'
  }
  dependsOn: [
    nodeSubnetCreation
  ]
}

var aksClusterOutboundIPAddressName = '${aksClusterName}-outbound-ip'
module aksClusterOutboundIPAddress '../modules/network/publicipaddress.bicep' = {
  name: aksClusterOutboundIPAddressName
  scope: resourceGroup(regionalResourceGroup)
  params: {
    name: aksClusterOutboundIPAddressName
    ipTags: aksClusterOutboundIPAddressIPTags
    location: location
    zones: length(locationAvailabilityZoneList) > 0 ? locationAvailabilityZoneList : null
    roleAssignmentProperties: {
      principalId: aksClusterUserDefinedManagedIdentityPrincipalId
      principalType: 'ServicePrincipal'
      roleDefinitionId: networkContributorRoleId
    }
  }
}

module aks_keyvault_builder '../modules/keyvault/keyvault.bicep' = {
  name: aksKeyVaultName
  params: {
    location: location
    keyVaultName: aksKeyVaultName
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

var keyVaultCryptoUserId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  '12338af0-0e69-4776-bea7-57ae8d297424'
)

resource aks_keyvault_crypto_user 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(managedIdentityId, keyVaultCryptoUserId, aks_keyvault.id)
  scope: aks_keyvault
  properties: {
    roleDefinitionId: keyVaultCryptoUserId
    principalId: aksClusterUserDefinedManagedIdentityPrincipalId
    principalType: 'ServicePrincipal'
  }
}

output nodeSubnetId string = nodeSubnetCreation.outputs.subnetId
output podSubnetId string = aksPodSubnet.id
output vnetId string = vnetCreation.outputs.vnetId
output outboundIPResourceId string = aksClusterOutboundIPAddress.outputs.resourceId
output managedIdentityId string = managedIdentityId
output etcdKeyUriWithVersion string = aks_etcd_kms.properties.keyUriWithVersion
output etcdKeyVaultId string = aks_keyvault_builder.outputs.kvId
output locationAvailabilityZones string = locationAvailabilityZones
