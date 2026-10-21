using '../templates/mock-identity-rbac.bicep'

param firstPartyAppName = '{{ .ci.dev.mockIdentities.firstParty.applicationName }}'
param armHelperAppName = '{{ .ci.dev.mockIdentities.armHelper.applicationName }}'
param msiMockAppName = '{{ .ci.dev.mockIdentities.msiMock.applicationName }}'
param poolAppBaseName = '{{ .ci.dev.mockIdentities.pool.appBaseName }}'
param poolSize = {{ .ci.dev.mockIdentities.pool.size }}
param armHelperPoolAppBaseName = '{{ .ci.dev.mockIdentities.armHelperPool.appBaseName }}'
param armHelperPoolSize = {{ .ci.dev.mockIdentities.armHelperPool.size }}

// DEV deploys mock identities into its own home (global) subscription, so grant
// them there in addition to the e2e subscriptions below.
param grantHomeSubscription = true

param e2eSubscriptionIds = [
{{ range .ci.dev.e2eSubscriptions }}  '{{ .id }}'
{{ end }}]
