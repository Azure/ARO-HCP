targetScope = 'subscription'

@export()
var tokenManagementRoleName = 'token-mgmt-role'

resource tokenManagementRole 'Microsoft.Authorization/roleDefinitions@2022-04-01' = {
  name: guid(tokenManagementRoleName)
  properties: {
    roleName: 'ARO HCP ACR Token Management'
    type: 'customRole'
    assignableScopes: [
      subscription().id
    ]
    description: 'This role allows the management of tokens in the ACR'
    permissions: [
      {
        actions: [
          'Microsoft.ContainerRegistry/registries/tokens/read'
          'Microsoft.ContainerRegistry/registries/tokens/write'
          'Microsoft.ContainerRegistry/registries/tokens/delete'
          'Microsoft.ContainerRegistry/registries/generateCredentials/action'
          'Microsoft.ContainerRegistry/registries/tokens/operationStatuses/read'
          'Microsoft.ContainerRegistry/registries/scopeMaps/read'
        ]
      }
    ]
  }
}
