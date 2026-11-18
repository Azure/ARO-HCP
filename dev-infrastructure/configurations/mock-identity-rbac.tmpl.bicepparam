using '../templates/mock-identity-rbac.bicep'

param firstPartyAppName = '{{ .ci.dev.mockIdentities.firstParty.applicationName }}'
param armHelperAppName = '{{ .ci.dev.mockIdentities.armHelper.applicationName }}'
param msiMockAppName = '{{ .ci.dev.mockIdentities.msiMock.applicationName }}'

// DEV deploys mock identities into its own home (global) subscription, so grant
// them there in addition to the e2e subscriptions below.
param grantHomeSubscription = true

param e2eSubscriptionIds = empty('{{ .ci.dev.e2eSubscriptions }}') ? [] : map(split('{{ .ci.dev.e2eSubscriptions }}', ','), pair => split(pair, '=')[0])
