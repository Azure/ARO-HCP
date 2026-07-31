// TRANSIENT: STG-global "V2" copy of grafana-roles.tmpl.bicepparam. Identical to the
// canonical file except the globally-unique resource names and the grafanaRoles
// assignments are sourced from the transient stgGlobalV2 block, so the Viewer role
// lands on arohcp-stg2 only and never touches the shared arohcp-stg. Removed at
// decommission.
using '../templates/grafana-roles.bicep'

param grafanaName = '{{ .stgGlobalV2.grafanaName }}'
param globalMSIName = '{{ .global.globalMSIName }}'
// grafanaRoles is intentionally empty here: the EV2 compound identity that runs this
// ARM step cannot assign Group principals (roleAssignments/write is ABAC-restricted to
// PrincipalType == 'ServicePrincipal'). Group role assignments (the team Viewer) are
// handled by the grafana-group-roles Shell step, which reads stgGlobalV2.grafanaRoles
// and runs under the global EV2 MSI. The MSI Grafana Admin assignment is unaffected
// (grafana-roles.bicep sets it from globalMSIName, independent of grafanaRoles).
param grafanaRoles = ''
param azureFrontDoorProfileName = '{{ .stgGlobalV2.frontDoorName }}'
