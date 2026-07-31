using '../templates/mock-identity-apps.bicep'

param identities = [
  {
    applicationName: '{{ .ci.dev.mockIdentities.firstParty.applicationName }}'
    certDns: '{{ .ci.dev.mockIdentities.firstParty.certDns }}'
  }
  {
    applicationName: '{{ .ci.dev.mockIdentities.armHelper.applicationName }}'
    certDns: '{{ .ci.dev.mockIdentities.armHelper.certDns }}'
  }
  {
    applicationName: '{{ .ci.dev.mockIdentities.msiMock.applicationName }}'
    certDns: '{{ .ci.dev.mockIdentities.msiMock.certDns }}'
  }
]

param poolSize = {{ .ci.dev.mockIdentities.pool.size }}
param poolAppBaseName = '{{ .ci.dev.mockIdentities.pool.appBaseName }}'
param poolCertBaseDns = '{{ .ci.dev.mockIdentities.pool.certBaseDns }}'
