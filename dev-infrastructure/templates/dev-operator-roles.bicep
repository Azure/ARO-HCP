// CustomRoles for Platform Workload Identities for development environment

targetScope = 'subscription'

@description('Array of roles for platform workload identity')
param roles array = []

// E2E customer subscriptions are kept in these role definitions' assignableScopes
// during the migration to per-subscription (self-contained) role definitions. Legacy
// cross-subscription assignments (e.g. the KMS Plugin - Dev role on the mock MSI
// principals) still reference these home definitions, so shrinking the scope now would
// orphan them (RoleScopeBeingRemovedContainsAssignments). Once the legacy assignments
// are cleaned up, this param and the concat below can be dropped.
@description('E2E customer subscription IDs kept in assignableScopes during the role-definition migration so legacy cross-subscription assignments are not orphaned')
param e2eTestSubscriptions array = []

var e2eTestSubscriptionScopes = [for subscriptionId in e2eTestSubscriptions: '/subscriptions/${subscriptionId}']

resource roleDef 'Microsoft.Authorization/roleDefinitions@2022-04-01' = [
  for role in roles: {
    name: guid(role.roleName)
    properties: {
      roleName: role.roleName
      description: role.roleDescription
      type: 'CustomRole'
      permissions: [
        {
          actions: role.actions
          notActions: role.notActions
          dataActions: role.dataActions
          notDataActions: role.notDataActions
        }
      ]
      assignableScopes: concat(
        [
          subscription().id
        ],
        e2eTestSubscriptionScopes
      )
    }
  }
]
