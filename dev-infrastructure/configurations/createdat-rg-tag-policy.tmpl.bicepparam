using '../templates/createdat-rg-tag-policy.bicep'

param e2eSubscriptionIds = empty('{{ .ci.dev.e2eSubscriptions }}') ? [] : map(split('{{ .ci.dev.e2eSubscriptions }}', ','), pair => split(pair, '=')[0])
