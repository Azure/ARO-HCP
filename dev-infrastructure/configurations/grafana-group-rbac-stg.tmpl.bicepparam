// TRANSIENT: STG-global "V2" only. Params for templates/grafana-group-rbac.bicep,
// which grants the global EV2 MSI a constrained RBAC Administrator role on the
// arohcp-stg2 Grafana so the grafana-group-roles Shell step can assign the team
// Grafana Viewer to a Group principal. Removed at decommission.
using '../templates/grafana-group-rbac.bicep'

param grafanaName = '{{ .stgGlobalV2.grafanaName }}'
param globalMSIName = '{{ .global.globalMSIName }}'
