using '../templates/ci-bot-rbac.bicep'

param botApplicationName = '{{ .ci.stg.bot.applicationName }}'

param e2eSubscriptionIds = empty('{{ .ci.stg.e2eSubscriptions }}') ? [] : map(split('{{ .ci.stg.e2eSubscriptions }}', ','), pair => split(pair, '=')[0])

param infrastructureSubscriptions = []
