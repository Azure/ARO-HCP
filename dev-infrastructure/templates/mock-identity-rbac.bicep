@description('Application name for the first-party mock identity')
param firstPartyAppName string

@description('Application name for the ARM helper mock identity')
param armHelperAppName string

@description('Application name for the MSI mock identity')
param msiMockAppName string

@description('Base application name for pooled MSI mock identities')
@minLength(1)
param poolAppBaseName string

@description('Number of pooled MSI mock identities')
param poolSize int = 0

@description('Subscription IDs that should receive mock identity role assignments')
param e2eSubscriptionIds array = []

@description('Custom role name for the first-party mock principal')
param firstPartyRoleName string = 'dev-first-party-mock'

@description('Custom role name for the MSI mock principal')
param msiMockRoleName string = 'dev-msi-mock'

module firstPartyLookup './entra-app-lookup.bicep' = {
  name: 'lookup-first-party'
  params: {
    applicationName: firstPartyAppName
    manage: true
    lookupSp: true
  }
}

module armHelperLookup './entra-app-lookup.bicep' = {
  name: 'lookup-arm-helper'
  params: {
    applicationName: armHelperAppName
    manage: true
    lookupSp: true
  }
}

module msiMockLookup './entra-app-lookup.bicep' = {
  name: 'lookup-msi-mock'
  params: {
    applicationName: msiMockAppName
    manage: true
    lookupSp: true
  }
}

module poolLookups './entra-app-lookup.bicep' = [
  for i in range(0, poolSize): {
    name: 'lookup-pool-${i}'
    params: {
      applicationName: '${poolAppBaseName}-${i}'
      manage: true
      lookupSp: true
    }
  }
]

module homeSubscriptionRbac './e2e-subscription-rbac-assignment-subscription.bicep' = {
  name: 'mock-rbac-home'
  scope: subscription()
  params: {
    firstPartyPrincipalId: firstPartyLookup.outputs.principalId
    armHelperPrincipalId: armHelperLookup.outputs.principalId
    miMockPrincipalId: msiMockLookup.outputs.principalId
    msiMockPoolPrincipals: [
      for i in range(0, poolSize): {
        name: '${poolAppBaseName}-${i}'
        principalId: poolLookups[i].outputs.principalId
      }
    ]
    firstPartyRoleName: firstPartyRoleName
    msiMockRoleName: msiMockRoleName
  }
}

module e2eSubscriptionRbac './e2e-subscription-rbac-assignment-subscription.bicep' = [
  for (subId, index) in e2eSubscriptionIds: {
    name: 'mock-rbac-e2e-${index}'
    scope: subscription(subId)
    params: {
      firstPartyPrincipalId: firstPartyLookup.outputs.principalId
      armHelperPrincipalId: armHelperLookup.outputs.principalId
      miMockPrincipalId: msiMockLookup.outputs.principalId
      msiMockPoolPrincipals: [
        for i in range(0, poolSize): {
          name: '${poolAppBaseName}-${i}'
          principalId: poolLookups[i].outputs.principalId
        }
      ]
      firstPartyRoleName: firstPartyRoleName
      msiMockRoleName: msiMockRoleName
    }
  }
]
