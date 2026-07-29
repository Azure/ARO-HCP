using '../templates/e2e-subscription-rbac-assignments.bicep'

param customerSubscriptionIds = empty('{{ $sep := "" }}{{ range $subscription := .ci.dev.e2eSubscriptions }}{{ $sep }}{{ $subscription.id }}{{ $sep = "," }}{{ end }}') ? [] : split('{{ $sep := "" }}{{ range $subscription := .ci.dev.e2eSubscriptions }}{{ $sep }}{{ $subscription.id }}{{ $sep = "," }}{{ end }}', ',')

param firstPartyPrincipalId = '{{ .ci.dev.devMockIdentities.sharedPrincipals.firstParty.principalId }}'

param armHelperPrincipalId = '{{ .ci.dev.devMockIdentities.sharedPrincipals.armHelper.principalId }}'

param miMockPrincipalId = '{{ .ci.dev.devMockIdentities.sharedPrincipals.miMock.principalId }}'

param msiMockPoolPrincipals = [
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-0'
    principalId: '{{ (index .ci.dev.devMockIdentities.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-0").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-1'
    principalId: '{{ (index .ci.dev.devMockIdentities.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-1").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-2'
    principalId: '{{ (index .ci.dev.devMockIdentities.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-2").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-3'
    principalId: '{{ (index .ci.dev.devMockIdentities.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-3").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-4'
    principalId: '{{ (index .ci.dev.devMockIdentities.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-4").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-5'
    principalId: '{{ (index .ci.dev.devMockIdentities.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-5").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-6'
    principalId: '{{ (index .ci.dev.devMockIdentities.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-6").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-7'
    principalId: '{{ (index .ci.dev.devMockIdentities.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-7").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-8'
    principalId: '{{ (index .ci.dev.devMockIdentities.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-8").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-9'
    principalId: '{{ (index .ci.dev.devMockIdentities.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-9").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-10'
    principalId: '{{ (index .ci.dev.devMockIdentities.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-10").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-11'
    principalId: '{{ (index .ci.dev.devMockIdentities.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-11").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-12'
    principalId: '{{ (index .ci.dev.devMockIdentities.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-12").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-13'
    principalId: '{{ (index .ci.dev.devMockIdentities.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-13").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-14'
    principalId: '{{ (index .ci.dev.devMockIdentities.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-14").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-15'
    principalId: '{{ (index .ci.dev.devMockIdentities.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-15").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-16'
    principalId: '{{ (index .ci.dev.devMockIdentities.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-16").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-17'
    principalId: '{{ (index .ci.dev.devMockIdentities.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-17").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-18'
    principalId: '{{ (index .ci.dev.devMockIdentities.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-18").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-19'
    principalId: '{{ (index .ci.dev.devMockIdentities.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-19").principalId }}'
  }
]
