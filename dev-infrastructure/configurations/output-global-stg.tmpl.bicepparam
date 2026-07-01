// TRANSIENT: STG-global "V2" copy of output-global.tmpl.bicepparam. Identical to the
// canonical file except the globally-unique resource names and DNS parent zones are
// sourced from the transient stgGlobalV2 block. Removed at decommission.
using '../templates/output-global.bicep'

param svcAcrName = '{{ .stgGlobalV2.acrSvcName }}'
param ocpAcrName = '{{ .stgGlobalV2.acrOcpName }}'
param cxParentZoneName = '{{ .stgGlobalV2.cxParentZoneName }}'
param svcParentZoneName = '{{ .stgGlobalV2.svcParentZoneName }}'
param grafanaName = '{{ .stgGlobalV2.grafanaName }}'
param azureFrontDoorProfileName = '{{ .stgGlobalV2.frontDoorName }}'
param globalMSIName = '{{ .global.globalMSIName }}'
param globalKVName = '{{ .stgGlobalV2.globalKeyVaultName }}'
param genevaActionsKVName = '{{ .stgGlobalV2.genevaKeyVaultName }}'
