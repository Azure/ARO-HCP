using '../../tooling/tenant-quota/dashboards.bicep'

param azureMonitorWorkspaceId = '__azureMonitorWorkspaceId__'
param tenantName = '{{ (index .opstool.tenantQuota.tenants 0).tenantName }}'

param devSubscriptions = [
{{ range .ci.dev.infrastructureSubscriptions }}  { id: '{{ .id }}', name: '{{ .name }}' }
{{ end }}{{ range .ci.dev.e2eSubscriptions }}  { id: '{{ .id }}', name: '{{ .name }}' }
{{ end }}]

param devRegions = [
{{ range .ci.dev.dashboardRegions }}  '{{ . }}'
{{ end }}]

param intSubscriptions = [
{{ range .ci.int.e2eSubscriptions }}  { id: '{{ .id }}', name: '{{ .name }}' }
{{ end }}]

param intRegions = [
{{ range .ci.int.dashboardRegions }}  '{{ . }}'
{{ end }}]

param stgSubscriptions = [
{{ range .ci.stg.e2eSubscriptions }}  { id: '{{ .id }}', name: '{{ .name }}' }
{{ end }}]

param stgRegions = [
{{ range .ci.stg.dashboardRegions }}  '{{ . }}'
{{ end }}]

param prodSubscriptions = [
{{ range .ci.prod.e2eSubscriptions }}  { id: '{{ .id }}', name: '{{ .name }}' }
{{ end }}]

param prodRegions = [
{{ range .ci.prod.dashboardRegions }}  '{{ . }}'
{{ end }}]
