using '../templates/mock-identity-rbac.bicep'

param firstPartyAppName = '{{ .ci.int.mockIdentities.firstParty.applicationName }}'
param armHelperAppName = '{{ .ci.int.mockIdentities.armHelper.applicationName }}'
param msiMockAppName = '{{ .ci.int.mockIdentities.msiMock.applicationName }}'
param poolAppBaseName = '{{ .ci.int.mockIdentities.pool.appBaseName }}'
param poolSize = {{ .ci.int.mockIdentities.pool.size }}

param firstPartyRoleName = 'int-first-party'
param msiMockRoleName = 'int-msi-mock'

param e2eSubscriptionIds = [
{{ range .ci.int.e2eSubscriptions }}  '{{ .id }}'
{{ end }}]
