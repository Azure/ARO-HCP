using '../templates/ci-bot-rbac.bicep'

param botApplicationName = '{{ .ci.dev.bot.applicationName }}'

param e2eSubscriptionIds = empty('{{ .ci.dev.e2eSubscriptions }}') ? [] : map(split('{{ .ci.dev.e2eSubscriptions }}', ','), pair => split(pair, '=')[0])

param infrastructureSubscriptions = empty('{{ .ci.dev.infrastructureSubscriptions }}') ? [] : map(split('{{ .ci.dev.infrastructureSubscriptions }}', ','), pair => {
  id: split(pair, '=')[0]
  isGlobalSubscription: contains('{{ .ci.dev.infrastructureSubscriptionsGlobals }}', split(pair, '=')[0])
})

param grantAksRbac = true
