using '../templates/ci-bot-rbac.bicep'

param botApplicationName = '{{ .ci.int.bot.applicationName }}'

param e2eSubscriptionIds = empty('{{ .ci.int.e2eSubscriptions }}') ? [] : map(split('{{ .ci.int.e2eSubscriptions }}', ','), pair => split(pair, '=')[0])

param infrastructureSubscriptions = []
