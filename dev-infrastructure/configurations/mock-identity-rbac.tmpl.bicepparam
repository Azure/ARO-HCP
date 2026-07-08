using '../templates/mock-identity-rbac.bicep'

param firstPartyAppName = '{{ .ci.dev.mockIdentities.firstParty.applicationName }}'
param armHelperAppName = '{{ .ci.dev.mockIdentities.armHelper.applicationName }}'
param msiMockAppName = '{{ .ci.dev.mockIdentities.msiMock.applicationName }}'
param poolAppBaseName = '{{ .ci.dev.mockIdentities.pool.appBaseName }}'
param poolSize = {{ .ci.dev.mockIdentities.pool.size }}

param e2eSubscriptionIds = [
{{ range .ci.dev.e2eSubscriptions }}  '{{ .id }}'
{{ end }}]
