targetScope = 'subscription'

@description('Global resource group name')
param globalResourceGroupName string = 'global'

@description('The name of the first party identity role')
param firstPartyRoleName string = 'dev-first-party-mock'

@description('The name of the MSI mock identity role')
param msiMockRoleName string = 'dev-msi-mock'

@description('Explicit E2E customer subscription IDs that need these roles in assignableScopes')
param e2eTestSubscriptions array = []

var e2eTestSubscriptionScopes = [for subscriptionId in e2eTestSubscriptions: '/subscriptions/${subscriptionId}']

resource customRole 'Microsoft.Authorization/roleDefinitions@2022-04-01' = {
  name: guid(subscription().id, firstPartyRoleName)
  properties: {
    roleName: firstPartyRoleName
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
    assignableScopes: concat(
      [
        subscription().id
        subscriptionResourceId('Microsoft.Resources/resourceGroups/', globalResourceGroupName)
      ],
      e2eTestSubscriptionScopes
    )
  }
}

resource msiCustomRole 'Microsoft.Authorization/roleDefinitions@2022-04-01' = {
  name: guid(subscription().id, msiMockRoleName)
  properties: {
    roleName: msiMockRoleName
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
    assignableScopes: concat(
      [
        subscription().id
        subscriptionResourceId('Microsoft.Resources/resourceGroups/', globalResourceGroupName)
      ],
      e2eTestSubscriptionScopes
    )
  }
}
