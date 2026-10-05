using '../templates/cost-tag-inheritance.bicep'

// New E2E hosted-cluster subscriptions are included automatically. Infrastructure
// subscriptions require an explicit opt-in so the global subscription is excluded.
param subscriptionIds = [
{{ range .ci.dev.e2eSubscriptions }}  '{{ .id }}'
{{ end }}{{ range .ci.dev.infrastructureSubscriptions }}{{ if index . "costTagInheritance" }}  '{{ .id }}'
{{ end }}{{ end }}]
