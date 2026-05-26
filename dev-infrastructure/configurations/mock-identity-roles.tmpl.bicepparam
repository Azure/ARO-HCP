using '../templates/mock-identity-roles.bicep'

param globalResourceGroupName = '{{ .global.rg }}'

param e2eTestSubscriptions = [
  '{{ (index .devCi.e2eSubscriptionRbac.customerSubscriptions 0).id }}'
  '{{ (index .devCi.e2eSubscriptionRbac.customerSubscriptions 1).id }}'
]
