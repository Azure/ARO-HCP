targetScope = 'subscription'

@description('Principal ID for aro-dev-first-party2')
param firstPartyPrincipalId string

@description('Principal ID for aro-dev-arm-helper2')
param armHelperPrincipalId string

@description('Principal ID for aro-dev-msi-mock2')
param miMockPrincipalId string

@description('Pooled MSI mock principals that also need customer-subscription access')
param msiMockPoolPrincipals array = []

@description('Custom role name for the first-party mock principal')
param firstPartyRoleName string = 'dev-first-party-mock'

@description('Custom role name for the MSI mock principal')
param msiMockRoleName string = 'dev-msi-mock'

var contributorRoleDefinitionId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'b24988ac-6180-42a0-ab88-20f7382dd24c'
)
var rbacAdminRoleDefinitionId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'f58310d9-a9f6-439a-9e8d-f62e7b41a168'
)
// Built-in 'Key Vault Crypto User' role. In dev/int the single mock MSI service
// principal backs every cluster operator identity (see the hardcoded MI dataplane
// client), so when a cluster enables etcd encryption the KMS plugin authenticates
// as this principal and needs Key Vault crypto access. This matches the role the
// product assigns to the per-cluster KMS identity (cluster_scoped_identities_config.go).
var kmsCryptoUserRoleDefinitionId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  '12338af0-0e69-4776-bea7-57ae8d297424'
)

// Custom role definitions — defined locally in the target subscription so there
// is no cross-subscription dependency on assignableScopes. Azure enforces custom
// role display-name (roleName) uniqueness per tenant, so each subscription's copy
// suffixes the display name with the subscription id to avoid
// RoleDefinitionWithSameNameExists. The role-definition resource id
// (guid(subscription().id, roleName)) is already per-subscription unique.

resource firstPartyRole 'Microsoft.Authorization/roleDefinitions@2022-04-01' = {
  name: guid(subscription().id, firstPartyRoleName)
  properties: {
    roleName: '${firstPartyRoleName}-${subscription().subscriptionId}'
    description: 'ARO HCP Dev Role for mock 1p service principal'
    type: 'CustomRole'
    permissions: [
      {
        actions: [
          'Microsoft.Network/virtualNetworks/subnets/serviceAssociationLinks/delete'
          'Microsoft.Network/virtualNetworks/subnets/serviceAssociationLinks/write'
          'Microsoft.Network/virtualNetworks/subnets/serviceAssociationLinks/read'
          'Microsoft.Network/virtualNetworks/subnets/serviceAssociationLinks/details/read'
          'Microsoft.Network/virtualNetworks/subnets/serviceAssociationLinks/validate/action'
          'Microsoft.Resources/subscriptions/resourceGroups/read'
          'Microsoft.Resources/subscriptions/resourceGroups/write'
        ]
        notActions: []
      }
    ]
    assignableScopes: [
      subscription().id
    ]
  }
}

resource msiMockRole 'Microsoft.Authorization/roleDefinitions@2022-04-01' = {
  name: guid(subscription().id, msiMockRoleName)
  properties: {
    roleName: '${msiMockRoleName}-${subscription().subscriptionId}'
    description: 'ARO HCP Dev Role for MSI mock principal'
    type: 'CustomRole'
    permissions: [
      {
        actions: [
          'Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/delete'
          'Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/read'
          'Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/write'
          'Microsoft.ManagedIdentity/userAssignedIdentities/read'
          'Microsoft.ManagedIdentity/userAssignedIdentities/assign/action'
          'Microsoft.Network/loadBalancers/backendAddressPools/read'
          'Microsoft.Network/loadBalancers/backendAddressPools/write'
          'Microsoft.Network/loadBalancers/read'
          'Microsoft.Network/loadBalancers/write'
          'Microsoft.Network/natGateways/join/action'
          'Microsoft.Network/natGateways/read'
          'Microsoft.Network/networkSecurityGroups/join/action'
          'Microsoft.Network/networkSecurityGroups/read'
          'Microsoft.Network/networkSecurityGroups/write'
          'Microsoft.Network/privateDnsZones/virtualNetworkLinks/read'
          'Microsoft.Network/privateDnsZones/virtualNetworkLinks/write'
          'Microsoft.Network/routeTables/join/action'
          'Microsoft.Network/routeTables/read'
          'Microsoft.Network/virtualNetworks/join/action'
          'Microsoft.Network/virtualNetworks/joinLoadBalancer/action'
          'Microsoft.Network/virtualNetworks/read'
          'Microsoft.Network/virtualNetworks/subnets/join/action'
          'Microsoft.Network/virtualNetworks/subnets/read'
          'Microsoft.Network/virtualNetworks/subnets/write'
          'Microsoft.Compute/diskEncryptionSets/read'
        ]
        notActions: []
      }
    ]
    assignableScopes: [
      subscription().id
    ]
  }
}

// Role assignments

resource firstPartyRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(subscription().id, firstPartyPrincipalId, firstPartyRole.id)
  scope: subscription()
  properties: {
    principalId: firstPartyPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: firstPartyRole.id
  }
}

resource armHelperContributorRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(subscription().id, armHelperPrincipalId, contributorRoleDefinitionId)
  scope: subscription()
  properties: {
    principalId: armHelperPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: contributorRoleDefinitionId
  }
}

resource armHelperRbacAdminRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(subscription().id, armHelperPrincipalId, rbacAdminRoleDefinitionId)
  scope: subscription()
  properties: {
    principalId: armHelperPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: rbacAdminRoleDefinitionId
  }
}

resource miMockRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(subscription().id, miMockPrincipalId, msiMockRole.id)
  scope: subscription()
  properties: {
    principalId: miMockPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: msiMockRole.id
  }
}

resource miMockKmsRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(subscription().id, miMockPrincipalId, kmsCryptoUserRoleDefinitionId)
  scope: subscription()
  properties: {
    principalId: miMockPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: kmsCryptoUserRoleDefinitionId
  }
}

resource pooledMiMockRoleAssignments 'Microsoft.Authorization/roleAssignments@2022-04-01' = [
  for principal in msiMockPoolPrincipals: {
    name: guid(subscription().id, principal.principalId, msiMockRole.id)
    scope: subscription()
    properties: {
      principalId: principal.principalId
      principalType: 'ServicePrincipal'
      roleDefinitionId: msiMockRole.id
    }
  }
]

resource pooledMiMockKmsRoleAssignments 'Microsoft.Authorization/roleAssignments@2022-04-01' = [
  for principal in msiMockPoolPrincipals: {
    name: guid(subscription().id, principal.principalId, kmsCryptoUserRoleDefinitionId)
    scope: subscription()
    properties: {
      principalId: principal.principalId
      principalType: 'ServicePrincipal'
      roleDefinitionId: kmsCryptoUserRoleDefinitionId
    }
  }
]
