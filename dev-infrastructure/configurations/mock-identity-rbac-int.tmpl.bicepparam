using '../templates/mock-identity-rbac.bicep'

param firstPartyAppName = '{{ .ci.int.mockIdentities.firstParty.applicationName }}'
param armHelperAppName = '{{ .ci.int.mockIdentities.armHelper.applicationName }}'
param msiMockAppName = '{{ .ci.int.mockIdentities.msiMock.applicationName }}'
param poolAppBaseName = '{{ .ci.int.mockIdentities.pool.appBaseName }}'
param poolSize = {{ .ci.int.mockIdentities.pool.size }}

param firstPartyRoleName = 'int-first-party'
param msiMockRoleName = 'int-msi-mock'

// grantHomeSubscription is intentionally left false (the default): the INT mock
// identity apps are deployed from the DEV global subscription, but INT's home
// subscription (ARO SRE Team - INT) is already listed in e2eSubscriptions below,
// so it receives its grants there. Setting it true would instead grant INT
// roles on the DEV global (deployment) subscription.

param e2eSubscriptionIds = [
{{ range .ci.int.e2eSubscriptions }}  '{{ .id }}'
{{ end }}]
