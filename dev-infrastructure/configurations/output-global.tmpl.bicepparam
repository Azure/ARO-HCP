using '../templates/output-global.bicep'

param svcAcrName = '{{ .acr.svc.name }}'
param ocpAcrName = '{{ .acr.ocp.name }}'
param cxParentZoneName = '{{ .dns.cxParentZoneName }}'
param svcParentZoneName = '{{ .dns.svcParentZoneName }}'
param grafanaName = '{{ .monitoring.grafanaName }}'
param azureFrontDoorProfileName = '{{ .oidc.frontdoor.name }}'
param globalMSIName = '{{ .global.globalMSIName }}'
param globalKVName = '{{ .global.keyVault.name }}'
param genevaActionsKVName = '{{ .geneva.actions.keyVault.name }}'
param genevaActionsEnabled = {{ .geneva.actions.enabled }}
