targetScope = 'subscription'

// Fans the createdAt resource-group tag policy out to every e2e test
// (hosted-cluster) subscription. Runs from the dev-ci global subscription
// (Owner via the privileged entrypoint) and deploys the policy definition +
// assignment into each target subscription via a subscription-scoped module.
// Mirrors the e2e-subscription-rbac-assignments.bicep cross-subscription
// fan-out pattern.

@description('e2e test (hosted-cluster) subscription IDs that should receive the createdAt resource-group tag policy')
param e2eSubscriptionIds array = []

module createdAtPolicy './createdat-rg-tag-policy-subscription.bicep' = [
  for (e2eSubscriptionId, index) in e2eSubscriptionIds: {
    name: 'createdat-rg-tag-policy-${index}'
    scope: subscription(e2eSubscriptionId)
  }
]
