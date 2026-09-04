extension microsoftGraphBeta

@description('Display name of the CI bot application')
param botApplicationName string

@description('E2E subscription IDs where the bot needs RBAC')
param e2eSubscriptionIds array = []

@description('Infrastructure subscription entries: objects with id and isGlobalSubscription')
param infrastructureSubscriptions array = []

@description('Whether to grant AKS RBAC Cluster Admin (only needed in DEV)')
param grantAksRbac bool = false

resource botApp 'Microsoft.Graph/applications@beta' existing = {
  uniqueName: toLower(replace(botApplicationName, ' ', '-'))
}

resource botSp 'Microsoft.Graph/servicePrincipals@beta' existing = {
  appId: botApp.appId
}

// E2E (customer test) subscriptions get the standard test-runner roles
// (Contributor, RBAC Admin, plus AKS RBAC Cluster Admin when grantAksRbac is set).
// Key Vault Admin and the global-only data-plane roles (Grafana Admin) are
// reserved for infrastructure subscriptions, which opt in per-sub below, so
// isGlobalSubscription and grantKeyVaultAdmin are intentionally always false here.
module e2eRbac 'ci-bot-rbac-subscription.bicep' = [
  for (subId, index) in e2eSubscriptionIds: {
    name: 'ci-bot-e2e-rbac-${index}'
    scope: subscription(subId)
    params: {
      botPrincipalId: botSp.id
      isGlobalSubscription: false
      grantAksRbac: grantAksRbac
      grantKeyVaultAdmin: false
    }
  }
]

module infraRbac 'ci-bot-rbac-subscription.bicep' = [
  for (sub, index) in infrastructureSubscriptions: {
    name: 'ci-bot-infra-rbac-${index}'
    scope: subscription(sub.id)
    params: {
      botPrincipalId: botSp.id
      isGlobalSubscription: sub.?isGlobalSubscription ?? false
      grantAksRbac: grantAksRbac
      grantKeyVaultAdmin: sub.?grantKeyVaultAdmin ?? false
    }
  }
]
