@description('CI bot applications to create, one per target environment')
param bots array

@description('Service management reference ID for the applications')
param serviceManagementReference string = ''

// Microsoft Graph app ID (constant across all tenants)
var msGraphAppId = '00000003-0000-0000-c000-000000000000'
// Application.ReadWrite.OwnedBy (Role / application permission)
var applicationReadWriteOwnedByRoleId = '18a4783c-866b-4cc7-a460-3d5e5662c884'
// User.Read (Scope / delegated permission)
var userReadScopeId = 'e1fe6dd8-ba31-4d61-89e7-88639da4683d'
// Directory.Read.All (Role / application permission)
var directoryReadAllRoleId = '7ab1d382-f21e-4acd-a863-ba3e13f7da61'

module botApp '../modules/entra/app.bicep' = [
  for bot in bots: {
    name: 'ci-bot-${bot.envName}'
    params: {
      applicationName: bot.applicationName
      uniqueName: toLower(replace(bot.applicationName, ' ', '-'))
      manageSp: true
      serviceManagementReference: serviceManagementReference
      requiredResourceAccess: [
        {
          resourceAppId: msGraphAppId
          resourceAccess: [
            {
              id: applicationReadWriteOwnedByRoleId
              type: 'Role'
            }
            {
              id: userReadScopeId
              type: 'Scope'
            }
            {
              id: directoryReadAllRoleId
              type: 'Role'
            }
          ]
        }
      ]
    }
  }
]
