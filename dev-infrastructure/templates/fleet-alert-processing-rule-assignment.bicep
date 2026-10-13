@description('The name of the Fleet managed identity')
param fleetMIName string

@description('The resource group containing the Fleet managed identity')
param fleetMIResourceGroup string

// Monitoring Contributor role
// https://www.azadvertizer.net/azrolesadvertizer/749f88d5-cbae-40b8-bcfc-e573ddc772fa.html
//
// The fleet controller needs Microsoft.AlertsManagement/actionRules read and delete in
// order to reap expired alert processing rules. A custom least-privilege role is not an
// option: the MSIT and AME tenants have already reached their custom-role quota, so
// Monitoring Contributor is the narrowest built-in role that grants actionRules access.
// It is write-capable over monitoring resources, so the grant is scoped to this single
// regional resource group to contain the blast radius. See ARO-29669 / PR #7020, which
// made the same tradeoff for the Admin API identity in this same resource group.
var monitoringContributorRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  '749f88d5-cbae-40b8-bcfc-e573ddc772fa'
)

resource fleetMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup(fleetMIResourceGroup)
  name: fleetMIName
}

resource fleetMonitoringContributorRole 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: resourceGroup()
  name: guid(resourceGroup().id, fleetMSI.id, monitoringContributorRoleId)
  properties: {
    roleDefinitionId: monitoringContributorRoleId
    principalId: fleetMSI.properties.principalId
    principalType: 'ServicePrincipal'
  }
}
