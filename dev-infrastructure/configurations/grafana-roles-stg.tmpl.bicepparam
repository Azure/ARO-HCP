// TRANSIENT: STG-global "V2" copy of grafana-roles.tmpl.bicepparam. Identical to the
// canonical file except the globally-unique resource names and the grafanaRoles
// assignments are sourced from the transient stgGlobalV2 block, so the Viewer role
// lands on arohcp-stg2 only and never touches the shared arohcp-stg. Removed at
// decommission.
using '../templates/grafana-roles.bicep'

param grafanaName = '{{ .stgGlobalV2.grafanaName }}'
param globalMSIName = '{{ .global.globalMSIName }}'
param grafanaRoles = '{{ .stgGlobalV2.grafanaRoles }}'
param azureFrontDoorProfileName = '{{ .stgGlobalV2.frontDoorName }}'
