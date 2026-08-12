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

@description('Whether explicit owners are appended to or replace existing owner relationships')
param ownerRelationshipSemantics 'append' | 'replace' = 'append'

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

// The Graph extension treats keyCredentials: null as an explicit invalid value,
// not as an omitted property. Keep separate conditional resources so callers
// that do not manage credentials leave the property out of the request entirely.
resource entraAppWithoutKeyCredentials 'Microsoft.Graph/applications@beta' = if (empty(keyCredentials)) {
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
        relationshipSemantics: ownerRelationshipSemantics
        relationships: ownerIdArray
      }
    : null
}

resource entraAppWithKeyCredentials 'Microsoft.Graph/applications@beta' = if (!empty(keyCredentials)) {
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
        relationshipSemantics: ownerRelationshipSemantics
        relationships: ownerIdArray
      }
    : null
  keyCredentials: keyCredentials
}

resource servicePrincipal 'Microsoft.Graph/servicePrincipals@beta' = if (manageSp) {
  #disable-next-line BCP318
  appId: empty(keyCredentials) ? entraAppWithoutKeyCredentials.appId : entraAppWithKeyCredentials.appId
  owners: hasExplicitOwners
    ? {
        relationshipSemantics: ownerRelationshipSemantics
        relationships: ownerIdArray
      }
    : null
}

@description('The application (client) ID')
#disable-next-line BCP318
output appId string = empty(keyCredentials) ? entraAppWithoutKeyCredentials.appId : entraAppWithKeyCredentials.appId

@description('The application object ID (used for Graph API calls like addPassword)')
#disable-next-line BCP318
output appObjectId string = empty(keyCredentials) ? entraAppWithoutKeyCredentials.id : entraAppWithKeyCredentials.id

@description('The service principal object ID (empty string when manageSp is false)')
#disable-next-line BCP318
output principalId string = manageSp ? servicePrincipal.id : ''
