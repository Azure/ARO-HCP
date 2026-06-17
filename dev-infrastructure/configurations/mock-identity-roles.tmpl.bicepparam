using '../templates/mock-identity-roles.bicep'

param globalResourceGroupName = '{{ .global.rg }}'

param e2eTestSubscriptions = empty('{{ range $index, $subscription := .ci.dev.e2eSubscriptions }}{{ if $index }},{{ end }}{{ $subscription.id }}{{ end }}') ? [] : split('{{ range $index, $subscription := .ci.dev.e2eSubscriptions }}{{ if $index }},{{ end }}{{ $subscription.id }}{{ end }}', ',')
