using '../templates/ci-bot-rbac.bicep'

param botApplicationName = '{{ .ci.prod.bot.applicationName }}'

param e2eSubscriptionIds = empty('{{ .ci.prod.e2eSubscriptions }}') ? [] : map(split('{{ .ci.prod.e2eSubscriptions }}', ','), pair => split(pair, '=')[0])

param infrastructureSubscriptions = []
