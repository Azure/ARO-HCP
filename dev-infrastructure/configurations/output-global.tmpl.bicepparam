using '../templates/output-global.bicep'

param svcAcrName = '{{ .svcAcrName }}'
param ocpAcrName = '{{ .ocpAcrName }}'
param cxParentZoneName = '{{ .dns.cxParentZoneName }}'
param svcParentZoneName = '{{ .dns.svcParentZoneName }}'
param grafanaName = '{{ .monitoring.grafanaName }}'
