import {
  csvToArray
} from '../common.bicep'

extension microsoftGraphBeta

// Application identity
@description('Display name for the Entra application')
param applicationName string

@description('URL-safe unique identifier for the Entra application. Defaults to applicationName. Must not contain spaces.')
param uniqueName string = applicationName

@description('Comma-separated list of owner object IDs for the application and service principal. When empty, Azure AD default behavior applies (caller is added as owner).')
param ownerIds string = ''

@description('Whether to create the service principal for this application')
param manageSp bool

@description('Trusted subject name and issuer pairs for SNI authentication')
param trustedSubjectNameAndIssuers array = []

@description('Service management reference ID for the application. When empty, the property is omitted from the Graph request.')
param serviceManagementReference string = ''

@description('Whether the application is a fallback public client')
param isFallbackPublicClient bool = true

@description('Requested access token version (1 or 2). Default is 2.')
param requestedAccessTokenVersion int = 2

@description('Key credentials for the application (e.g. certificate-based auth)')
param keyCredentials array = []

@description('Required resource access declarations (Graph API permissions, etc.)')
param requiredResourceAccess array = []

var hasExplicitOwners = !empty(ownerIds)
var ownerIdArray = hasExplicitOwners ? csvToArray(ownerIds) : []

resource entraApp 'Microsoft.Graph/applications@beta' = {
  displayName: applicationName
  isFallbackPublicClient: isFallbackPublicClient
  signInAudience: 'AzureADMyOrg'
  uniqueName: uniqueName
  requiredResourceAccess: requiredResourceAccess
  serviceManagementReference: !empty(serviceManagementReference) ? serviceManagementReference : null
  api: {
    requestedAccessTokenVersion: requestedAccessTokenVersion
  }
  trustedSubjectNameAndIssuers: trustedSubjectNameAndIssuers
  owners: hasExplicitOwners
    ? {
        relationships: ownerIdArray
      }
    : null
  keyCredentials: keyCredentials
}

resource servicePrincipal 'Microsoft.Graph/servicePrincipals@beta' = if (manageSp) {
  appId: entraApp.appId
  owners: hasExplicitOwners
    ? {
        relationships: ownerIdArray
      }
    : null
}

@description('The application (client) ID')
output appId string = entraApp.appId

@description('The application object ID (used for Graph API calls like addPassword)')
output appObjectId string = entraApp.id

@description('The service principal object ID (empty string when manageSp is false)')
#disable-next-line BCP318
output principalId string = manageSp ? servicePrincipal.id : ''
