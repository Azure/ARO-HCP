using '../templates/ci-bot-rbac.bicep'

param botApplicationName = '{{ .ci.int.bot.applicationName }}'

param e2eSubscriptionIds = [
{{ range .ci.int.e2eSubscriptions }}  '{{ .id }}'
{{ end }}]

param infrastructureSubscriptions = []
