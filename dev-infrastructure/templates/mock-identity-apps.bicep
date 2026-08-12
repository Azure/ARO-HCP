// Creates the mock identity Entra applications and their service principals via
// ../modules/entra/app.bicep. It does NOT configure any authentication mechanism
// on the apps — that is done out of band by pinning the certificate (see below).
//
// AUTHENTICATION: PINNED CERTIFICATE (keyCredentials)
// The mock identities authenticate with self-signed Key Vault certificates.
// Each app's certificate public key is registered as a pinned
// `keyCredential` (AsymmetricX509Cert / Verify) by the privileged dev-ci
// pipeline's Shell steps, which invoke `tooling/entra-app-credentials` to read
// or create the cert in Key Vault (Bicep cannot manage Key Vault certificate
// material) and PATCH it onto the app via Microsoft Graph. Entra then
// authenticates by matching the presented leaf's thumbprint against the pinned
// keyCredentials. This module passes no trusted subject name and issuer pairs
// and deliberately does not manage `keyCredentials`, so redeploying this
// template does not wipe the pinned certificates.
//
// CERTIFICATE LIFECYCLE:
// The privileged pipeline creates missing certificates after deploying these
// apps and before applying RBAC. Rotation is intentionally excluded from the
// pipeline and requires an explicitly confirmed CLI invocation; see
// docs/ci/dev-mock-identities.md.
@description('Shared mock identity definitions: array of {applicationName, certDns}')
param identities array

@description('Comma-separated owner object IDs for every mock identity application and service principal')
@minLength(1)
param ownerIds string

@description('Number of pooled MSI mock identities to create')
param poolSize int = 0

@description('Base application name for pooled identities (e.g. aro-dev-msi-mock-pool)')
@minLength(1)
param poolAppBaseName string

@description('Number of pooled ARM helper identities to create')
param armHelperPoolSize int = 0

@description('Base application name for pooled ARM helper identities')
@minLength(1)
param armHelperPoolAppBaseName string = 'unused'

module sharedApps '../modules/entra/app.bicep' = [
  for identity in identities: {
    name: 'mock-app-${identity.applicationName}'
    params: {
      applicationName: identity.applicationName
      uniqueName: toLower(replace(identity.applicationName, ' ', '-'))
      ownerIds: ownerIds
      ownerRelationshipSemantics: 'replace'
      manageSp: true
    }
  }
]

module poolApps '../modules/entra/app.bicep' = [
  for i in range(0, poolSize): {
    name: 'mock-app-pool-${i}'
    params: {
      applicationName: '${poolAppBaseName}-${i}'
      uniqueName: toLower(replace('${poolAppBaseName}-${i}', ' ', '-'))
      ownerIds: ownerIds
      ownerRelationshipSemantics: 'replace'
      manageSp: true
    }
  }
]

module armHelperPoolApps '../modules/entra/app.bicep' = [
  for i in range(0, armHelperPoolSize): {
    name: 'mock-app-arm-helper-pool-${i}'
    params: {
      applicationName: '${armHelperPoolAppBaseName}-${i}'
      uniqueName: toLower(replace('${armHelperPoolAppBaseName}-${i}', ' ', '-'))
      ownerIds: ownerIds
      ownerRelationshipSemantics: 'replace'
      manageSp: true
    }
  }
]
