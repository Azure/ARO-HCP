extension microsoftGraphBeta

@description('Display name for the CIHealth authentication application')
param applicationName string

@description('Stable Microsoft Graph unique name for the CIHealth authentication application')
param applicationUniqueName string

@description('OAuth callback URL for the CIHealth authentication application')
param redirectUri string

resource application 'Microsoft.Graph/applications@beta' = {
  displayName: applicationName
  uniqueName: applicationUniqueName
  signInAudience: 'AzureADMyOrg'
  isFallbackPublicClient: false
  groupMembershipClaims: 'SecurityGroup'
  web: {
    redirectUris: [
      redirectUri
    ]
  }
  api: {
    requestedAccessTokenVersion: 2
  }
}

resource servicePrincipal 'Microsoft.Graph/servicePrincipals@beta' = {
  appId: application.appId
  appRoleAssignmentRequired: false
}

output appId string = application.appId
output appObjectId string = application.id
output principalId string = servicePrincipal.id
