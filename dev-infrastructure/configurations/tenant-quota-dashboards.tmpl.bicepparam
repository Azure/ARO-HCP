using '../../tooling/tenant-quota/dashboards.bicep'

param azureMonitorWorkspaceId = '__azureMonitorWorkspaceId__'
param tenantName = '{{ .opstool.tenantQuota.tenants.redHat0.tenantName }}'

param devSubscriptions = concat(
  empty('{{ .ci.dev.infrastructureSubscriptions }}') ? [] : map(split('{{ .ci.dev.infrastructureSubscriptions }}', ','), pair => { id: split(pair, '=')[0], name: split(pair, '=')[1] }),
  empty('{{ .ci.dev.e2eSubscriptions }}') ? [] : map(split('{{ .ci.dev.e2eSubscriptions }}', ','), pair => { id: split(pair, '=')[0], name: split(pair, '=')[1] })
)

param devRegions = split('{{ .ci.dev.regions }}', ',')

param intSubscriptions = empty('{{ .ci.int.e2eSubscriptions }}') ? [] : map(split('{{ .ci.int.e2eSubscriptions }}', ','), pair => { id: split(pair, '=')[0], name: split(pair, '=')[1] })

param intRegions = split('{{ .ci.int.regions }}', ',')

param stgSubscriptions = empty('{{ .ci.stg.e2eSubscriptions }}') ? [] : map(split('{{ .ci.stg.e2eSubscriptions }}', ','), pair => { id: split(pair, '=')[0], name: split(pair, '=')[1] })

param stgRegions = split('{{ .ci.stg.regions }}', ',')

param prodSubscriptions = empty('{{ .ci.prod.e2eSubscriptions }}') ? [] : map(split('{{ .ci.prod.e2eSubscriptions }}', ','), pair => { id: split(pair, '=')[0], name: split(pair, '=')[1] })

param prodRegions = split('{{ .ci.prod.regions }}', ',')
