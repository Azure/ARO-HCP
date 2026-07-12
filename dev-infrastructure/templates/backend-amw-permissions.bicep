@description('The name of the Backend managed identity')
param backendMIName string

@description('The resource group containing the Backend managed identity')
param backendMIResourceGroup string

@description('The name of the SVC Azure Monitor Workspace')
param svcMonitorName string

@description('The name of the HCP Azure Monitor Workspace')
param hcpMonitorName string

// Contributor role
// https://www.azadvertizer.net/azrolesadvertizer/b24988ac-6180-42a0-ab88-20f7382dd24c.html
var contributorRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'b24988ac-6180-42a0-ab88-20f7382dd24c'
)

resource backendMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup(backendMIResourceGroup)
  name: backendMIName
}

resource svcMonitor 'Microsoft.Monitor/accounts@2021-06-03-preview' existing = {
  name: svcMonitorName
}

resource hcpMonitor 'Microsoft.Monitor/accounts@2021-06-03-preview' existing = {
  name: hcpMonitorName
}

resource svcMonitorContributorRole 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: svcMonitor
  name: guid(svcMonitor.id, backendMSI.id, contributorRoleId)
  properties: {
    roleDefinitionId: contributorRoleId
    principalId: backendMSI.properties.principalId
    principalType: 'ServicePrincipal'
  }
}

resource hcpMonitorContributorRole 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: hcpMonitor
  name: guid(hcpMonitor.id, backendMSI.id, contributorRoleId)
  properties: {
    roleDefinitionId: contributorRoleId
    principalId: backendMSI.properties.principalId
    principalType: 'ServicePrincipal'
  }
}
