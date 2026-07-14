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
