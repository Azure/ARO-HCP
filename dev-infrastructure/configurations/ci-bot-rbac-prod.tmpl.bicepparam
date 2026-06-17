using '../templates/ci-bot-rbac.bicep'

param botApplicationName = '{{ .ci.prod.bot.applicationName }}'

param e2eSubscriptionIds = [
{{ range .ci.prod.e2eSubscriptions }}  '{{ .id }}'
{{ end }}]

param infrastructureSubscriptions = []
