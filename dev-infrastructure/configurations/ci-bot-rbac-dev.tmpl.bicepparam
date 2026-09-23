using '../templates/ci-bot-rbac.bicep'

param botApplicationName = '{{ .ci.dev.bot.applicationName }}'

param e2eSubscriptionIds = [
{{ range .ci.dev.e2eSubscriptions }}  '{{ .id }}'
{{ end }}]

param infrastructureSubscriptions = [
{{ range .ci.dev.infrastructureSubscriptions }}  {
    id: '{{ .id }}'
    isGlobalSubscription: {{ if index . "isGlobalSubscription" }}{{ .isGlobalSubscription }}{{ else }}false{{ end }}
  }
{{ end }}]

param grantAksRbac = true
