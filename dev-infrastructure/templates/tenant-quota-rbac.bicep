extension microsoftGraphBeta

@description('Display name of the tenant-quota collector Entra application')
param applicationName string

@description('Subscription IDs the collector monitors (every ci.<env> infrastructure and E2E subscription)')
param subscriptionIds array

resource collectorApp 'Microsoft.Graph/applications@beta' existing = {
  uniqueName: toLower(applicationName)
}

resource collectorSp 'Microsoft.Graph/servicePrincipals@beta' existing = {
  appId: collectorApp.appId
}

module readerRbac 'tenant-quota-rbac-subscription.bicep' = [
  for (subId, index) in subscriptionIds: {
    name: 'tenant-quota-reader-${index}'
    scope: subscription(subId)
    params: {
      collectorPrincipalId: collectorSp.id
    }
  }
]
