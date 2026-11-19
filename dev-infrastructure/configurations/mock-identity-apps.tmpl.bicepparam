using '../templates/mock-identity-apps.bicep'

param ownerIds = '{{ .entraAppOwnerIds }}'

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
