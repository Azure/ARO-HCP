using '../templates/mock-identity-apps.bicep'

param identities = [
  {
    applicationName: '{{ .ci.int.mockIdentities.firstParty.applicationName }}'
    certDns: '{{ .ci.int.mockIdentities.firstParty.certDns }}'
  }
  {
    applicationName: '{{ .ci.int.mockIdentities.armHelper.applicationName }}'
    certDns: '{{ .ci.int.mockIdentities.armHelper.certDns }}'
  }
  {
    applicationName: '{{ .ci.int.mockIdentities.msiMock.applicationName }}'
    certDns: '{{ .ci.int.mockIdentities.msiMock.certDns }}'
  }
]

param poolSize = {{ .ci.int.mockIdentities.pool.size }}
param poolAppBaseName = '{{ .ci.int.mockIdentities.pool.appBaseName }}'
param poolCertBaseDns = '{{ .ci.int.mockIdentities.pool.certBaseDns }}'
