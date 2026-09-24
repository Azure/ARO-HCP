import { monitoringWorkspaceRefFromId } from '../modules/resource.bicep'

@description('The name of the Fleet managed identity')
param fleetMIName string

@description('The resource group containing the Fleet managed identity')
param fleetMIResourceGroup string

@description('The name of the SVC Azure Monitor Workspace')
param svcMonitorName string

@description('The name of the HCP Azure Monitor Workspace')
param hcpMonitorName string

@description('Optional existing SVC Azure Monitor Workspace resource ID; defaults to the regional workspace')
param svcWorkspaceResourceId string = ''

@description('Optional existing HCP Azure Monitor Workspace resource ID; defaults to the regional workspace')
param hcpWorkspaceResourceId string = ''

// Contributor role
// https://www.azadvertizer.net/azrolesadvertizer/b24988ac-6180-42a0-ab88-20f7382dd24c.html
var contributorRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'b24988ac-6180-42a0-ab88-20f7382dd24c'
)

// Resolve the job identity in this subscription, not the external workspace subscription.
resource fleetMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup(fleetMIResourceGroup)
  name: fleetMIName
}

resource svcMonitor 'Microsoft.Monitor/accounts@2021-06-03-preview' existing = {
  name: svcMonitorName
}

resource hcpMonitor 'Microsoft.Monitor/accounts@2021-06-03-preview' existing = {
  name: hcpMonitorName
}

var workspaceIds = union(
  [toLower(empty(svcWorkspaceResourceId) ? svcMonitor.id : svcWorkspaceResourceId)],
  [toLower(empty(hcpWorkspaceResourceId) ? hcpMonitor.id : hcpWorkspaceResourceId)]
)

// Explicit IDs can reference either local workspace; retain their persisted assignment names.
resource svcMonitorContributorRole 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (contains(workspaceIds, toLower(svcMonitor.id))) {
  scope: svcMonitor
  name: guid(svcMonitor.id, fleetMSI.id, contributorRoleId)
  properties: {
    roleDefinitionId: contributorRoleId
    principalId: fleetMSI.properties.principalId
    principalType: 'ServicePrincipal'
  }
}

resource hcpMonitorContributorRole 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (contains(workspaceIds, toLower(hcpMonitor.id)) && toLower(hcpMonitor.id) != toLower(svcMonitor.id)) {
  scope: hcpMonitor
  name: guid(hcpMonitor.id, fleetMSI.id, contributorRoleId)
  properties: {
    roleDefinitionId: contributorRoleId
    principalId: fleetMSI.properties.principalId
    principalType: 'ServicePrincipal'
  }
}

// Local targets are already covered above, even when selected by the other metrics stream.
var externalWorkspaceIds = filter(
  workspaceIds,
  id => id != toLower(svcMonitor.id) && id != toLower(hcpMonitor.id)
)
var externalWorkspaceRefs = map(externalWorkspaceIds, id => monitoringWorkspaceRefFromId(id))

module externalMonitorContributorRoles '../modules/metrics/amw-role-assignment.bicep' = [for workspace in externalWorkspaceRefs: {
  name: 'fleet-amw-${uniqueString(fleetMSI.id, workspace.resourceGroup.subscriptionId, workspace.resourceGroup.name, workspace.name)}'
  scope: resourceGroup(workspace.resourceGroup.subscriptionId, workspace.resourceGroup.name)
  params: {
    workspaceName: workspace.name
    principalId: fleetMSI.properties.principalId
    roleDefinitionId: 'b24988ac-6180-42a0-ab88-20f7382dd24c'
  }
}]

@description('Job-owned assignments on external workspaces; delete these assignments, not the workspaces, during job cleanup')
output externalRoleAssignmentIds array = [for (_, i) in externalWorkspaceRefs: externalMonitorContributorRoles[i].outputs.roleAssignmentId]
