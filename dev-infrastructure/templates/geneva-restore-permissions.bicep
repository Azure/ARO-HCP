// Grants the global Shell-step managed identity the Microsoft Graph
// Application.ReadWrite.All application permission so the restore-entra-app
// step can restore a soft-deleted Geneva Actions Entra app/SP before
// geneva-identities recreates it.
//
// Restoring a soft-deleted application that the identity does not own
// requires the tenant-wide Application.ReadWrite.All role; the narrower
// Application.ReadWrite.OwnedBy role is not sufficient because the
// owner relationship is dropped when the object is deleted.
//
// See AROSLSRE-1130.

extension microsoftGraphBeta

@description('Name of the global managed identity that runs the restore Shell step')
param globalMSIName string

// Microsoft Graph app ID (constant across all tenants)
var msGraphAppId = '00000003-0000-0000-c000-000000000000'
// Application.ReadWrite.All (Role / application permission)
var applicationReadWriteAllRoleId = '1bfefb4e-e0b5-418b-a88f-73c46d2cc8e9'

resource globalMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: globalMSIName
}

resource msGraphServicePrincipal 'Microsoft.Graph/servicePrincipals@beta' existing = {
  appId: msGraphAppId
}

resource applicationReadWriteAllGrant 'Microsoft.Graph/appRoleAssignedTo@beta' = {
  appRoleId: applicationReadWriteAllRoleId
  principalId: globalMSI.properties.principalId
  resourceId: msGraphServicePrincipal.id
}
