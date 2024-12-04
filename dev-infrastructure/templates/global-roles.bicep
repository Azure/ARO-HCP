targetScope = 'subscription'

@description('Defines if the ACR token management role should be created')
param manageTokenRole bool

import * as tmr from '../modules/acr/token-role-name.bicep'

resource tokenManagementRole 'Microsoft.Authorization/roleDefinitions@2022-04-01' = if (manageTokenRole) {
  name: guid(tmr.tokenManagementRoleName)
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
