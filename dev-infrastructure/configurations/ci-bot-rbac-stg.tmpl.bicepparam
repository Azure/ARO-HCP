using '../templates/ci-bot-rbac.bicep'

param botApplicationName = '{{ .ci.stg.bot.applicationName }}'

param e2eSubscriptionIds = [
{{ range .ci.stg.e2eSubscriptions }}  '{{ .id }}'
{{ end }}]

param infrastructureSubscriptions = []
