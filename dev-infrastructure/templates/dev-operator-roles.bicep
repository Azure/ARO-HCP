// CustomRoles for Platform Workload Identities for development environment

targetScope = 'subscription'

@description('Array of roles for platform workload identity')
param roles array = []

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
      assignableScopes: [
        subscription().id
      ]
    }
  }
]
