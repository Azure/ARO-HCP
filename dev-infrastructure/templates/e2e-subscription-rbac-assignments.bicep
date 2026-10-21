targetScope = 'subscription'

@description('Subscription IDs that should receive the shared E2E role assignments')
param customerSubscriptionIds array = []

@description('Subscription ID that owns the shared custom role definitions')
param homeSubscriptionId string

@description('Principal ID for aro-dev-first-party2')
param firstPartyPrincipalId string

@description('Principal ID for aro-dev-arm-helper2')
param armHelperPrincipalId string

@description('Principal ID for aro-dev-msi-mock2')
param miMockPrincipalId string

@description('Pooled MSI mock principals that also need customer-subscription access')
param msiMockPoolPrincipals array = []

@description('Custom role name for the first-party mock principal')
param firstPartyRoleName string = 'dev-first-party-mock'

@description('Custom role name for the MSI mock principal')
param msiMockRoleName string = 'dev-msi-mock'

@description('Historical custom role name for the KMS plugin role')
param kmsPluginRoleName string = 'Azure Red Hat OpenShift KMS Plugin - Dev'

@description('Optional legacy role-assignment IDs keyed by customer subscription for temporary adoption of pre-existing assignments')
param legacyAssignmentIdsBySubscription object = {}

module customerSubscriptionAssignments './e2e-subscription-rbac-assignment-subscription.bicep' = [
  for (customerSubscriptionId, index) in customerSubscriptionIds: {
    name: 'cust-sub-rbac-${index}'
    scope: subscription(customerSubscriptionId)
    params: {
      homeSubscriptionId: homeSubscriptionId
      firstPartyPrincipalId: firstPartyPrincipalId
      armHelperPrincipalId: armHelperPrincipalId
      miMockPrincipalId: miMockPrincipalId
      msiMockPoolPrincipals: msiMockPoolPrincipals
      firstPartyRoleName: firstPartyRoleName
      msiMockRoleName: msiMockRoleName
      kmsPluginRoleName: kmsPluginRoleName
      legacyAssignmentIds: legacyAssignmentIdsBySubscription[?customerSubscriptionId] ?? {}
    }
  }
]
