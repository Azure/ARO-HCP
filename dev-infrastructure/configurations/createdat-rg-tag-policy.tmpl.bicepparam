using '../templates/createdat-rg-tag-policy.bicep'

param e2eSubscriptionIds = concat(
  empty('{{ .ci.dev.e2eSubscriptions }}') ? [] : map(split('{{ .ci.dev.e2eSubscriptions }}', ','), pair => split(pair, '=')[0]),
  empty('{{ .ci.stg.e2eSubscriptions }}') ? [] : map(split('{{ .ci.stg.e2eSubscriptions }}', ','), pair => split(pair, '=')[0]),
  empty('{{ .ci.prod.e2eSubscriptions }}') ? [] : map(split('{{ .ci.prod.e2eSubscriptions }}', ','), pair => split(pair, '=')[0])
)
