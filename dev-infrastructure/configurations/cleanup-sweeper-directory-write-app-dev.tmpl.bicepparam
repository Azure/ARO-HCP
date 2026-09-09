using '../modules/entra/app.bicep'

// Dedicated Entra app granted Microsoft Graph Application.ReadWrite.All, used
// by the cleanup-sweeper shared-leftovers workflow to purge aged soft-deleted
// apps/SPs (see docs/ci/cleanup.md).
// Kept separate from the mock identities and the CI bots: it needs a
// materially higher privilege than either, so it must not share an app with a
// lower-privilege identity. Application.ReadWrite.All still requires manual
// tenant-admin consent after deployment; Bicep cannot grant admin consent for
// Graph application permissions.
param applicationName = '{{ .ci.dev.cleanupSweeperDirectoryWriteApp.applicationName }}'
param ownerIds = '{{ .entraAppOwnerIds }}'
param ownerRelationshipSemantics = 'replace'
param manageSp = true
param requiredResourceAccess = [
  {
    // Microsoft Graph
    resourceAppId: '00000003-0000-0000-c000-000000000000'
    resourceAccess: [
      {
        // Application.ReadWrite.All (application permission)
        id: '1bfefb4e-e0b5-418b-a88f-73c46d2cc8e9'
        type: 'Role'
      }
    ]
  }
]
