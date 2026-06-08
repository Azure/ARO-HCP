using '../templates/mock-identity-roles.bicep'

param globalResourceGroupName = '{{ .global.rg }}'

param e2eTestSubscriptions = empty('{{ range $index, $subscription := .devCi.e2eSubscriptionRbac.customerSubscriptions }}{{ if $index }},{{ end }}{{ $subscription.id }}{{ end }}') ? [] : split('{{ range $index, $subscription := .devCi.e2eSubscriptionRbac.customerSubscriptions }}{{ if $index }},{{ end }}{{ $subscription.id }}{{ end }}', ',')
