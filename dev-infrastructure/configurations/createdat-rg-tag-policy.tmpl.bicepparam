using '../templates/createdat-rg-tag-policy.bicep'

param e2eSubscriptionIds = empty('{{ $sep := "" }}{{ range $subscription := .ci.dev.e2eSubscriptions }}{{ $sep }}{{ $subscription.id }}{{ $sep = "," }}{{ end }}') ? [] : split('{{ $sep := "" }}{{ range $subscription := .ci.dev.e2eSubscriptions }}{{ $sep }}{{ $subscription.id }}{{ $sep = "," }}{{ end }}', ',')
