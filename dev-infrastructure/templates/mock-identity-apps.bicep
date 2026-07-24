// Creates the mock identity Entra applications and their service principals and
// configures them for Subject Name and Issuer (SNI) certificate authentication
// via ../modules/entra/app.bicep (trustedSubjectNameAndIssuers).
//
// CERTIFICATE CREATION (separate step, not this template):
// This template does NOT create or rotate the certificates themselves — it only
// declares which subject name (certDns) each app trusts. Bicep cannot create Key
// Vault certificates, so they are created out of band by `make
// create-mock-identity-certs` (DEV) / `make create-int-mock-identity-certs`
// (INT), which call scripts/create-kv-cert.sh (idempotent az keyvault
// certificate create) into the environment Key Vault (aro-hcp-dev-svc-kv for
// DEV, aro-hcp-int-kv for INT). SNI validates the presented certificate's
// subject name and issuer rather than pinning a specific public key, so
// certificate rotation works without redeploying this template as long as the
// new certificate keeps the same subject (certDns).
// For a fresh bootstrap or a subject-name change, run the cert target first (see
// docs/ci/dev-mock-identities.md), then deploy this template.
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
