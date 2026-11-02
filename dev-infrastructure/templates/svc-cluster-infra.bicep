import {
  csvToArray
  getLocationAvailabilityZonesCSV
} from '../modules/common.bicep'

@description('Azure Region Location')
param location string = resourceGroup().location

@description('Availability Zones to use for the infrastructure, as a CSV string. Defaults to all the zones of the location')
param locationAvailabilityZones string = getLocationAvailabilityZonesCSV(location)

@description('AKS cluster name')
param aksClusterName string

@description('VNET address prefix')
param vnetAddressPrefix string

@description('Subnet address prefix')
param subnetPrefix string

@description('Specifies the address prefix of the subnet hosting the pods of the AKS cluster.')
param podSubnetPrefix string

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

@description('Admin API Istio Ingress Gateway IP Address Name')
param opsIngressGatewayIPAddressName string = ''

@description('IPTags to be set on the Admin API Istio Ingress Gateway IP address in the format of ipTagType:tag,ipTagType:tag')
param opsIngressGatewayIPAddressTags string = ''

@description('The resourcegroup for regional infrastructure')
param regionalResourceGroup string

@description('The service tag for Geneva Actions')
param genevaActionsServiceTag string

@description('The service tag for SRE access')
param sreServiceTag string

@description('Reference pre-created infrastructure identities instead of creating identities in the service resource group.')
param useLeasedInfrastructureIdentities bool = false

@description('Resource group containing the pre-created service infrastructure identities in the service deployment subscription.')
param infrastructureIdentityResourceGroup string = ''

@description('Name of the pre-created service AKS control-plane identity. Required when useLeasedInfrastructureIdentities is true.')
param leasedAksIdentityName string = ''

var locationAvailabilityZoneList = csvToArray(locationAvailabilityZones)

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

var aksClusterUserDefinedManagedIdentityId = useLeasedInfrastructureIdentities
  ? leasedAksClusterUserDefinedManagedIdentityId
  : aksClusterUserDefinedManagedIdentity.id

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

@description('Additional Allow/Inbound NSG rules as CSV: name=priority:protocol:source:destination:destinationPort')
param additionalIngressRules string = ''

var additionalIngressRuleEntries = [
  for rule in csvToArray(additionalIngressRules): {
    name: split(rule, '=')[0]
    fields: split(split(rule, '=')[1], ':')
  }
]
var additionalIngressSecurityRules = [
  for rule in additionalIngressRuleEntries: {
    name: rule.name
    properties: {
      access: 'Allow'
      direction: 'Inbound'
      priority: int(rule.fields[0])
      protocol: rule.fields[1]
      sourceAddressPrefix: rule.fields[2]
      sourcePortRange: '*'
      destinationAddressPrefix: rule.fields[3]
      destinationPortRange: rule.fields[4]
    }
  }
]

resource svcClusterNSG 'Microsoft.Network/networkSecurityGroups@2023-11-01' = {
  location: location
  name: 'svc-cluster-node-nsg'
  properties: {
    securityRules: concat(
      [
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
      ],
      additionalIngressSecurityRules
    )
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
    aksClusterUserDefinedManagedIdentityId,
    networkContributorRoleId,
    resourceId('Microsoft.Network/virtualNetworks/subnets', vnetName, nodeSubnetName)
  )
  dependsOn: [
    vnetCreation
  ]
  properties: {
    roleDefinitionId: networkContributorRoleId
    principalId: aksClusterUserDefinedManagedIdentityPrincipalId
    principalType: 'ServicePrincipal'
  }
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
    // Role Assignment needed for the public IP address to be used on the Load Balancer
    roleAssignmentProperties: {
      principalId: aksClusterUserDefinedManagedIdentityPrincipalId
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
  name: guid(aksClusterUserDefinedManagedIdentityId, keyVaultCryptoUserId, aks_keyvault.id)
  scope: aks_keyvault
  properties: {
    roleDefinitionId: keyVaultCryptoUserId
    principalId: aksClusterUserDefinedManagedIdentityPrincipalId
    principalType: 'ServicePrincipal'
  }
}

// Outputs consumed by the aks-cluster-create pipeline step
output nodeSubnetId string = nodeSubnetCreation.outputs.subnetId
output podSubnetId string = aksPodSubnet.id
output outboundIPResourceId string = aksClusterOutboundIPAddress.outputs.resourceId
output etcdKeyUriWithVersion string = aks_etcd_kms.properties.keyUriWithVersion
output managedIdentityId string = aksClusterUserDefinedManagedIdentityId

// Outputs consumed by svc-cluster.bicep
output vnetId string = vnet.id
output istioIngressGatewayIP string = istioIngressGatewayIPAddress.outputs.ipAddress
output opsIngressGatewayIP string = empty(opsIngressGatewayIPAddressName) ? '' : opsIngressGatewayIPAddress!.outputs.ipAddress
