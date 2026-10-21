@description('The resource ID of the user-assigned managed identity that manages the VNet')
param deploymentMsiId string

// Network Contributor Role
// https://www.azadvertizer.net/azrolesadvertizer/4d97b98b-1d4f-4787-a291-c67834d212e7.html
var networkContributorRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions/',
  '4d97b98b-1d4f-4787-a291-c67834d212e7'
)

// Tag Contributor Role
// https://www.azadvertizer.net/azrolesadvertizer/4a9ae827-6dc8-4573-8ac7-8239d42aa03f.html
var tagContributorRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions/',
  '4a9ae827-6dc8-4573-8ac7-8239d42aa03f'
)

resource deploymentMsiNetworkContributorRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: resourceGroup()
  name: guid(deploymentMsiId, networkContributorRoleId, resourceGroup().id)
  properties: {
    roleDefinitionId: networkContributorRoleId
    principalId: reference(deploymentMsiId, '2023-01-31').principalId
    principalType: 'ServicePrincipal'
  }
}

resource deploymentMsiTagContributorRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: resourceGroup()
  name: guid(deploymentMsiId, tagContributorRoleId, resourceGroup().id)
  properties: {
    roleDefinitionId: tagContributorRoleId
    principalId: reference(deploymentMsiId, '2023-01-31').principalId
    principalType: 'ServicePrincipal'
  }
}
