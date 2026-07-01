// TRANSIENT: STG-global "V2" copy of kusto.tmpl.bicepparam. Identical to the
// canonical file except the (globally-unique) Kusto cluster name is sourced from
// the transient stgGlobalV2 block so it does not collide with the live cluster.
// Removed at decommission.
using '../templates/kusto.bicep'

param location = '{{ .kusto.location }}'

param sku = '{{ .kusto.sku }}'
param tier = '{{ .kusto.tier }}'

param kustoName = '{{ .stgGlobalV2.kustoName }}'

param manageInstance = {{ .kusto.manageInstance }}

param serviceLogsDatabase = '{{ .kusto.serviceLogsDatabase }}'

param hostedControlPlaneLogsDatabase = '{{ .kusto.hostedControlPlaneLogsDatabase }}'

param adminGroups = '{{ .kusto.adminGroups }}'

param viewerGroups = '{{ .kusto.viewerGroups }}'

param viewerIdentities = '{{ .kusto.viewerIdentities }}'

param autoScaleMin = {{ .kusto.autoScaleMin }}

param autoScaleMax = {{ .kusto.autoScaleMax }}

param enableAutoScale = {{ .kusto.enableAutoScale }}
