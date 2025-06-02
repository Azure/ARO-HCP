using '../templates/output-global.bicep'

param svcAcrName = '{{ .acr.svc.name }}'
param ocpAcrName = '{{ .acr.ocp.name }}'
param cxParentZoneName = '{{ .dns.cxParentZoneName }}'
param svcParentZoneName = '{{ .dns.svcParentZoneName }}'
param grafanaName = '{{ .monitoring.grafanaName }}'

param acrSvcSubscriptionId = '{{ .acr.svc.subscription }}'
param acrSvcResourceGroup = '{{ .acr.svc.rg }}'

param acrOcpSubscriptionId = '{{ .acr.ocp.subscription }}'
param acrOcpResourceGroup = '{{ .acr.ocp.rg }}'
