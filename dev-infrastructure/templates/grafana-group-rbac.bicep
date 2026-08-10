// Grants a deployment MSI a constrained "Role Based Access Control
// Administrator" role on a Grafana resource so a companion `grafana-group-roles`
// Shell step can assign Grafana roles to Group (or User) principals.
//
// Why this exists: the built-in `grafana-roles` ARM step runs under the EV2
// compound identity, whose Microsoft.Authorization/roleAssignments/write is
// ABAC-restricted to principalType == 'ServicePrincipal', so it can only assign
// ServicePrincipal principals. A Shell step runs under an identity of our choice
// (this MSI) which, once it holds the grant below, can also assign Group/User
// principals.
//
// Least privilege: mirrors templates/ci-bot-rbac-subscription.bicep — RBAC
// Administrator with an ABAC condition that forbids assigning or removing the
// privilege-escalation roles (Owner, User Access Administrator, RBAC
// Administrator). Scope is the single Grafana resource, not the subscription.

@description('Grafana resource name')
param grafanaName string

@description('The deployment MSI name (identity used by the shell step)')
param globalMSIName string

// https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles/privileged#role-based-access-control-administrator
var rbacAdminRole = 'f58310d9-a9f6-439a-9e8d-f62e7b41a168'

// Privilege-escalation roles the MSI must never be able to assign or remove.
var ownerRole = '8e3af657-a8ff-443c-a75c-2fe8c4bcb635'
var userAccessAdminRole = '18d7d88d-d35e-4fb5-a5c3-7773c20a72d9'

resource grafana 'Microsoft.Dashboard/grafana@2024-10-01' existing = {
  name: grafanaName
}

resource globalMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: globalMSIName
}

resource grafanaGroupRbacAdmin 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(grafana.id, globalMSI.id, rbacAdminRole)
  scope: grafana
  properties: {
    principalId: globalMSI.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', rbacAdminRole)
    condition: '((!(ActionMatches{\'Microsoft.Authorization/roleAssignments/write\'})) OR (@Request[Microsoft.Authorization/roleAssignments:RoleDefinitionId] ForAnyOfAllValues:GuidNotEquals {${ownerRole}, ${userAccessAdminRole}, ${rbacAdminRole}})) AND ((!(ActionMatches{\'Microsoft.Authorization/roleAssignments/delete\'})) OR (@Resource[Microsoft.Authorization/roleAssignments:RoleDefinitionId] ForAnyOfAllValues:GuidNotEquals {${ownerRole}, ${userAccessAdminRole}, ${rbacAdminRole}}))'
    conditionVersion: '2.0'
    description: 'Deployment MSI: assign Grafana roles except Owner, UAA, RBAC Administrator'
  }
}

// Consumed by the grafana-group-roles Shell step (which already depends on this
// step) so it doesn't need to depend on a separate output step for the value.
output subscriptionId string = subscription().subscriptionId
