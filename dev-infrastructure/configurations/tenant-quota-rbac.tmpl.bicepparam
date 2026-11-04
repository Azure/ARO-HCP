using '../templates/tenant-quota-rbac.bicep'

param applicationName = '{{ .opstool.tenantQuota.tenants.redHat0.applicationName }}'

// Same subscription set the tenant-quota chart derives for redHat0: all ci.<env>
// infrastructure and E2E subscriptions, entries formatted as id=name.
param subscriptionIds = map(
  filter(
    split('{{ .ci.dev.infrastructureSubscriptions }},{{ .ci.dev.e2eSubscriptions }},{{ .ci.int.e2eSubscriptions }},{{ .ci.stg.e2eSubscriptions }},{{ .ci.prod.e2eSubscriptions }}', ','),
    pair => !empty(pair)
  ),
  pair => split(pair, '=')[0]
)
