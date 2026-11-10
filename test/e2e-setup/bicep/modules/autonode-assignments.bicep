targetScope = 'resourceGroup'

@description('Name of the dp-autonode identity')
param dpAutoNodeMiName string

@description('Resource group name where the dp-autonode identity is located')
param resourceGroupName string

@description('The Network security group name for the HCP cluster resources')
param nsgName string

@description('The virtual network name for the HCP cluster resources')
param vnetName string

@description('RBAC scope to use for role assignments: resourceGroup or resource')
@allowed([
  'resource'
  'resourceGroup'
])
param rbacScope string = 'resourceGroup'

//
// E X I S T I N G   R E S O U R C E S
//

resource vnet 'Microsoft.Network/virtualNetworks@2022-07-01' existing = {
  name: vnetName
}

resource nsg 'Microsoft.Network/networkSecurityGroups@2022-07-01' existing = {
  name: nsgName
}

resource dpAutoNodeMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: dpAutoNodeMiName
  scope: resourceGroup(resourceGroupName)
}

// Network Contributor: grants AutoNode the virtualNetworks/subnets/join/action it needs to attach provisioned
// nodes to the customer's subnet. Virtual Machine Contributor and Managed Identity Operator are granted
// separately by the RP against the managed resource group, which doesn't exist yet when this bicep runs.
// https://www.azadvertizer.net/azrolesadvertizer/4d97b98b-1d4f-4787-a291-c67834d212e7.html
var networkContributorRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  '4d97b98b-1d4f-4787-a291-c67834d212e7'
)

resource dpAutoNodeNetworkContributorRoleResourceGroupAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (rbacScope == 'resourceGroup') {
  name: guid(resourceGroup().id, dpAutoNodeMi.id, networkContributorRoleId)
  scope: resourceGroup()
  properties: {
    principalId: dpAutoNodeMi.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: networkContributorRoleId
  }
}

resource dpAutoNodeNetworkContributorRoleVnetAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (rbacScope == 'resource') {
  name: guid(resourceGroup().id, dpAutoNodeMi.id, networkContributorRoleId, vnet.id)
  scope: vnet
  properties: {
    principalId: dpAutoNodeMi.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: networkContributorRoleId
  }
}

// No subnet-scoped assignment: the VNet-scoped grant above already covers the subnet via RBAC scope inheritance.

resource dpAutoNodeNetworkContributorRoleNsgAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (rbacScope == 'resource') {
  name: guid(resourceGroup().id, dpAutoNodeMi.id, networkContributorRoleId, nsg.id)
  scope: nsg
  properties: {
    principalId: dpAutoNodeMi.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: networkContributorRoleId
  }
}
