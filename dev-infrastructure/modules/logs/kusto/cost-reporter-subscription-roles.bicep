targetScope = 'subscription'

@description('Principal ID of the Kusto cost reporting managed identity')
param principalId string

// Cost Management Reader reads the actual cost of the Kusto resources in this
// subscription; Reader lets Resource Graph inventory those resources.
// https://www.azadvertizer.net/azrolesadvertizer/72fafb9e-0641-4937-9268-a91bfd8191a3.html
// https://www.azadvertizer.net/azrolesadvertizer/acdd72a7-3385-48ef-bd42-f606fba81ae7.html
var roleDefinitionIds = [
  subscriptionResourceId('Microsoft.Authorization/roleDefinitions', '72fafb9e-0641-4937-9268-a91bfd8191a3')
  subscriptionResourceId('Microsoft.Authorization/roleDefinitions', 'acdd72a7-3385-48ef-bd42-f606fba81ae7')
]

resource roleAssignments 'Microsoft.Authorization/roleAssignments@2022-04-01' = [
  for roleDefinitionId in roleDefinitionIds: {
    name: guid(subscription().id, principalId, roleDefinitionId)
    properties: {
      principalId: principalId
      principalType: 'ServicePrincipal'
      roleDefinitionId: roleDefinitionId
    }
  }
]
