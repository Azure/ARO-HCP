@description('Principal ID of the Fleet managed identity')
param fleetPrincipalId string

// Monitoring Contributor role
// https://www.azadvertizer.net/azrolesadvertizer/749f88d5-cbae-40b8-bcfc-e573ddc772fa.html
//
// The fleet controller needs Microsoft.AlertsManagement/actionRules read and delete in
// order to reap expired alert processing rules. A custom least-privilege role is not an
// option: the platform cannot currently support an additional custom role across every
// target environment, so Monitoring Contributor is the narrowest built-in role that grants actionRules access.
// It is write-capable over monitoring resources, so the grant is scoped to this single
// regional resource group to contain the blast radius. See ARO-29669 / PR #7020, which
// made the same tradeoff for the Admin API identity in this same resource group.
var monitoringContributorRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  '749f88d5-cbae-40b8-bcfc-e573ddc772fa'
)

// The assignment name is derived from the principal ID rather than the identity's
// resource ID. A resource ID is name-based and so survives a delete and recreate of the
// identity unchanged, which would hold this name fixed while the principal ID changed.
// A role assignment's principal is immutable, so ARM would then attempt to mutate the
// existing assignment and fail with RoleAssignmentUpdateNotPermitted. That is reachable
// here because the identity lives in the service resource group while this assignment
// lives in the regional one, so recreating the identity can leave the assignment behind.
// Keying on the principal ID means a recreated identity gets a new assignment instead.
resource fleetMonitoringContributorRole 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: resourceGroup()
  name: guid(resourceGroup().id, fleetPrincipalId, monitoringContributorRoleId)
  properties: {
    roleDefinitionId: monitoringContributorRoleId
    principalId: fleetPrincipalId
    principalType: 'ServicePrincipal'
  }
}
