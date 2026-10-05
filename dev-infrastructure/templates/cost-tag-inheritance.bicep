targetScope = 'subscription'

@description('DEV E2E subscriptions whose usage records should inherit subscription and resource-group tags')
param subscriptionIds array

module tagInheritance './cost-tag-inheritance-subscription.bicep' = [
  for subscriptionId in subscriptionIds: {
    name: 'cost-tag-inheritance-${subscriptionId}'
    scope: subscription(subscriptionId)
  }
]
