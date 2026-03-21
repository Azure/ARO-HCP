import { csvToArray } from '../modules/common.bicep'

param miseApplicationName string
param miseApplicationOwnerIds string
param miseApplicationDeploy bool

extension microsoftGraphBeta

resource miseApp 'Microsoft.Graph/applications@beta' = if (miseApplicationDeploy) {
  displayName: miseApplicationName
  signInAudience: 'AzureADMyOrg'
  uniqueName: miseApplicationName
  requiredResourceAccess: []
  serviceManagementReference: 'b8e9ef87-cd63-4085-ab14-1c637806568c'
  api: {
    requestedAccessTokenVersion: 2
  }
  owners: {
    relationships: [for ownerId in csvToArray(miseApplicationOwnerIds): ownerId]
  }
}
