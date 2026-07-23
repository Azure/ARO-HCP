// Creates the mock identity Entra applications and their service principals and
// configures them for Subject Name and Issuer (SNI) certificate authentication
// via ../modules/entra/app.bicep (trustedSubjectNameAndIssuers).
//
// CERTIFICATE PREREQUISITE (manual / out of band):
// This template does NOT create or rotate the certificates themselves — it only
// declares which subject name (certDns) each app trusts. The matching
// certificates must already exist in the environment Key Vault
// (aro-hcp-dev-svc-kv for DEV, aro-hcp-int-kv for INT) and be issued to the
// services that authenticate as these identities. SNI validates the presented
// certificate's subject name and issuer rather than pinning a specific public
// key, so certificate rotation works without redeploying this template as long
// as the new certificate keeps the same subject (certDns).
// For a fresh bootstrap or a subject-name change, create the Key Vault
// certificate first (see docs/ci/dev-mock-identities.md), then deploy this
// template. Certificate provisioning used to be handled by the retired
// imperative mock-identity flow; it was intentionally dropped in favour of the
// declarative Entra-app model, so cert provisioning is now a documented manual
// prerequisite.
@description('Shared mock identity definitions: array of {applicationName, certDns}')
param identities array

@description('Number of pooled MSI mock identities to create')
param poolSize int = 0

@description('Base application name for pooled identities (e.g. aro-dev-msi-mock-pool)')
@minLength(1)
param poolAppBaseName string

@description('Base certificate DNS for pooled identities (e.g. msimockpool.hcp.osadev.cloud)')
@minLength(1)
param poolCertBaseDns string

var selfSignedAuthorityId = '00000000-0000-0000-0000-000000000001'

module sharedApps '../modules/entra/app.bicep' = [
  for identity in identities: {
    name: 'mock-app-${identity.applicationName}'
    params: {
      applicationName: identity.applicationName
      uniqueName: toLower(replace(identity.applicationName, ' ', '-'))
      manageSp: true
      trustedSubjectNameAndIssuers: [
        {
          authorityId: selfSignedAuthorityId
          subjectName: identity.certDns
        }
      ]
    }
  }
]

module poolApps '../modules/entra/app.bicep' = [
  for i in range(0, poolSize): {
    name: 'mock-app-pool-${i}'
    params: {
      applicationName: '${poolAppBaseName}-${i}'
      uniqueName: toLower(replace('${poolAppBaseName}-${i}', ' ', '-'))
      manageSp: true
      trustedSubjectNameAndIssuers: [
        {
          authorityId: selfSignedAuthorityId
          subjectName: '${i}.${poolCertBaseDns}'
        }
      ]
    }
  }
]
